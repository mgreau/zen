package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/gitignore"
	"github.com/mgreau/zen/internal/ui"
)

// Logger receives progress messages during a create. A nil Logger is valid
// and discards them, which is what the daemon and MCP callers want.
type Logger func(msg string)

func (l Logger) logf(format string, args ...any) {
	if l != nil {
		l(fmt.Sprintf(format, args...))
	}
}

// runGit runs one git command in dir. A non-zero timeout bounds that single
// command, so a hung fetch on a large repo fails with a clear deadline error
// rather than blocking GitMu indefinitely. what names the step in errors.
func runGit(ctx context.Context, dir string, timeout time.Duration, what string, args ...string) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s timed out after %s", what, timeout)
	}
	return fmt.Errorf("%s: %w: %s", what, err, strings.TrimSpace(string(out)))
}

// EnsureNestedExcluded keeps a worktree created *inside* the clone out of
// `git status` in the main checkout, by adding its top-level directory to
// the clone's info/exclude.
//
// Derived from the paths rather than from config so it stays correct
// whatever decided the layout, and a no-op for a worktree beside the clone.
// The committed .gitignore is never touched: zen manages repos the user may
// not own, and excluding a zen-owned path there would mean a commit to
// someone else's project.
func EnsureNestedExcluded(originPath, worktreePath string) {
	rel, err := filepath.Rel(originPath, worktreePath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return // beside the clone: nothing inside the repo to hide
	}
	top := strings.Split(rel, string(filepath.Separator))[0] + "/"
	switch gitignore.EnsureExcluded(originPath, top) {
	case gitignore.Written:
		// Fires at most once per repo: subsequent creates see it ignored.
		// Point at the user-level file, which covers every repo at once and
		// leaves zen writing into none of them.
		ui.LogInfo(fmt.Sprintf(
			"Excluded %s in %s. To keep zen out of each repo, add %s to your user-level gitignore instead: echo '%s' >> ~/.config/git/ignore",
			top, originPath, top, top))
	case gitignore.Failed:
		ui.LogWarn(fmt.Sprintf(
			"%s is inside %s but git does not ignore it -- add %s to your user-level gitignore (~/.config/git/ignore), or it will show as untracked",
			top, originPath, top))
	}
}

// CreateFromMain fetches origin/main and creates a new worktree checked out
// to a new branch cut from it. Used for feature work (branch didn't exist
// before), as opposed to PR review worktrees which check out an existing
// remote branch/ref instead.
//
// Uses --no-checkout + a separate checkout to avoid "Could not write new
// index file" on large repos (13K+ files) — the two-step approach handles
// the index write reliably. Serializes on GitMu to prevent concurrent
// index.lock conflicts across worktree operations on the same origin repo.
func CreateFromMain(originPath, worktreePath, worktreeName, branch string) error {
	GitMu.Lock()
	defer GitMu.Unlock()

	fetchCmd := exec.Command("git", "fetch", "origin", "main")
	fetchCmd.Dir = originPath
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %w: %s", err, string(out))
	}

	wtCmd := exec.Command("git", "worktree", "add", "--no-checkout", worktreePath, "-b", branch, "origin/main")
	wtCmd.Dir = originPath
	if out, err := wtCmd.CombinedOutput(); err != nil {
		CleanupFailedAdd(originPath, worktreePath, branch)
		return fmt.Errorf("git worktree add: %w: %s", err, string(out))
	}

	EnsureNestedExcluded(originPath, worktreePath)

	checkoutCmd := exec.Command("git", "checkout")
	checkoutCmd.Dir = worktreePath
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		CleanupFailedAdd(originPath, worktreePath, branch)
		return fmt.Errorf("git checkout in worktree: %w: %s", err, string(out))
	}

	// Clean stale index.lock (only if holding process is dead)
	lockFile := filepath.Join(originPath, ".git", "worktrees", worktreeName, "index.lock")
	RemoveStaleLock(lockFile, worktreeName)

	return nil
}

// CreateFromPR fetches the PR's remote branch (pull/N/head) into a local
// pr-N branch and creates a worktree checked out to it. Used for PR review
// worktrees, which check out an existing remote ref, as opposed to
// CreateFromMain which cuts a brand new branch for feature work.
//
// This is the single creation path for review worktrees -- the CLI, the MCP
// server, and the daemon all route through it. Keeping it single matters
// beyond deduplication: worktree creation now has to exclude a nested
// worktree from git status, and a second inline copy of these git calls
// would silently skip that step.
//
// timeout bounds each individual git command; zero means no deadline beyond
// ctx. log may be nil.
//
// If worktreePath already exists by the time the lock is acquired (e.g. a
// concurrent caller created it first), this is a no-op -- callers that need
// a fast pre-lock existence check too (to skip the wait entirely in the
// common case) can still do their own os.Stat before calling.
//
// Uses --no-checkout + a separate checkout to avoid "Could not write new
// index file" on large repos (13K+ files) -- the two-step approach handles
// the index write reliably. Serializes on GitMu to prevent concurrent
// index.lock conflicts across worktree operations on the same origin repo.
func CreateFromPR(ctx context.Context, originPath, worktreePath, worktreeName string, prNumber int, timeout time.Duration, log Logger) error {
	GitMu.Lock()
	defer GitMu.Unlock()

	if _, err := os.Stat(worktreePath); err == nil {
		return nil
	}

	branch := fmt.Sprintf("pr-%d", prNumber)

	log.logf("Fetching pull/%d/head...", prNumber)
	fetchRef := fmt.Sprintf("+pull/%d/head:%s", prNumber, branch)
	if err := runGit(ctx, originPath, timeout, "git fetch", "fetch", "origin", fetchRef); err != nil {
		return err
	}

	log.logf("Creating worktree %s...", worktreeName)
	if err := runGit(ctx, originPath, timeout, "git worktree add", "worktree", "add", "--no-checkout", worktreePath, branch); err != nil {
		CleanupFailedAdd(originPath, worktreePath, branch)
		return err
	}

	if err := runGit(ctx, worktreePath, timeout, "git checkout in worktree", "checkout"); err != nil {
		CleanupFailedAdd(originPath, worktreePath, branch)
		return err
	}

	EnsureNestedExcluded(originPath, worktreePath)

	lockFile := filepath.Join(originPath, ".git", "worktrees", worktreeName, "index.lock")
	RemoveStaleLock(lockFile, worktreeName)

	return nil
}
