// Package review provides shared logic for creating PR review worktrees.
// Both the CLI commands and the MCP server use this package.
package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	ctxpkg "github.com/mgreau/zen/internal/context"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/prcache"
	"github.com/mgreau/zen/internal/ui"
	wt "github.com/mgreau/zen/internal/worktree"
)

// Result holds the output of a successful worktree creation.
type Result struct {
	WorktreePath string `json:"worktree_path"`
	PRNumber     int    `json:"pr_number"`
	Title        string `json:"title"`
	Author       string `json:"author"`
}

// CreateWorktree creates a PR review worktree. It fetches the PR branch,
// creates the git worktree, injects CLAUDE.local.md context, and caches
// PR metadata. Returns the result or an error.
//
// If the worktree already exists, returns a Result with the existing path.
// The caller is responsible for detecting the repo if repoShort is empty.
func CreateWorktree(ctx context.Context, cfg *config.Config, repoShort string, prNumber int) (*Result, error) {
	basePath := cfg.RepoBasePath(repoShort)
	if basePath == "" {
		return nil, fmt.Errorf("unknown repo %q — check ~/.zen/config.yaml", repoShort)
	}
	fullRepo := cfg.RepoFullName(repoShort)

	originPath := filepath.Join(basePath, repoShort)
	worktreeName := fmt.Sprintf("%s-pr-%d", repoShort, prNumber)
	worktreePath := filepath.Join(basePath, worktreeName)

	// If worktree already exists, return it
	if _, err := os.Stat(worktreePath); err == nil {
		// Try to get cached metadata
		meta, ok := prcache.Get(repoShort, prNumber)
		title, author := "", ""
		if ok {
			title = meta.Title
			author = meta.Author
		}
		return &Result{
			WorktreePath: worktreePath,
			PRNumber:     prNumber,
			Title:        title,
			Author:       author,
		}, nil
	}

	// Fetch PR details from GitHub
	ui.LogInfo(fmt.Sprintf("Fetching PR #%d from %s...", prNumber, fullRepo))
	client, err := github.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating GitHub client: %w", err)
	}
	details, err := client.GetPRDetails(ctx, fullRepo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("fetching PR details: %w", err)
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
		return nil, fmt.Errorf("git fetch: %w: %s", err, string(out))
	}

	ui.LogInfo(fmt.Sprintf("Creating worktree %s...", worktreeName))
	wtCmd := exec.Command("git", "worktree", "add", worktreePath, branchName)
	wtCmd.Dir = originPath
	if out, err := wtCmd.CombinedOutput(); err != nil {
		wt.GitMu.Unlock()
		return nil, fmt.Errorf("git worktree add: %w: %s", err, string(out))
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
	prcache.Set(repoShort, prNumber, details.Title, details.Author)

	return &Result{
		WorktreePath: worktreePath,
		PRNumber:     prNumber,
		Title:        details.Title,
		Author:       details.Author,
	}, nil
}

// DetectRepo tries each configured repo to find which one contains the
// given PR number. Returns the repo short name or an error.
// Unlike the CLI version, this does not prompt interactively — it returns
// an error if ambiguous.
func DetectRepo(ctx context.Context, cfg *config.Config, prNumber int) (string, error) {
	repos := cfg.RepoNames()
	if len(repos) == 1 {
		return repos[0], nil
	}

	client, err := github.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("creating GitHub client: %w", err)
	}

	type match struct {
		repo string
	}
	var matches []match
	for _, repo := range repos {
		fullRepo := cfg.RepoFullName(repo)
		if _, err := client.GetPRDetails(ctx, fullRepo, prNumber); err == nil {
			matches = append(matches, match{repo: repo})
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("PR #%d not found in any configured repo", prNumber)
	case 1:
		return matches[0].repo, nil
	default:
		// Try reviewer heuristic
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
				return reviewMatches[0].repo, nil
			}
		}
		var names []string
		for _, m := range matches {
			names = append(names, m.repo)
		}
		return "", fmt.Errorf("PR #%d exists in multiple repos (%s) — specify with repo parameter",
			prNumber, fmt.Sprintf("%v", names))
	}
}
