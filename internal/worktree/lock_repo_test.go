package worktree

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepoLockSerializesHolders(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := AcquireRepoLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan *RepoLock, 1)
	failed := make(chan error, 1)
	go func() {
		lock, err := AcquireRepoLock(repo)
		if err != nil {
			failed <- err
			return
		}
		acquired <- lock
	}()
	select {
	case <-acquired:
		t.Fatal("second holder acquired the lock before the first released it")
	case err := <-failed:
		t.Fatalf("second holder failed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case second := <-acquired:
		if err := second.Release(); err != nil {
			t.Fatal(err)
		}
	case err := <-failed:
		t.Fatalf("second holder failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second holder did not acquire the lock after release")
	}
	if err := first.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestRepoLockPathIsSharedThroughSymlinks(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "repo-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	direct, err := repoLockPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	throughLink, err := repoLockPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".repo.zen.lock"); direct != want || throughLink != want {
		t.Errorf("lock paths %q and %q, want %q", direct, throughLink, want)
	}
}
