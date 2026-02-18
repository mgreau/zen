package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ghpkg "github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Find stale worktrees (merged PRs, old branches)",
	RunE:  runCleanup,
}

var (
	cleanupDays   int
	cleanupDelete bool
)

func init() {
	cleanupCmd.Flags().IntVarP(&cleanupDays, "days", "d", 30, "Consider worktrees older than N days as stale")
	cleanupCmd.Flags().BoolVar(&cleanupDelete, "delete", false, "Delete stale worktrees (with confirmation)")
	rootCmd.AddCommand(cleanupCmd)
}

type staleWorktree struct {
	worktree.Worktree
	Reason string `json:"stale_reason"`
}

func runCleanup(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	fmt.Println()
	fmt.Println(ui.BoldText("Finding Stale Worktrees"))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("Checking worktrees (PRs merged/closed, or inactive for %d+ days)...\n\n", cleanupDays)

	wts, err := worktree.ListAll(cfg)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	ghClient, clientErr := ghpkg.NewClient(ctx)

	var staleList []staleWorktree
	for _, wt := range wts {
		isStale := false
		reason := ""

		if wt.Type == worktree.TypePRReview && wt.PRNumber > 0 && clientErr == nil {
			fullRepo := cfg.RepoFullName(wt.Repo)
			state, err := ghClient.GetPRState(ctx, fullRepo, wt.PRNumber)
			if err == nil {
				if state == "MERGED" {
					isStale = true
					reason = "PR merged"
				} else if state == "CLOSED" {
					isStale = true
					reason = "PR closed (not merged)"
				}
			}
		}

		if !isStale && wt.Type == worktree.TypeFeature && wt.Branch != "" && clientErr == nil {
			fullRepo := cfg.RepoFullName(wt.Repo)
			state, prNum, err := ghClient.GetPRStateByBranch(ctx, fullRepo, wt.Branch)
			if err == nil {
				if state == "MERGED" {
					isStale = true
					reason = fmt.Sprintf("PR #%d merged", prNum)
				} else if state == "CLOSED" {
					isStale = true
					reason = fmt.Sprintf("PR #%d closed (not merged)", prNum)
				}
			}
		}

		if !isStale {
			age, err := worktree.AgeDays(wt.Path)
			if err == nil && age >= cleanupDays {
				isStale = true
				reason = fmt.Sprintf("No activity for %d days", age)
			}
		}

		if isStale {
			staleList = append(staleList, staleWorktree{Worktree: wt, Reason: reason})
		}
	}

	if jsonFlag {
		printJSON(staleList)
		return nil
	}

	if len(staleList) == 0 {
		fmt.Println("No stale worktrees found.")
		fmt.Println()
		return nil
	}

	home := os.Getenv("HOME")
	for i, s := range staleList {
		fmt.Printf("%s %s\n", ui.YellowText(fmt.Sprintf("%d.", i+1)), s.Name)
		fmt.Printf("   %s\n", ui.DimText("Path: "+ui.ShortenHome(s.Path, home)))
		fmt.Printf("   %s\n", ui.DimText("Reason: "+s.Reason))
		fmt.Println()
	}

	ui.Separator()
	fmt.Printf("Checked: %d worktrees\n", len(wts))
	fmt.Printf("Stale: %s worktrees\n", ui.YellowText(fmt.Sprintf("%d", len(staleList))))
	fmt.Println()

	if !cleanupDelete {
		fmt.Println("To delete these worktrees, run:")
		fmt.Printf("  zen cleanup --days %d --delete\n\n", cleanupDays)
		return nil
	}

	// Interactive deletion
	fmt.Printf("%s\n\n", ui.BoldText(fmt.Sprintf("Delete these %d worktrees?", len(staleList))))
	fmt.Println("  [a] Delete ALL")
	fmt.Println("  [s] Select individually")
	fmt.Println("  [n] Cancel")
	fmt.Println()
	fmt.Print("Choice [a/s/n]: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	choice := strings.TrimSpace(scanner.Text())

	switch strings.ToLower(choice) {
	case "a":
		fmt.Println()
		fmt.Println(ui.BoldText("Deleting all stale worktrees..."))
		fmt.Println()
		deleted, failed := 0, 0
		for _, s := range staleList {
			fmt.Printf("  %s\n", ui.CyanText(s.Name))
			if deleteWorktree(s) {
				deleted++
			} else {
				failed++
			}
		}
		fmt.Println()
		ui.Separator()
		fmt.Printf("Deleted: %s  Failed: %s\n", ui.GreenText(fmt.Sprintf("%d", deleted)), ui.RedText(fmt.Sprintf("%d", failed)))

	case "s":
		fmt.Println()
		deleted, skippedCount := 0, 0
		for _, s := range staleList {
			fmt.Printf("%s - %s\n", ui.CyanText(s.Name), s.Reason)
			fmt.Print("  Delete? [y/N]: ")
			scanner.Scan()
			resp := strings.TrimSpace(scanner.Text())
			if strings.ToLower(resp) == "y" {
				if deleteWorktree(s) {
					deleted++
				}
			} else {
				skippedCount++
				fmt.Println("    Skipped")
			}
			fmt.Println()
		}
		ui.Separator()
		fmt.Printf("Deleted: %s  Skipped: %s\n", ui.GreenText(fmt.Sprintf("%d", deleted)), ui.DimText(fmt.Sprintf("%d", skippedCount)))

	default:
		fmt.Println("Cancelled.")
	}

	return nil
}

func deleteWorktree(s staleWorktree) bool {
	basePath := cfg.RepoBasePath(s.Repo)
	originPath := filepath.Join(basePath, s.Repo)

	if _, err := os.Stat(filepath.Join(originPath, ".git")); os.IsNotExist(err) {
		fmt.Printf("    %s\n", ui.RedText("Cannot find git repo for worktree"))
		return false
	}

	removeCmd := exec.Command("git", "worktree", "remove", s.Path, "--force")
	removeCmd.Dir = originPath
	if err := removeCmd.Run(); err != nil {
		fmt.Printf("    %s\n", ui.RedText("✗ Failed to remove"))
		return false
	}

	fmt.Printf("    %s\n", ui.GreenText("✓ Removed worktree"))
	return true
}
