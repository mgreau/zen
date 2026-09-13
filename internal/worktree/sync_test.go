package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "file.txt")
	git(t, dir, "commit", "-m", "A")
}

func TestSHAEqual(t *testing.T) {
	full := "abcdef1234567890abcdef1234567890abcdef12"
	if !SHAEqual(full, full) {
		t.Fatal("identical SHAs should match")
	}
	if !SHAEqual(full, full[:12]) {
		t.Fatal("prefix should match")
	}
	if SHAEqual("", full) {
		t.Fatal("empty should not match")
	}
	if SHAEqual("abc", "abd") {
		t.Fatal("short unequal should not match")
	}
}

func TestTrackedDirty_ignoresUntracked(t *testing.T) {
	orig := t.TempDir()
	initRepo(t, orig)
	if dirty, err := TrackedDirty(orig); err != nil || dirty {
		t.Fatalf("clean repo dirty=%v err=%v", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(orig, "CLAUDE.local.md"), []byte("ctx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, err := TrackedDirty(orig); err != nil || dirty {
		t.Fatalf("untracked context file should not count as dirty: dirty=%v err=%v", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(orig, "file.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, err := TrackedDirty(orig); err != nil || !dirty {
		t.Fatalf("tracked edit should be dirty: dirty=%v err=%v", dirty, err)
	}
}

func TestFastForward_advancesWorktree(t *testing.T) {
	orig := t.TempDir()
	initRepo(t, orig)
	shaA := git(t, orig, "rev-parse", "HEAD")
	git(t, orig, "branch", "pr-1")

	clone := t.TempDir()
	git(t, orig, "clone", orig, clone)
	git(t, clone, "config", "user.email", "test@example.com")
	git(t, clone, "config", "user.name", "test")
	git(t, clone, "fetch", "origin", "pr-1:pr-1")

	wtDir := filepath.Join(t.TempDir(), "repo-pr-1")
	git(t, clone, "worktree", "add", wtDir, "pr-1")

	if err := os.WriteFile(filepath.Join(orig, "file.txt"), []byte("B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, orig, "add", "file.txt")
	git(t, orig, "commit", "-m", "B")
	shaB := git(t, orig, "rev-parse", "HEAD")
	git(t, orig, "branch", "-f", "pr-1", shaB)

	if err := FetchRefspec(context.Background(), clone, "+pr-1:"+RemotePRRef(1)); err != nil {
		t.Fatal(err)
	}
	if err := FastForward(context.Background(), wtDir, 1); err != nil {
		t.Fatal(err)
	}
	head, err := HEAD(wtDir)
	if err != nil {
		t.Fatal(err)
	}
	if !SHAEqual(head, shaB) {
		t.Fatalf("HEAD=%s want %s (was %s)", head, shaB, shaA)
	}
}

func TestFastForward_nonFF(t *testing.T) {
	orig := t.TempDir()
	initRepo(t, orig)
	git(t, orig, "branch", "pr-1")

	clone := t.TempDir()
	git(t, orig, "clone", orig, clone)
	git(t, clone, "config", "user.email", "test@example.com")
	git(t, clone, "config", "user.name", "test")
	git(t, clone, "fetch", "origin", "pr-1:pr-1")

	wtDir := filepath.Join(t.TempDir(), "repo-pr-1")
	git(t, clone, "worktree", "add", wtDir, "pr-1")

	// Diverging commit on the worktree branch vs a rewritten remote.
	if err := os.WriteFile(filepath.Join(wtDir, "file.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wtDir, "add", "file.txt")
	git(t, wtDir, "commit", "-m", "local")

	if err := os.WriteFile(filepath.Join(orig, "file.txt"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, orig, "add", "file.txt")
	git(t, orig, "commit", "-m", "remote")
	git(t, orig, "branch", "-f", "pr-1", "HEAD")

	if err := FetchRefspec(context.Background(), clone, "+pr-1:"+RemotePRRef(1)); err != nil {
		t.Fatal(err)
	}
	err := FastForward(context.Background(), wtDir, 1)
	if !IsNonFastForward(err) {
		t.Fatalf("want non-ff error, got %v", err)
	}
}

func TestIsMissingPRRef(t *testing.T) {
	if IsMissingPRRef(nil) {
		t.Fatal("nil")
	}
	if !IsMissingPRRef(fmt.Errorf("git fetch origin +refs/pull/1/head:refs/remotes/origin/pr-1: exit status 128: fatal: couldn't find remote ref refs/pull/1/head")) {
		t.Fatal("couldn't find remote ref")
	}
	if !IsMissingPRRef(fmt.Errorf("git fetch: %w: %s", fmt.Errorf("exit status 128"), "fatal: couldn't find remote ref pull/9/head")) {
		t.Fatal("create-path wrap")
	}
	if IsMissingPRRef(fmt.Errorf("git fetch timed out: context deadline exceeded")) {
		t.Fatal("timeout is a retry, not a missing ref")
	}
}

func TestIsNonFastForward(t *testing.T) {
	if IsNonFastForward(nil) {
		t.Fatal("nil")
	}
	if !IsNonFastForward(&ErrNonFastForward{Detail: "diverging"}) {
		t.Fatal("typed")
	}
	if !IsNonFastForward(fmt.Errorf("not possible to fast-forward")) {
		t.Fatal("string")
	}
}

func TestUniqueCommitCount_diverged(t *testing.T) {
	orig := t.TempDir()
	initRepo(t, orig)
	git(t, orig, "branch", "pr-1")

	clone := t.TempDir()
	git(t, orig, "clone", orig, clone)
	git(t, clone, "config", "user.email", "test@example.com")
	git(t, clone, "config", "user.name", "test")
	git(t, clone, "fetch", "origin", "pr-1:pr-1")

	wtDir := filepath.Join(t.TempDir(), "repo-pr-1")
	git(t, clone, "worktree", "add", wtDir, "pr-1")

	if err := os.WriteFile(filepath.Join(wtDir, "file.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wtDir, "add", "file.txt")
	git(t, wtDir, "commit", "-m", "local")

	if err := FetchRefspec(context.Background(), clone, "+pr-1:"+RemotePRRef(1)); err != nil {
		t.Fatal(err)
	}
	n, err := UniqueCommitCount(wtDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("UniqueCommitCount=%d want 1", n)
	}
}
