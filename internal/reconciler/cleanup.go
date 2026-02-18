package reconciler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	ghpkg "github.com/mgreau/zen/internal/github"
	wt "github.com/mgreau/zen/internal/worktree"
)

// CleanupReconciler removes worktrees for merged PRs.
type CleanupReconciler struct {
	cfg *config.Config
}

// NewCleanupReconciler creates a new CleanupReconciler.
func NewCleanupReconciler(cfg *config.Config) *CleanupReconciler {
	return &CleanupReconciler{cfg: cfg}
}

// SetConfig updates the config used by this reconciler.
func (r *CleanupReconciler) SetConfig(cfg *config.Config) {
	r.cfg = cfg
}

// Reconcile processes a single cleanup key.
func (r *CleanupReconciler) Reconcile(ctx context.Context, key string, _ workqueue.Options) error {
	repo, prNumber, err := ParsePRKey(key)
	if err != nil {
		return workqueue.NonRetriableError(err, "invalid key format")
	}

	label := fmt.Sprintf("%s PR #%d", repo, prNumber)

	basePath := r.cfg.RepoBasePath(repo)
	if basePath == "" {
		return workqueue.NonRetriableError(
			fmt.Errorf("unknown repo %q", repo),
			"repo not configured",
		)
	}

	worktreeName := fmt.Sprintf("%s-pr-%d", repo, prNumber)
	worktreePath := filepath.Join(basePath, worktreeName)
	originPath := filepath.Join(basePath, repo)

	// Remove worktree (retryable on failure)
	if err := removeWorktree(originPath, worktreePath); err != nil {
		return fmt.Errorf("removeWorktree: %w", err)
	}

	logf("Cleanup complete for %s", label)
	return nil
}

func removeWorktree(originPath, worktreePath string) error {
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil // already removed
	}

	removeCmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	removeCmd.Dir = originPath
	if out, err := removeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, string(out))
	}
	return nil
}

// ScanMergedPRs finds worktrees for merged PRs older than the given age
// and queues them for cleanup.
func ScanMergedPRs(ctx context.Context, cfg *config.Config, queue workqueue.Interface, cleanupAfterDays int) {
	wts, err := wt.ListAll(cfg)
	if err != nil {
		logf("Error listing worktrees for cleanup scan: %v", err)
		return
	}

	ghClient, err := ghpkg.NewClient(ctx)
	if err != nil {
		logf("Error creating GitHub client for cleanup scan: %v", err)
		return
	}

	for _, w := range wts {
		if w.Type != wt.TypePRReview || w.PRNumber == 0 {
			continue
		}
		fullRepo := cfg.RepoFullName(w.Repo)
		state, err := ghClient.GetPRState(ctx, fullRepo, w.PRNumber)
		if err != nil {
			continue // skip on API error, try next cycle
		}
		if state != "MERGED" {
			continue
		}
		age, err := wt.AgeDays(w.Path)
		if err != nil || age < cleanupAfterDays {
			continue
		}
		key := MakePRKey(w.Repo, w.PRNumber)
		if err := queue.Queue(ctx, key, workqueue.Options{}); err != nil {
			logf("Error queuing cleanup for %s PR #%d: %v", w.Repo, w.PRNumber, err)
		}
	}
}
