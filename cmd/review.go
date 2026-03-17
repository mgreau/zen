package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/review"
	"github.com/mgreau/zen/internal/terminal"
	"github.com/mgreau/zen/internal/ui"
	wt "github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review [pr-number]",
	Short: "Create or resume a PR review worktree",
	Long: `Manage PR review worktrees.

Usage:
  zen review <pr-number>           Create worktree + open iTerm tab
  zen review resume <pr-number>    Resume existing session in new tab
  zen review delete <pr-number>    Delete a PR review worktree`,
	DisableFlagParsing: false,
	RunE:               runReview,
}

var reviewResumeCmd = &cobra.Command{
	Use:   "resume <pr-number>",
	Short: "Resume a PR review session in a new iTerm2 tab",
	Args:  cobra.ExactArgs(1),
	RunE:  runReviewResume,
}

var reviewDeleteCmd = &cobra.Command{
	Use:   "delete <pr-number>",
	Short: "Delete a PR review worktree",
	Args:  cobra.ExactArgs(1),
	RunE:  runReviewDelete,
}

var (
	reviewRepo        string
	reviewNoITerm     bool
	reviewModel       string
	reviewDeleteForce bool
)

func init() {
	reviewCmd.Flags().StringVar(&reviewRepo, "repo", "", "Repository short name from config (auto-detected if omitted)")
	reviewCmd.Flags().BoolVar(&reviewNoITerm, "no-terminal", false, "Create worktree only, don't open terminal tab")
	reviewCmd.Flags().StringVarP(&reviewModel, "model", "m", "", "Claude model to use (e.g., sonnet, opus, haiku)")
	addResumeFlags(reviewResumeCmd)
	reviewDeleteCmd.Flags().BoolVarP(&reviewDeleteForce, "force", "f", false, "Skip confirmation")
	reviewCmd.AddCommand(reviewResumeCmd)
	reviewCmd.AddCommand(reviewDeleteCmd)
	rootCmd.AddCommand(reviewCmd)
}

func runReview(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}
	prNumber, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", args[0], err)
	}

	ctx := context.Background()

	// Auto-detect repo if not specified
	if reviewRepo == "" {
		detected, err := detectRepoForPR(ctx, prNumber)
		if err != nil {
			return err
		}
		reviewRepo = detected
	}

	// Check if worktree already exists and resume
	basePath := cfg.RepoBasePath(reviewRepo)
	if basePath != "" {
		worktreeName := fmt.Sprintf("%s-pr-%d", reviewRepo, prNumber)
		worktreePath := filepath.Join(basePath, worktreeName)
		if _, err := os.Stat(worktreePath); err == nil {
			ui.LogInfo(fmt.Sprintf("Worktree already exists, resuming PR #%d...", prNumber))
			if reviewModel != "" {
				resumeModel = reviewModel
			}
			return openReviewTab(worktreePath, worktreeName)
		}
	}

	// Create worktree using shared logic
	result, err := review.CreateWorktree(ctx, cfg, reviewRepo, prNumber)
	if err != nil {
		return err
	}

	home := homeDir()
	shortPath := ui.ShortenHome(result.WorktreePath, home)

	if jsonFlag {
		printJSON(result)
		return nil
	}

	fmt.Println()
	ui.LogSuccess(fmt.Sprintf("Created worktree: %s", shortPath))
	fmt.Printf("  PR:     #%d — %s\n", result.PRNumber, result.Title)
	fmt.Printf("  Author: %s\n", result.Author)

	if reviewModel != "" {
		fmt.Printf("  Model:  %s\n", ui.CyanText(reviewModel))
	}

	// Ensure /review-pr command is installed
	if err := ensureClaudeCommand("review-pr"); err != nil {
		ui.LogInfo(fmt.Sprintf("Warning: could not install /review-pr command: %v", err))
	}

	if reviewNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Open manually:"))
		modelFlag := ""
		if reviewModel != "" {
			modelFlag = fmt.Sprintf(" --model %s", reviewModel)
		}
		fmt.Printf("  cd %s && %s%s \"/review-pr\"\n", result.WorktreePath, cfg.ClaudeBin, modelFlag)
		return nil
	}

	// Open terminal tab
	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}

	if err := term.OpenTabWithClaude(result.WorktreePath, "/review-pr", cfg.ClaudeBin, reviewModel); err != nil {
		return fmt.Errorf("opening %s tab: %w", term.Name(), err)
	}

	ui.LogSuccess(fmt.Sprintf("%s tab opened", term.Name()))
	fmt.Println()
	return nil
}

func runReviewDelete(cmd *cobra.Command, args []string) error {
	prNumber, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", args[0], err)
	}

	match, err := findWorktreeByPR(prNumber)
	if err != nil {
		return err
	}

	home := homeDir()
	shortPath := ui.ShortenHome(match.Path, home)

	if !reviewDeleteForce {
		fmt.Printf("Delete worktree %s?\n", ui.CyanText(match.Name))
		fmt.Printf("  Path: %s\n", shortPath)
		fmt.Print("  Confirm [y/N]: ")

		var resp string
		fmt.Scanln(&resp)
		if resp != "y" && resp != "Y" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	basePath := cfg.RepoBasePath(match.Repo)
	originPath := filepath.Join(basePath, match.Repo)

	removeCmd := exec.Command("git", "worktree", "remove", match.Path, "--force")
	removeCmd.Dir = originPath
	if out, err := removeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, string(out))
	}

	ui.LogSuccess(fmt.Sprintf("Deleted worktree: %s", shortPath))
	return nil
}

// openReviewTab resumes an existing worktree in a new iTerm tab.
func openReviewTab(worktreePath, worktreeName string) error {
	w := wt.Worktree{
		Path:   worktreePath,
		Name:   worktreeName,
		Type:   wt.TypePRReview,
	}
	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}
	return resumeWorktree(w, fmt.Sprintf("zen review resume %s", worktreeName), term)
}

// detectRepoForPR tries each configured repo to find which one contains the
// given PR number. If multiple repos have the same PR number, asks the user
// to choose. Returns the repo short name or an error.
func detectRepoForPR(ctx context.Context, prNumber int) (string, error) {
	repos := cfg.RepoNames()
	if len(repos) == 1 {
		return repos[0], nil
	}

	ui.LogInfo(fmt.Sprintf("Detecting repo for PR #%d...", prNumber))

	client, err := github.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("creating GitHub client: %w", err)
	}

	type match struct {
		repo   string
		title  string
		author string
	}
	var matches []match
	for _, repo := range repos {
		fullRepo := cfg.RepoFullName(repo)
		details, err := client.GetPRDetails(ctx, fullRepo, prNumber)
		if err == nil {
			matches = append(matches, match{repo: repo, title: details.Title, author: details.Author})
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("PR #%d not found in any configured repo (%s)\n  Specify with: zen review --repo <name> %d",
			prNumber, strings.Join(repos, ", "), prNumber)
	case 1:
		ui.LogInfo(fmt.Sprintf("Found PR #%d in %s", prNumber, matches[0].repo))
		return matches[0].repo, nil
	default:
		// Check if the user is a requested reviewer on exactly one of them.
		currentUser, _ := github.GetCurrentUser(ctx)
		if currentUser != "" {
			var reviewMatches []match
			for _, m := range matches {
				fullRepo := cfg.RepoFullName(m.repo)
				if ok, _ := client.IsRequestedReviewer(ctx, fullRepo, prNumber, currentUser); ok {
					reviewMatches = append(reviewMatches, m)
				}
			}
			if len(reviewMatches) == 1 {
				ui.LogInfo(fmt.Sprintf("Found PR #%d in %s (you're a requested reviewer)", prNumber, reviewMatches[0].repo))
				return reviewMatches[0].repo, nil
			}
		}

		// Multiple matches, ask the user.
		fmt.Printf("PR #%d exists in multiple repos:\n", prNumber)
		for i, m := range matches {
			fmt.Printf("  [%d] %s — %s (by %s)\n", i+1, m.repo, ui.Truncate(m.title, 50), m.author)
		}
		fmt.Print("Which repo? [1]: ")
		var resp string
		fmt.Scanln(&resp)
		resp = strings.TrimSpace(resp)
		if resp == "" {
			resp = "1"
		}
		idx, err := strconv.Atoi(resp)
		if err != nil || idx < 1 || idx > len(matches) {
			return "", fmt.Errorf("invalid choice %q", resp)
		}
		return matches[idx-1].repo, nil
	}
}
