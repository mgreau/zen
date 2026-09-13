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

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	ctxpkg "github.com/mgreau/zen/internal/context"
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
// If the worktree already exists, it fetches pull/N/head and fast-forwards
// unless the tree is dirty or an agent is running, then returns the existing
// path. confirmReset is required to git reset --hard on a rewritten GitHub
// head; nil never resets (daemon/MCP). The caller is responsible for detecting
// the repo if repoShort is empty.
func CreateWorktree(ctx context.Context, cfg *config.Config, ag agent.Agent, repoShort string, prNumber int, log Logger, confirmReset ConfirmReset) (*Result, error) {
	if log == nil {
		log = noop
	}

	basePath := cfg.RepoBasePath(repoShort)
	if basePath == "" {
		return nil, fmt.Errorf("unknown repo %q -- check ~/.zen/config.yaml", repoShort)
	}
	fullRepo := cfg.RepoFullName(repoShort)

	originPath := filepath.Join(basePath, repoShort)
	worktreeName := wt.PRName(repoShort, prNumber)
	worktreePath := filepath.Join(basePath, worktreeName)

	if _, err := os.Stat(worktreePath); err == nil {
		return refreshExisting(ctx, ag, repoShort, fullRepo, originPath, worktreePath, prNumber, log, confirmReset)
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
	// Use --no-checkout + separate checkout to avoid "Could not write new index file"
	// on large repos (13K+ files). The two-step approach handles the index write reliably.
	wtCmd := exec.CommandContext(gitCtx, "git", "worktree", "add", "--no-checkout", worktreePath, branchName)
	wtCmd.Dir = originPath
	if out, err := wtCmd.CombinedOutput(); err != nil {
		cancel()
		wt.CleanupFailedAdd(originPath, worktreePath, branchName)
		wt.GitMu.Unlock()
		if gitCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("git worktree add timed out after %s", gitTimeout)
		}
		return nil, fmt.Errorf("git worktree add: %w: %s", err, string(out))
	}
	cancel()

	gitCtx, cancel = context.WithTimeout(ctx, gitTimeout)
	checkoutCmd := exec.CommandContext(gitCtx, "git", "checkout")
	checkoutCmd.Dir = worktreePath
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		cancel()
		wt.CleanupFailedAdd(originPath, worktreePath, branchName)
		wt.GitMu.Unlock()
		if gitCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("git checkout in worktree timed out after %s", gitTimeout)
		}
		return nil, fmt.Errorf("git checkout in worktree: %w: %s", err, string(out))
	}
	cancel()

	// Clean stale index.lock (only if holding process is dead)
	lockFile := filepath.Join(originPath, ".git", "worktrees", worktreeName, "index.lock")
	wt.RemoveStaleLock(lockFile, worktreeName)

	wt.GitMu.Unlock()

	injectContext(ctx, ag, worktreePath, fullRepo, prNumber, log)
	prcache.Set(repoShort, prNumber, details.Title, details.Author)

	return &Result{
		WorktreePath: worktreePath,
		PRNumber:     prNumber,
		Title:        details.Title,
		Author:       details.Author,
	}, nil
}

func refreshExisting(ctx context.Context, ag agent.Agent, repoShort, fullRepo, originPath, worktreePath string, prNumber int, log Logger, confirmReset ConfirmReset) (*Result, error) {
	title, author := "", ""
	if meta, ok := prcache.Get(repoShort, prNumber); ok {
		title = meta.Title
		author = meta.Author
	}

	wantSHA := ""
	client, err := github.NewClient(ctx)
	if err != nil {
		log(fmt.Sprintf("Warning: GitHub client: %v", err))
	} else if details, derr := client.GetPRDetails(ctx, fullRepo, prNumber); derr != nil {
		log(fmt.Sprintf("Warning: fetching PR details: %v", derr))
	} else {
		wantSHA = details.HeadSHA
		title = details.Title
		author = details.Author
		prcache.Set(repoShort, prNumber, details.Title, details.Author)
	}

	log(fmt.Sprintf("Refreshing PR #%d worktree...", prNumber))
	outcome, err := SyncExisting(ctx, originPath, worktreePath, prNumber, wantSHA, log, confirmReset)
	if err != nil {
		log(fmt.Sprintf("Warning: failed to refresh PR #%d: %v", prNumber, err))
	} else if outcome == SyncUpdated {
		injectContext(ctx, ag, worktreePath, fullRepo, prNumber, log)
	} else if outcome == SyncSkippedReset {
		log(fmt.Sprintf("PR #%d cannot be fast-forwarded onto GitHub's head; worktree left as-is", prNumber))
	}

	return &Result{
		WorktreePath: worktreePath,
		PRNumber:     prNumber,
		Title:        title,
		Author:       author,
	}, nil
}

func injectContext(ctx context.Context, ag agent.Agent, worktreePath, fullRepo string, prNumber int, log Logger) {
	log(fmt.Sprintf("Injecting PR context into %s...", ag.ContextFile()))
	if rendered, rerr := ctxpkg.RenderPRContext(ctx, fullRepo, prNumber); rerr != nil {
		log(fmt.Sprintf("Warning: failed to render context: %v", rerr))
	} else if ref, ierr := ag.InjectContext(worktreePath, rendered); ierr != nil {
		log(fmt.Sprintf("Warning: failed to inject context: %v", ierr))
	} else {
		log(fmt.Sprintf("Wrote PR context to %s", ref))
	}
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
