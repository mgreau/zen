package reconciler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chainguard.dev/driftlessaf/workqueue"
	"chainguard.dev/driftlessaf/workqueue/inmem"
	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	ghpkg "github.com/mgreau/zen/internal/github"
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
	rec.prMergeInfo = func(context.Context, string, int) (*ghpkg.PRMergeInfo, error) {
		t.Fatal("a missing worktree must not call GitHub")
		return nil, nil
	}

	// Worktree path doesn't exist, so cleanup is a no-op
	err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{})
	if err != nil {
		t.Fatalf("unexpected error for missing worktree: %v", err)
	}
}

func TestCleanupReconcile_DirtyWorktreeIsTerminalSkip(t *testing.T) {
	cfg, worktreePath, prHead := cleanupFixture(t)
	if err := os.WriteFile(filepath.Join(worktreePath, "notes.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := NewCleanupReconciler(cfg)
	rec.prMergeInfo = mergedInfo(prHead, 2*time.Hour)
	err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{})
	if err != nil {
		t.Fatalf("dirty worktree should be a terminal skip: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatal("dirty worktree was removed")
	}
}

func TestCleanupReconcile_LocalCommitIsTerminalSkip(t *testing.T) {
	cfg, worktreePath, prHead := cleanupFixture(t)
	if err := os.WriteFile(filepath.Join(worktreePath, "tracked"), []byte("reviewer fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCleanupGit(t, worktreePath, "commit", "-am", "reviewer fix")

	rec := NewCleanupReconciler(cfg)
	rec.prMergeInfo = mergedInfo(prHead, 2*time.Hour)
	err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{})
	if err != nil {
		t.Fatalf("a local commit beyond the PR head should be a terminal skip: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatal("worktree with a local commit was removed")
	}
}

func TestCleanupReconcile_RemovesMergedReviewWithOnlyZenContext(t *testing.T) {
	cfg, worktreePath, prHead := cleanupFixture(t)
	if _, err := agent.New(agent.Claude, "").InjectContext(worktreePath, "generated"); err != nil {
		t.Fatal(err)
	}

	rec := NewCleanupReconciler(cfg)
	rec.prMergeInfo = mergedInfo(prHead, 2*time.Hour)
	if err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{}); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatal("merged review with only zen-owned files was not removed")
	}
}

func TestCleanupReconcile_RechecksMergeGate(t *testing.T) {
	tests := []struct {
		name string
		info ghpkg.PRMergeInfo
	}{
		{name: "closed without merging", info: ghpkg.PRMergeInfo{State: "CLOSED"}},
		{name: "open", info: ghpkg.PRMergeInfo{State: "OPEN"}},
		{name: "merged too recently", info: ghpkg.PRMergeInfo{State: "MERGED", MergedAt: time.Now().Add(-30 * time.Minute)}},
		{name: "merged without a merge time", info: ghpkg.PRMergeInfo{State: "MERGED"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, worktreePath, prHead := cleanupFixture(t)
			info := tt.info
			info.HeadSHA = prHead

			rec := NewCleanupReconciler(cfg)
			rec.prMergeInfo = func(context.Context, string, int) (*ghpkg.PRMergeInfo, error) {
				return &info, nil
			}
			if err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{}); err != nil {
				t.Fatalf("Reconcile() error: %v", err)
			}
			if _, err := os.Stat(worktreePath); err != nil {
				t.Fatal("worktree was removed before its PR passed the merge gate")
			}
		})
	}
}

func TestScanMergedPRs(t *testing.T) {
	basePath := t.TempDir()
	originPath := filepath.Join(basePath, "mono")
	initCleanupRepo(t, basePath, originPath)
	runCleanupGit(t, originPath, "worktree", "add", "-b", "pr-1", filepath.Join(basePath, "mono-pr-1"))
	runCleanupGit(t, originPath, "worktree", "add", "-b", "pr-2", filepath.Join(basePath, "mono-pr-2"))
	runCleanupGit(t, originPath, "worktree", "add", "-b", "mgreau/fix-pr-3", filepath.Join(basePath, "mono-fix-pr-3"))
	runCleanupGit(t, originPath, "worktree", "add", "-b", "pr-4", filepath.Join(basePath, "mono-pr-4"))

	now := time.Now()
	prs := map[int]ghpkg.PRMergeInfo{
		1: {State: "MERGED", MergedAt: now.Add(-30 * time.Minute)},
		2: {State: "MERGED", MergedAt: now.Add(-2 * time.Hour)},
		3: {State: "MERGED", MergedAt: now.Add(-2 * time.Hour)},
		4: {State: "CLOSED"},
	}
	var looked []int
	lookup := func(_ context.Context, fullRepo string, prNumber int) (*ghpkg.PRMergeInfo, error) {
		if fullRepo != "example/mono" {
			t.Errorf("lookup repo = %q, want example/mono", fullRepo)
		}
		looked = append(looked, prNumber)
		info := prs[prNumber]
		return &info, nil
	}

	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "example/mono", BasePath: basePath},
	}}
	queue := inmem.NewWorkQueue(10)
	scanMergedPRs(context.Background(), cfg, queue, lookup, time.Hour, now)

	_, queued, _, err := queue.Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, k := range queued {
		keys = append(keys, k.Name())
	}
	// PR 1 merged 30m ago (< 1h), PR 3 is a work stream, PR 4 never merged.
	if strings.Join(keys, ",") != "mono:2" {
		t.Errorf("queued %v, want [mono:2]", keys)
	}
	for _, pr := range looked {
		if pr == 3 {
			t.Error("work stream mono-fix-pr-3 was looked up as a review of PR 3")
		}
	}
}

// cleanupFixture creates <base>/testrepo with one commit, the PR head, and
// the review worktree <base>/testrepo-pr-999 on branch pr-999.
func cleanupFixture(t *testing.T) (cfg *config.Config, worktreePath, prHead string) {
	t.Helper()
	basePath := t.TempDir()
	originPath := filepath.Join(basePath, "testrepo")
	worktreePath = filepath.Join(basePath, "testrepo-pr-999")
	initCleanupRepo(t, basePath, originPath)
	runCleanupGit(t, originPath, "worktree", "add", "-b", "pr-999", worktreePath)

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	cfg = &config.Config{Repos: map[string]config.RepoConfig{
		"testrepo": {FullName: "test/testrepo", BasePath: basePath},
	}}
	return cfg, worktreePath, strings.TrimSpace(string(out))
}

func initCleanupRepo(t *testing.T, basePath, originPath string) {
	t.Helper()
	runCleanupGit(t, basePath, "init", "-b", "main", originPath)
	runCleanupGit(t, originPath, "config", "user.email", "test@example.com")
	runCleanupGit(t, originPath, "config", "user.name", "Test")
	runCleanupGit(t, originPath, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(originPath, "tracked"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCleanupGit(t, originPath, "add", "tracked")
	runCleanupGit(t, originPath, "commit", "-m", "initial")
}

func mergedInfo(prHead string, ago time.Duration) prMergeInfoFunc {
	return func(context.Context, string, int) (*ghpkg.PRMergeInfo, error) {
		return &ghpkg.PRMergeInfo{State: "MERGED", HeadSHA: prHead, MergedAt: time.Now().Add(-ago)}, nil
	}
}

func runCleanupGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
