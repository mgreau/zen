package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mgreau/zen/internal/config"
)

// writeLock writes an index.lock holding index bytes, as git does, aged by age.
func writeLock(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte("DIRC\x00\x00\x00\x02 12345 not a pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-age)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveStaleLock(t *testing.T) {
	dir := t.TempDir()

	fresh := filepath.Join(dir, "fresh.lock")
	writeLock(t, fresh, time.Second)
	if RemoveStaleLock(fresh, "fresh") {
		t.Error("removed a lock a running git could still hold")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh lock is gone: %v", err)
	}

	stale := filepath.Join(dir, "stale.lock")
	writeLock(t, stale, staleLockAge+time.Minute)
	if !RemoveStaleLock(stale, "stale") {
		t.Error("kept a lock older than staleLockAge")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale lock still present: %v", err)
	}

	if RemoveStaleLock(filepath.Join(dir, "missing.lock"), "missing") {
		t.Error("reported removing a lock that does not exist")
	}

	notFile := filepath.Join(dir, "dir.lock")
	if err := os.Mkdir(notFile, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(notFile, old, old); err != nil {
		t.Fatal(err)
	}
	if RemoveStaleLock(notFile, "dir") {
		t.Error("removed something that is not a regular file")
	}

	link := filepath.Join(dir, "link.lock")
	target := filepath.Join(dir, "target")
	writeLock(t, target, 2*staleLockAge)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if RemoveStaleLock(link, "link") {
		t.Error("removed a symlink")
	}
}

// Listing worktrees must never touch a lock, however old: a listing runs on
// every daemon poll and every zen command, concurrently with the user's git.
func TestListForRepo_leavesLocksAlone(t *testing.T) {
	base := t.TempDir()
	origin := filepath.Join(base, "repo")
	initRepo(t, origin)
	wtDir := filepath.Join(base, "repo-pr-1")
	git(t, origin, "worktree", "add", "-b", "pr-1", wtDir)

	mainLock := filepath.Join(origin, ".git", "index.lock")
	wtLock := filepath.Join(origin, ".git", "worktrees", "repo-pr-1", "index.lock")
	writeLock(t, mainLock, time.Second)
	writeLock(t, wtLock, 2*staleLockAge)

	cfg := &config.Config{Repos: map[string]config.RepoConfig{"repo": {FullName: "o/repo", BasePath: base}}}
	if _, err := ListForRepo(cfg, "repo"); err != nil {
		t.Fatal(err)
	}
	for _, lock := range []string{mainLock, wtLock} {
		if _, err := os.Stat(lock); err != nil {
			t.Errorf("ListForRepo touched %s: %v", lock, err)
		}
	}
}

func TestResetToRemotePR_recoversStaleLockOnly(t *testing.T) {
	orig := t.TempDir()
	initRepo(t, orig)
	git(t, orig, "branch", "pr-1")
	clone := t.TempDir()
	git(t, orig, "clone", orig, clone)
	git(t, clone, "fetch", "origin", "pr-1:pr-1")
	wtDir := filepath.Join(t.TempDir(), "repo-pr-1")
	git(t, clone, "worktree", "add", wtDir, "pr-1")
	if err := FetchRefspec(context.Background(), clone, "+pr-1:"+RemotePRRef(1)); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(clone, ".git", "worktrees", "repo-pr-1", "index.lock")

	writeLock(t, lock, time.Second)
	if err := ResetToRemotePR(context.Background(), wtDir, 1); err == nil {
		t.Fatal("reset succeeded over a fresh lock")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("fresh lock was removed: %v", err)
	}

	writeLock(t, lock, 2*staleLockAge)
	if err := ResetToRemotePR(context.Background(), wtDir, 1); err != nil {
		t.Fatalf("reset did not recover from a stale lock: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("stale lock still present after recovery: %v", err)
	}
}
