package reconciler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	ctxpkg "github.com/mgreau/zen/internal/context"
	ghpkg "github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/notify"
	"github.com/mgreau/zen/internal/prcache"
	wt "github.com/mgreau/zen/internal/worktree"
)

// SetupReconciler prepares worktrees for new PR reviews.
// It runs 3 idempotent steps: ensureWorktree, ensureContextInjected, cachePRMeta.
type SetupReconciler struct {
	cfg *config.Config

	prDataMu sync.RWMutex
	prData   map[string]ghpkg.ReviewRequest
}

// NewSetupReconciler creates a new SetupReconciler.
func NewSetupReconciler(cfg *config.Config) *SetupReconciler {
	return &SetupReconciler{
		cfg:    cfg,
		prData: make(map[string]ghpkg.ReviewRequest),
	}
}

// SetConfig updates the config used by this reconciler.
func (r *SetupReconciler) SetConfig(cfg *config.Config) {
	r.prDataMu.Lock()
	defer r.prDataMu.Unlock()
	r.cfg = cfg
}

// StorePRData stores PR metadata for later use during reconciliation.
func (r *SetupReconciler) StorePRData(key string, pr ghpkg.ReviewRequest) {
	r.prDataMu.Lock()
	defer r.prDataMu.Unlock()
	r.prData[key] = pr
}

func (r *SetupReconciler) getPRData(key string) (ghpkg.ReviewRequest, bool) {
	r.prDataMu.RLock()
	defer r.prDataMu.RUnlock()
	pr, ok := r.prData[key]
	return pr, ok
}

// Reconcile processes a single PR key through 3 idempotent steps.
func (r *SetupReconciler) Reconcile(ctx context.Context, key string, _ workqueue.Options) error {
	repo, prNumber, err := ParsePRKey(key)
	if err != nil {
		return workqueue.NonRetriableError(err, "invalid key format")
	}

	basePath := r.cfg.RepoBasePath(repo)
	if basePath == "" {
		return workqueue.NonRetriableError(
			fmt.Errorf("unknown repo %q", repo),
			"repo not configured",
		)
	}

	pr, ok := r.getPRData(key)
	if !ok {
		return workqueue.NonRetriableError(
			fmt.Errorf("no PR data for key %q", key),
			"missing PR metadata",
		)
	}

	label := fmt.Sprintf("%s PR #%d %q", repo, prNumber, pr.Title)

	worktreeName := fmt.Sprintf("%s-pr-%d", repo, prNumber)
	worktreePath := wt.Resolve(r.cfg, repo, worktreeName)
	originPath := filepath.Join(basePath, repo)
	fullRepo := r.cfg.RepoFullName(repo)

	// Step 1: Ensure worktree exists (retryable on failure)
	if err := r.ensureWorktree(ctx, originPath, worktreePath, worktreeName, prNumber); err != nil {
		return fmt.Errorf("ensureWorktree: %w", err)
	}

	// Step 2: Ensure PR context is injected (non-blocking)
	if err := r.ensureContextInjected(ctx, worktreePath, fullRepo, prNumber); err != nil {
		logf("Warning: failed to inject PR context for %s: %v", label, err)
	}

	// Step 3: Cache PR metadata for display commands (non-blocking)
	prcache.Set(repo, prNumber, pr.Title, pr.Author.Login)

	if err := notify.WorktreeReady(prNumber, worktreePath); err != nil {
		logf("Warning: notification failed for %s: %v", label, err)
	}
	logf("Setup complete for %s (worktree: %s)", label, worktreePath)
	return nil
}

func (r *SetupReconciler) ensureWorktree(ctx context.Context, originPath, worktreePath, worktreeName string, prNumber int) error {
	if _, err := os.Stat(worktreePath); err == nil {
		return nil // already exists
	}
	// No per-command timeout: the daemon's workqueue already bounds and
	// retries a stuck reconcile, and ctx carries its cancellation.
	return wt.CreateFromPR(ctx, originPath, worktreePath, worktreeName, prNumber, 0, nil)
}

func (r *SetupReconciler) ensureContextInjected(ctx context.Context, worktreePath, fullRepo string, prNumber int) error {
	ag := r.cfg.NewAgent("")
	if ag.ContextPresent(worktreePath) {
		return nil // already injected
	}
	rendered, err := ctxpkg.RenderPRContext(ctx, fullRepo, prNumber)
	if err != nil {
		return err
	}
	_, err = ag.InjectContext(worktreePath, rendered)
	return err
}

func logf(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
