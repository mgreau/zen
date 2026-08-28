package worktree

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

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
