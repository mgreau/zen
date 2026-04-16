// Package review provides shared logic for creating PR review worktrees.
// Both the CLI commands and the MCP server use this package.
package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ctxpkg "github.com/mgreau/zen/internal/context"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/prcache"
	wt "github.com/mgreau/zen/internal/worktree"
)

// gitTimeout is the maximum time allowed for a single git subprocess.
const gitTimeout = 2 * time.Minute

// Result holds the output of a successful worktree creation.
type Result struct {
	WorktreePath string `json:"worktree_path"`
	PRNumber     int    `json:"pr_number"`
	Title        string `json:"title"`
	Author       string `json:"author"`
}

// Logger is called for progress messages. CLI callers pass ui.LogInfo;
// MCP callers pass nil or a no-op to avoid stdout pollution.
type Logger func(msg string)

func noop(string) {}

// CreateWorktree creates a PR review worktree. It fetches the PR branch,
// creates the git worktree, injects CLAUDE.local.md context, and caches
// PR metadata. Returns the result or an error.
//
// If the worktree already exists, returns a Result with the existing path.
// The caller is responsible for detecting the repo if repoShort is empty.
func CreateWorktree(ctx context.Context, cfg *config.Config, repoShort string, prNumber int, log Logger) (*Result, error) {
	if log == nil {
		log = noop
	}

	basePath := cfg.RepoBasePath(repoShort)
	if basePath == "" {
		return nil, fmt.Errorf("unknown repo %q -- check ~/.zen/config.yaml", repoShort)
	}
	fullRepo := cfg.RepoFullName(repoShort)

	originPath := filepath.Join(basePath, repoShort)
	worktreeName := fmt.Sprintf("%s-pr-%d", repoShort, prNumber)
	worktreePath := filepath.Join(basePath, worktreeName)

	// If worktree already exists, return it
	if _, err := os.Stat(worktreePath); err == nil {
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
	log(fmt.Sprintf("Fetching PR #%d from %s...", prNumber, fullRepo))
	client, err := github.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating GitHub client: %w", err)
	}
	details, err := client.GetPRDetails(ctx, fullRepo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("fetching PR details: %w", err)
	}

	log(fmt.Sprintf("PR #%d: %s (by %s)", prNumber, details.Title, details.Author))

	// Create worktree under lock
	branchName := fmt.Sprintf("pr-%d", prNumber)

	wt.GitMu.Lock()

	log(fmt.Sprintf("Fetching pull/%d/head...", prNumber))
	gitCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	fetchCmd := exec.CommandContext(gitCtx, "git", "fetch", "origin", fmt.Sprintf("+pull/%d/head:%s", prNumber, branchName))
	fetchCmd.Dir = originPath
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		cancel()
		wt.GitMu.Unlock()
		if gitCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("git fetch timed out after %s", gitTimeout)
		}
		return nil, fmt.Errorf("git fetch: %w: %s", err, string(out))
	}
	cancel()

	log(fmt.Sprintf("Creating worktree %s...", worktreeName))
	gitCtx, cancel = context.WithTimeout(ctx, gitTimeout)
	wtCmd := exec.CommandContext(gitCtx, "git", "worktree", "add", worktreePath, branchName)
	wtCmd.Dir = originPath
	if out, err := wtCmd.CombinedOutput(); err != nil {
		cancel()
		wt.GitMu.Unlock()
		if gitCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("git worktree add timed out after %s", gitTimeout)
		}
		return nil, fmt.Errorf("git worktree add: %w: %s", err, string(out))
	}
	cancel()

	// Clean stale index.lock (only if holding process is dead)
	lockFile := filepath.Join(originPath, ".git", "worktrees", worktreeName, "index.lock")
	wt.RemoveStaleLock(lockFile, worktreeName)

	wt.GitMu.Unlock()

	// Inject PR context into CLAUDE.local.md
	log("Injecting PR context into CLAUDE.local.md...")
	if err := ctxpkg.InjectPRContext(ctx, worktreePath, fullRepo, prNumber); err != nil {
		log(fmt.Sprintf("Warning: failed to inject context: %v", err))
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
// Unlike the CLI version, this does not prompt interactively -- it returns
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

	var matches []string
	for _, repo := range repos {
		fullRepo := cfg.RepoFullName(repo)
		if _, err := client.GetPRDetails(ctx, fullRepo, prNumber); err == nil {
			matches = append(matches, repo)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("PR #%d not found in any configured repo", prNumber)
	case 1:
		return matches[0], nil
	default:
		// Try reviewer heuristic
		currentUser, _ := github.GetCurrentUser(ctx)
		if currentUser != "" {
			var reviewMatches []string
			for _, repo := range matches {
				fullRepo := cfg.RepoFullName(repo)
				if ok, _ := client.IsRequestedReviewer(ctx, fullRepo, prNumber, currentUser); ok {
					reviewMatches = append(reviewMatches, repo)
				}
			}
			if len(reviewMatches) == 1 {
				return reviewMatches[0], nil
			}
		}
		return "", fmt.Errorf("PR #%d exists in multiple repos (%s) -- specify with repo parameter",
			prNumber, strings.Join(matches, ", "))
	}
}
