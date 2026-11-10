package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ui"
)

// GitMu serializes git worktree operations to prevent concurrent index.lock conflicts.
var GitMu sync.Mutex

// RepoLock is a cross-process lock on one origin repository. GitMu only
// serializes worktree operations inside a single zen process, so `zen watch`
// creating review worktrees could race `zen work new` or `zen review` in
// another process on the same repository's index, refs, and config. The lock
// is an flock on a stable sibling file, .<repo>.zen.lock, which other tools
// that change the origin checkout can take too.
type RepoLock struct {
	file *os.File
}

// repoLockPath returns the sibling lock file path for originPath, resolving
// symlinks so every path to the same repository shares one lock.
func repoLockPath(originPath string) (string, error) {
	absolute, err := filepath.Abs(originPath)
	if err != nil {
		return "", fmt.Errorf("resolving repository path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving repository symlinks: %w", err)
	}
	return filepath.Join(filepath.Dir(canonical), "."+filepath.Base(canonical)+".zen.lock"), nil
}

// AcquireRepoLock blocks until no other process holds originPath's lock.
func AcquireRepoLock(originPath string) (*RepoLock, error) {
	path, err := repoLockPath(originPath)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening repository lock: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("locking repository: %w", err)
	}
	return &RepoLock{file: file}, nil
}

// Release unlocks and closes the lock. It is safe to call more than once.
func (lock *RepoLock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// CleanStaleLocks removes stale index.lock files from worktrees of the given repo.
// A lock is considered stale if the PID inside it is no longer running.
func CleanStaleLocks(cfg *config.Config, repo string) {
	basePath := cfg.RepoBasePath(repo)
	if basePath == "" {
		return
	}

	gitDir := filepath.Join(basePath, repo, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return
	}

	worktreesDir := filepath.Join(gitDir, "worktrees")
	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		lockFile := filepath.Join(worktreesDir, entry.Name(), "index.lock")
		RemoveStaleLock(lockFile, entry.Name())
	}

	// Also check the main repo's own index.lock
	mainLock := filepath.Join(gitDir, "index.lock")
	RemoveStaleLock(mainLock, repo)
}

// CleanAllStaleLocks cleans stale locks across all known repos.
func CleanAllStaleLocks(cfg *config.Config) {
	for _, repo := range cfg.RepoNames() {
		CleanStaleLocks(cfg, repo)
	}
}

// CleanupFailedAdd cleans up after a failed "git worktree add" that may have
// created the branch and/or a partial worktree directory but failed to complete
// (e.g., "Could not write new index file"). It removes the partial worktree
// directory, prunes git's worktree metadata, and deletes the orphaned branch.
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

// RemoveStaleLock removes an index.lock file only if the holding process
// is no longer running. Safe to call if the file does not exist.
func RemoveStaleLock(lockFile, name string) {
	data, err := os.ReadFile(lockFile)
	if err != nil {
		return // file doesn't exist or can't be read
	}

	// Try to extract PID from the lock file
	pidStr := strings.TrimSpace(string(data))
	// git writes host info too; extract first number
	for _, field := range strings.Fields(pidStr) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		// Check if process is alive
		if err := syscall.Kill(pid, 0); err == nil {
			return // process is alive, lock is legitimate
		}
		break
	}

	ui.LogWarn(fmt.Sprintf("Removing stale index.lock for worktree: %s", name))
	os.Remove(lockFile)
}
