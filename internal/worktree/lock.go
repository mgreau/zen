package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mgreau/zen/internal/ui"
)

// GitMu serializes git worktree operations to prevent concurrent index.lock conflicts.
var GitMu sync.Mutex

// staleLockAge is how old an index.lock must be before zen treats it as left
// behind by a crashed git process. git holds the lock only while it writes a
// new index, which takes seconds even on a large repository, and the lock
// file holds that new index rather than a PID, so age is the only signal that
// separates an abandoned lock from one a running git still needs.
var staleLockAge = 10 * time.Minute

// RemoveStaleLock removes lockFile if it is a regular file last modified more
// than staleLockAge ago, and reports whether it did. Missing files, younger
// locks, and anything that is not a regular file are left alone: removing a
// lock that a running git still holds makes its rename fail with "unable to
// write new index file".
func RemoveStaleLock(lockFile, name string) bool {
	info, err := os.Lstat(lockFile)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if time.Since(info.ModTime()) < staleLockAge {
		return false
	}
	if err := os.Remove(lockFile); err != nil {
		return false
	}
	ui.LogWarn(fmt.Sprintf("Removed stale index.lock for %s (last modified %s)", name, info.ModTime().Format(time.RFC3339)))
	return true
}

// recoverIndexLock is called after a git command in worktreePath failed. When
// the failure names index.lock and that lock is stale, it removes the lock and
// reports true so the caller can retry once. A fresh lock means another git is
// running in the worktree; it is left alone and the original error stands.
func recoverIndexLock(worktreePath, output string) bool {
	if !strings.Contains(output, "index.lock") {
		return false
	}
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-path", "index.lock")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return RemoveStaleLock(strings.TrimSpace(string(out)), filepath.Base(worktreePath))
}

// CleanupFailedAdd cleans up after a failed "git worktree add" that may have
// created the branch and/or a partial worktree directory but failed to complete.
// It removes the partial worktree directory, prunes git's worktree metadata,
// and deletes the orphaned branch.
//
// originPath is the main repo directory, worktreePath is the target worktree
// directory, and branch is the git branch that was being created.
func CleanupFailedAdd(originPath, worktreePath, branch string) {
	// Remove partial worktree directory if it exists
	if _, err := os.Stat(worktreePath); err == nil {
		os.RemoveAll(worktreePath)
	}

	// Prune stale worktree metadata
	pruneCmd := execCommand("git", "worktree", "prune")
	pruneCmd.Dir = originPath
	pruneCmd.CombinedOutput()

	// Delete the orphaned branch
	delCmd := execCommand("git", "branch", "-D", branch)
	delCmd.Dir = originPath
	delCmd.CombinedOutput()
}

// execCommand is a variable for testing.
var execCommand = exec.Command
