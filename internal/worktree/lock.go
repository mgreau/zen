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

// RemoteForRepo returns the name of the git remote in originPath whose URL
// points at fullRepo ("owner/name"). This lets zen operate in clones that
// follow the fork workflow, where the canonical repo is a non-"origin" remote
// (commonly "upstream") and "origin" is a personal fork that has no
// pull/N/head refs. Falls back to "origin" when no remote matches or the
// lookup fails.
func RemoteForRepo(originPath, fullRepo string) string {
	const fallback = "origin"
	if fullRepo == "" {
		return fallback
	}
	cmd := execCommand("git", "remote", "-v")
	cmd.Dir = originPath
	out, err := cmd.Output()
	if err != nil {
		return fallback
	}
	want := strings.ToLower(fullRepo)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if repoFromRemoteURL(fields[1]) == want {
			return fields[0]
		}
	}
	return fallback
}

// repoFromRemoteURL extracts a lowercased "owner/name" from a git remote URL,
// handling scp-style (git@github.com:owner/name.git), https, and ssh forms.
func repoFromRemoteURL(url string) string {
	u := strings.TrimSuffix(url, ".git")
	if i := strings.Index(u, "://"); i != -1 {
		// scheme://[user@]host/owner/name
		u = u[i+3:]
		if j := strings.Index(u, "/"); j != -1 {
			u = u[j+1:]
		}
	} else if i := strings.LastIndex(u, ":"); i != -1 {
		// scp-style git@host:owner/name
		u = u[i+1:]
	}
	return strings.ToLower(u)
}

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
