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
	"github.com/mgreau/zen/internal/review"
	wt "github.com/mgreau/zen/internal/worktree"
)

// prDetailsFunc is GitHub REST lookup. Tests replace it so Reconcile can
// skip closed/draft PRs without a network or a real git origin.
type prDetailsFunc func(ctx context.Context, fullRepo string, prNumber int) (*ghpkg.PRDetails, error)

// SetupReconciler prepares worktrees for new PR reviews.
// It runs 3 idempotent steps: ensureWorktree, ensureContextInjected, cachePRMeta.
type SetupReconciler struct {
	cfg *config.Config

	prDataMu  sync.RWMutex
	prData    map[string]ghpkg.ReviewRequest
	prDetails prDetailsFunc
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

	worktreeName := wt.PRName(repo, prNumber)
	worktreePath := filepath.Join(basePath, worktreeName)
	originPath := filepath.Join(basePath, repo)
	fullRepo := r.cfg.RepoFullName(repo)

	if skip, err := r.skipIfPRInactive(ctx, fullRepo, repo, prNumber, &pr); err != nil {
		return err
	} else if skip {
		logf("Leaving %s alone (closed or silenced draft)", label)
		return nil
	}

	created := false
	if _, err := os.Stat(worktreePath); err != nil {
		created = true
	}

	// Step 1: Ensure worktree exists and is on the GitHub head SHA.
	outcome, err := r.ensureWorktree(ctx, originPath, worktreePath, worktreeName, prNumber, pr.HeadRefOid)
	if err != nil {
		if wt.IsMissingPRRef(err) {
			logf("PR ref missing for %s; leaving worktree: %v", label, err)
			return nil
		}
		if wt.IsNonFastForward(err) {
			logf("Could not move %s onto rewritten GitHub head: %v", label, err)
			return nil
		}
		return fmt.Errorf("ensureWorktree: %w", err)
	}

	switch outcome {
	case review.SyncSkippedDirty, review.SyncSkippedAgent:
		logf("Leaving %s alone (%s); will retry next poll", label, outcome)
		return nil
	case review.SyncSkippedReset:
		logf("Leaving %s alone (rewritten GitHub head); run zen review %d to reset", label, prNumber)
		return nil
	}

	// Step 2: Inject / rewrite PR context when created or HEAD moved.
	forceContext := created || outcome == review.SyncUpdated
	if err := r.ensureContextInjected(ctx, worktreePath, fullRepo, prNumber, forceContext); err != nil {
		logf("Warning: failed to inject PR context for %s: %v", label, err)
	}

	// Step 3: Cache PR metadata for display commands (non-blocking)
	prcache.Set(repo, prNumber, pr.Title, pr.Author.Login)

	switch {
	case created:
		if err := notify.WorktreeReady(prNumber, worktreePath); err != nil {
			logf("Warning: notification failed for %s: %v", label, err)
		}
		logf("Setup complete for %s (worktree: %s)", label, worktreePath)
	case outcome == review.SyncUpdated:
		if err := notify.PRUpdated(prNumber, pr.Title, repo); err != nil {
			logf("Warning: update notification failed for %s: %v", label, err)
		}
		logf("Refreshed %s to %s", label, pr.HeadRefOid)
	default:
		logf("Worktree already current for %s", label)
	}
	return nil
}

func (r *SetupReconciler) ensureWorktree(ctx context.Context, originPath, worktreePath, worktreeName string, prNumber int, wantSHA string) (review.SyncOutcome, error) {
	if _, err := os.Stat(worktreePath); err == nil {
		return review.SyncExisting(ctx, originPath, worktreePath, prNumber, wantSHA, func(msg string) {
			logf("%s", msg)
		}, nil)
	}
	if err := wt.CreateFromPR(originPath, worktreePath, worktreeName, prNumber); err != nil {
		return review.SyncMissing, err
	}
	return review.SyncUpdated, nil
}

func (r *SetupReconciler) ensureContextInjected(ctx context.Context, worktreePath, fullRepo string, prNumber int, force bool) error {
	ag := r.cfg.NewAgent("")
	if !force && ag.ContextPresent(worktreePath) {
		return nil
	}
	rendered, err := ctxpkg.RenderPRContext(ctx, fullRepo, prNumber)
	if err != nil {
		return err
	}
	_, err = ag.InjectContext(worktreePath, rendered)
	return err
}

func (r *SetupReconciler) skipIfPRInactive(ctx context.Context, fullRepo, repoShort string, prNumber int, pr *ghpkg.ReviewRequest) (bool, error) {
	if !ShouldSyncWorktree(r.cfg.IgnoreDrafts, pr.IsDraft, pr.Closed) {
		return true, nil
	}
	details, err, ok := r.livePRDetails(ctx, fullRepo, prNumber)
	if !ok {
		return false, nil
	}
	if err != nil {
		if ghpkg.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	if details == nil {
		return true, nil
	}
	closed := PRStateClosed(details.State)
	if !ShouldSyncWorktree(r.cfg.IgnoreDrafts, details.Draft, closed) {
		return true, nil
	}
	if details.HeadSHA != "" {
		pr.HeadRefOid = details.HeadSHA
		r.StorePRData(MakePRKey(repoShort, prNumber), *pr)
	}
	return false, nil
}

func (r *SetupReconciler) livePRDetails(ctx context.Context, fullRepo string, prNumber int) (*ghpkg.PRDetails, error, bool) {
	if r.prDetails != nil {
		d, err := r.prDetails(ctx, fullRepo, prNumber)
		return d, err, true
	}
	client, err := ghpkg.NewClient(ctx)
	if err != nil {
		return nil, nil, false // snapshot fields are enough; don't fail setup
	}
	d, err := client.GetPRDetails(ctx, fullRepo, prNumber)
	return d, err, true
}

func logf(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
