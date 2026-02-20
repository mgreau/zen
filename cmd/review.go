package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
	reviewCmd.Flags().StringVar(&reviewRepo, "repo", "", "Repository short name from config (auto-detected if omitted)")
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

	ctx := context.Background()

	// Auto-detect repo if not specified
	if reviewRepo == "" {
		detected, err := detectRepoForPR(ctx, prNumber)
		if err != nil {
			return err
		}
		reviewRepo = detected
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

	// If worktree already exists, resume it
	if _, err := os.Stat(worktreePath); err == nil {
		ui.LogInfo(fmt.Sprintf("Worktree already exists, resuming PR #%d...", prNumber))
		return openReviewTab(worktreePath, worktreeName)
	}

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

// openReviewTab resumes an existing worktree in a new iTerm tab.
func openReviewTab(worktreePath, worktreeName string) error {
	w := wt.Worktree{
		Path:   worktreePath,
		Name:   worktreeName,
		Type:   wt.TypePRReview,
	}
	return resumeWorktree(w, fmt.Sprintf("zen review resume %s", worktreeName))
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
