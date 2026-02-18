package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	ctxpkg "github.com/mgreau/zen/internal/context"
	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/iterm"
	"github.com/mgreau/zen/internal/prcache"
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
	reviewDeleteForce bool
)

func init() {
	reviewCmd.Flags().StringVar(&reviewRepo, "repo", "mono", "Repository short name from config")
	reviewCmd.Flags().BoolVar(&reviewNoITerm, "no-iterm", false, "Create worktree only, don't open iTerm2 tab")
	addResumeFlags(reviewResumeCmd)
	reviewDeleteCmd.Flags().BoolVarP(&reviewDeleteForce, "force", "f", false, "Skip confirmation")
	reviewCmd.AddCommand(reviewResumeCmd)
	reviewCmd.AddCommand(reviewDeleteCmd)
	rootCmd.AddCommand(reviewCmd)
}

// ReviewResult holds the output for --json mode.
type ReviewResult struct {
	WorktreePath string `json:"worktree_path"`
	PRNumber     int    `json:"pr_number"`
	Title        string `json:"title"`
	Author       string `json:"author"`
}

func runReview(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}
	prNumber, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", args[0], err)
	}

	// Validate repo exists in config
	basePath := cfg.RepoBasePath(reviewRepo)
	if basePath == "" {
		return fmt.Errorf("unknown repo %q — check ~/.zen/config.yaml", reviewRepo)
	}
	fullRepo := cfg.RepoFullName(reviewRepo)

	// Construct paths
	originPath := filepath.Join(basePath, reviewRepo)
	worktreeName := fmt.Sprintf("%s-pr-%d", reviewRepo, prNumber)
	worktreePath := filepath.Join(basePath, worktreeName)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s\n  Resume with: zen review resume %d", worktreePath, prNumber)
	}

	ctx := context.Background()

	// Fetch PR details from GitHub
	ui.LogInfo(fmt.Sprintf("Fetching PR #%d from %s...", prNumber, fullRepo))
	client, err := github.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("creating GitHub client: %w", err)
	}
	details, err := client.GetPRDetails(ctx, fullRepo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching PR details: %w", err)
	}

	ui.LogInfo(fmt.Sprintf("PR #%d: %s (by %s)", prNumber, details.Title, details.Author))

	// Create worktree under lock
	branchName := fmt.Sprintf("pr-%d", prNumber)

	wt.GitMu.Lock()

	ui.LogInfo(fmt.Sprintf("Fetching pull/%d/head...", prNumber))
	fetchCmd := exec.Command("git", "fetch", "origin", fmt.Sprintf("+pull/%d/head:%s", prNumber, branchName))
	fetchCmd.Dir = originPath
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		wt.GitMu.Unlock()
		return fmt.Errorf("git fetch: %w: %s", err, string(out))
	}

	ui.LogInfo(fmt.Sprintf("Creating worktree %s...", worktreeName))
	wtCmd := exec.Command("git", "worktree", "add", worktreePath, branchName)
	wtCmd.Dir = originPath
	if out, err := wtCmd.CombinedOutput(); err != nil {
		wt.GitMu.Unlock()
		return fmt.Errorf("git worktree add: %w: %s", err, string(out))
	}

	// Clean stale index.lock
	lockFile := filepath.Join(originPath, ".git", "worktrees", worktreeName, "index.lock")
	os.Remove(lockFile)

	wt.GitMu.Unlock()

	// Inject PR context into CLAUDE.local.md
	ui.LogInfo("Injecting PR context into CLAUDE.local.md...")
	if err := ctxpkg.InjectPRContext(ctx, worktreePath, fullRepo, prNumber); err != nil {
		ui.LogInfo(fmt.Sprintf("Warning: failed to inject context: %v", err))
	}

	// Cache PR metadata
	prcache.Set(reviewRepo, prNumber, details.Title, details.Author)

	home := homeDir()
	shortPath := ui.ShortenHome(worktreePath, home)

	if jsonFlag {
		printJSON(ReviewResult{
			WorktreePath: worktreePath,
			PRNumber:     prNumber,
			Title:        details.Title,
			Author:       details.Author,
		})
		return nil
	}

	fmt.Println()
	ui.LogSuccess(fmt.Sprintf("Created worktree: %s", shortPath))
	fmt.Printf("  PR:     #%d — %s\n", prNumber, details.Title)
	fmt.Printf("  Author: %s\n", details.Author)

	if reviewNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Open manually:"))
		fmt.Printf("  cd %s && %s \"/review-pr\"\n", worktreePath, cfg.ClaudeBin)
		return nil
	}

	// Open iTerm tab
	if err := iterm.OpenTabWithClaude(worktreePath, "/review-pr", cfg.ClaudeBin); err != nil {
		return fmt.Errorf("opening iTerm tab: %w", err)
	}

	ui.LogSuccess("iTerm2 tab opened")
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
