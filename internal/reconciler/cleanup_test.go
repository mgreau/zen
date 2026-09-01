package reconciler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	wt "github.com/mgreau/zen/internal/worktree"
)

func TestCleanupReconcile_InvalidKey(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
	}}
	rec := NewCleanupReconciler(cfg)

	err := rec.Reconcile(context.Background(), "badkey", workqueue.Options{})
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if workqueue.GetNonRetriableDetails(err) == nil {
		t.Error("expected NonRetriableError for invalid key format")
	}
}

func TestCleanupReconcile_MissingWorktree(t *testing.T) {
	// Create a temp config pointing to a temp directory
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "testrepo")
	os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755)

	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{
			"testrepo": {FullName: "test/testrepo", BasePath: tmpDir},
		},
	}
	rec := NewCleanupReconciler(cfg)

	// Worktree path doesn't exist, so removeWorktree should be a no-op
	err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{})
	if err != nil {
		t.Fatalf("unexpected error for missing worktree: %v", err)
	}
}

// initGitRepo makes a real repo with one commit, so `git worktree add` and
// `git worktree remove` behave as they do in production.
func initGitRepo(t *testing.T, dir, name string) string {
	t.Helper()
	repoPath := filepath.Join(dir, name)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return repoPath
}

// A user mid-transition has worktrees in the old layout and nested in the
// config. Cleanup must remove them where they actually are.
//
// This is the failure that would otherwise be silent: computing the path
// from the layout alone yields a nested path that does not exist,
// removeWorktree's os.Stat takes its "already removed" branch, and the
// queue reports success while the sibling worktree stays on disk forever.
func TestCleanupReconcile_SiblingWorktreeUnderNestedLayout(t *testing.T) {
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repoPath := initGitRepo(t, tmpDir, "testrepo")

	// A review worktree created before the layout was switched.
	siblingPath := filepath.Join(tmpDir, "testrepo-pr-42")
	cmd := exec.Command("git", "worktree", "add", "-b", "pr-42", siblingPath)
	cmd.Dir = repoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}

	cfg := &config.Config{
		WorktreeLayout: config.LayoutNested,
		Repos: map[string]config.RepoConfig{
			"testrepo": {FullName: "test/testrepo", BasePath: tmpDir},
		},
	}

	if err := NewCleanupReconciler(cfg).Reconcile(context.Background(), "testrepo:42", workqueue.Options{}); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}

	if _, err := os.Stat(siblingPath); !os.IsNotExist(err) {
		t.Errorf("sibling worktree still on disk at %s -- cleanup reported success without removing it", siblingPath)
	}
}

// The mirror: a nested worktree with the layout rolled back to sibling.
func TestCleanupReconcile_NestedWorktreeUnderSiblingLayout(t *testing.T) {
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repoPath := initGitRepo(t, tmpDir, "testrepo")

	nestedPath := filepath.Join(repoPath, wt.NestedDir, "testrepo-pr-42")
	cmd := exec.Command("git", "worktree", "add", "-b", "pr-42", nestedPath)
	cmd.Dir = repoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}

	cfg := &config.Config{
		WorktreeLayout: config.LayoutSibling,
		Repos: map[string]config.RepoConfig{
			"testrepo": {FullName: "test/testrepo", BasePath: tmpDir},
		},
	}

	if err := NewCleanupReconciler(cfg).Reconcile(context.Background(), "testrepo:42", workqueue.Options{}); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}

	if _, err := os.Stat(nestedPath); !os.IsNotExist(err) {
		t.Errorf("nested worktree still on disk at %s", nestedPath)
	}
}
