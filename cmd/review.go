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
	reviewCmd.Flags().StringVarP(&reviewModel, "model", "m", "", "Model to use (agent-specific, e.g. opus or gpt-5-codex)")
	addAgentFlag(reviewCmd)
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

	// Check if worktree already exists: refresh then resume the session.
	basePath := cfg.RepoBasePath(reviewRepo)
	if basePath != "" {
		worktreeName := wt.PRName(reviewRepo, prNumber)
		worktreePath := filepath.Join(basePath, worktreeName)
		if _, err := os.Stat(worktreePath); err == nil {
			ag, aerr := resolveAgent()
			if aerr != nil {
				return aerr
			}
			result, rerr := review.CreateWorktree(ctx, cfg, ag, reviewRepo, prNumber, ui.LogInfo, confirmResetWorktree)
			if rerr != nil {
				ui.LogWarn(fmt.Sprintf("refresh before resume: %v", rerr))
			}
			if jsonFlag && result != nil {
				printJSON(result)
				return nil
			}
			if reviewNoITerm {
				return nil
			}
			ui.LogInfo(fmt.Sprintf("Worktree already exists, resuming PR #%d...", prNumber))
			if reviewModel != "" {
				resumeModel = reviewModel
			}
			return openReviewTab(worktreePath, worktreeName)
		}
	}

	ag, err := resolveAgent()
	if err != nil {
		return err
	}

	// Create worktree using shared logic
	result, err := review.CreateWorktree(ctx, cfg, ag, reviewRepo, prNumber, ui.LogInfo, confirmResetWorktree)
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

	// Ensure the /review-pr prompt is installed for the chosen agent
	ensureReviewPrompt(ag)

	launchCmd := ag.StartCommand(ag.ReviewPrompt(result.WorktreePath), reviewModel)

	if reviewNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Open manually:"))
		fmt.Printf("  cd %s && %s\n", result.WorktreePath, launchCmd)
		return nil
	}

	// Open terminal tab
	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}

	if err := term.OpenTab(result.WorktreePath, launchCmd); err != nil {
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
		if !ui.Interactive() {
			return fmt.Errorf("deleting %s needs a confirmation; run this from a terminal or pass --force", match.Name)
		}
		fmt.Printf("Delete worktree %s?\n", ui.CyanText(match.Name))
		fmt.Printf("  Path: %s\n", shortPath)
		if !ui.ConfirmYN("  Confirm [y/N]: ") {
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
		Path: worktreePath,
		Name: worktreeName,
		Type: wt.TypePRReview,
	}
	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}
	return resumeWorktree(w, fmt.Sprintf("zen review resume %s", worktreeName), term)
}

// confirmResetWorktree asks before git reset --hard onto a GitHub head the
// worktree cannot fast-forward onto. Default is no. --json never resets, and
// neither does a run without a terminal: reading a pipe here would let
// `yes | zen review N` discard commits nobody agreed to discard.
func confirmResetWorktree(req review.ResetRequest) bool {
	if jsonFlag {
		return false
	}
	if !ui.Interactive() {
		ui.LogInfo(fmt.Sprintf("PR #%d needs a reset to match GitHub; run zen review %d from a terminal to confirm it.",
			req.PRNumber, req.PRNumber))
		return false
	}
	if req.Kind == review.ResetBehind {
		fmt.Printf("PR #%d's GitHub head is behind this worktree (force-pushed backward, or committed to locally).\n", req.PRNumber)
	} else {
		fmt.Printf("PR #%d was rewritten on GitHub (force-push).\n", req.PRNumber)
	}
	fmt.Println("  Reset this worktree onto the GitHub head? Untracked files (CLAUDE.local.md, .zen/) are kept.")
	if req.UniqueCommits > 0 {
		fmt.Printf("  %d local commit(s) on pr-%d will leave the branch (reflog keeps them).\n", req.UniqueCommits, req.PRNumber)
	}
	return ui.ConfirmYN("  Reset? [y/N]: ")
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
