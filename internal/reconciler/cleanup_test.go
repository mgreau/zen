package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
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
