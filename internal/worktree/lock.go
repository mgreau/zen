package worktree

import (
	"fmt"
	"os"
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
		removeStaleLock(lockFile, entry.Name())
	}

	// Also check the main repo's own index.lock
	mainLock := filepath.Join(gitDir, "index.lock")
	removeStaleLock(mainLock, repo)
}

// CleanAllStaleLocks cleans stale locks across all known repos.
func CleanAllStaleLocks(cfg *config.Config) {
	for _, repo := range cfg.RepoNames() {
		CleanStaleLocks(cfg, repo)
	}
}

func removeStaleLock(lockFile, name string) {
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
