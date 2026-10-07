package reconciler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	ghpkg "github.com/mgreau/zen/internal/github"
	wt "github.com/mgreau/zen/internal/worktree"
)

// cleanupGitTimeout bounds the pull/N/head fetch RemoveReview may need before
// it can compare a worktree's HEAD with the PR head.
const cleanupGitTimeout = 2 * time.Minute

// prMergeInfoFunc is the GitHub REST lookup merged-review cleanup needs.
// Tests replace it so cleanup runs without a network.
type prMergeInfoFunc func(ctx context.Context, fullRepo string, prNumber int) (*ghpkg.PRMergeInfo, error)

// CleanupReconciler removes the review worktrees of merged PRs. Work streams
// (feature worktrees) are never touched; `zen cleanup` is the manual command
// that covers those.
type CleanupReconciler struct {
	cfg         *config.Config
	prMergeInfo prMergeInfoFunc
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

	worktreePath := wt.PRPath(basePath, repo, prNumber)
	originPath := filepath.Join(basePath, repo)
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil // already removed
	}

	lookup, err := r.lookup(ctx)
	if err != nil {
		return fmt.Errorf("GitHub client: %w", err)
	}
	info, err := lookup(ctx, r.cfg.RepoFullName(repo), prNumber)
	if err != nil {
		return fmt.Errorf("fetch PR merge state: %w", err)
	}

	// The scan that queued this key may be stale, and the daemon only ever
	// removes merged reviews: check again before touching the worktree.
	after := r.cfg.Watch.CleanupAfterMergeDuration()
	if info.State != "MERGED" {
		logf("Cleanup skipped for %s: PR is %s, not merged", label, info.State)
		return nil
	}
	if !mergedFor(info, after, time.Now()) {
		logf("Cleanup skipped for %s: merged less than cleanup_after_merge (%s) ago", label, after)
		return nil
	}

	gitCtx, cancel := context.WithTimeout(ctx, cleanupGitTimeout)
	defer cancel()

	// Safety refusals (local changes or commits, a running agent, not a review
	// checkout, or a HEAD that cannot be compared with the PR head) are
	// expected skips; Git and filesystem failures retry.
	if err := wt.RemoveReview(gitCtx, originPath, worktreePath, prNumber, info.HeadSHA); err != nil {
		if wt.RemovalBlocked(err) {
			logf("Cleanup skipped for %s: %v", label, err)
			return nil
		}
		return fmt.Errorf("remove worktree: %w", err)
	}

	logf("Cleanup complete for %s", label)
	return nil
}

func (r *CleanupReconciler) lookup(ctx context.Context) (prMergeInfoFunc, error) {
	if r.prMergeInfo != nil {
		return r.prMergeInfo, nil
	}
	client, err := ghpkg.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.GetPRMergeInfo, nil
}

// mergedFor reports whether info is a merged PR whose mergedAt is at least
// after before now.
func mergedFor(info *ghpkg.PRMergeInfo, after time.Duration, now time.Time) bool {
	return info != nil && info.State == "MERGED" && !info.MergedAt.IsZero() && now.Sub(info.MergedAt) >= after
}

// ScanMergedPRs queues cleanup for PR review worktrees whose PR merged at
// least cleanupAfterMerge ago, by GitHub's mergedAt. Closed-unmerged PRs and
// work streams are left alone.
func ScanMergedPRs(ctx context.Context, cfg *config.Config, queue workqueue.Interface, cleanupAfterMerge time.Duration) {
	ghClient, err := ghpkg.NewClient(ctx)
	if err != nil {
		logf("Error creating GitHub client for cleanup scan: %v", err)
		return
	}
	scanMergedPRs(ctx, cfg, queue, ghClient.GetPRMergeInfo, cleanupAfterMerge, time.Now())
}

func scanMergedPRs(ctx context.Context, cfg *config.Config, queue workqueue.Interface, lookup prMergeInfoFunc, cleanupAfterMerge time.Duration, now time.Time) {
	wts, err := wt.ListAll(cfg)
	if err != nil {
		logf("Error listing worktrees for cleanup scan: %v", err)
		return
	}

	for _, w := range wts {
		// Only PR reviews (see worktree.Classify), and only at the path
		// Reconcile removes for that PR.
		if w.Type != wt.TypePRReview || w.PRNumber == 0 || w.Name != wt.PRName(w.Repo, w.PRNumber) {
			continue
		}
		info, err := lookup(ctx, cfg.RepoFullName(w.Repo), w.PRNumber)
		if err != nil {
			continue // skip on API error, try next cycle
		}
		if !mergedFor(info, cleanupAfterMerge, now) {
			continue
		}
		key := MakePRKey(w.Repo, w.PRNumber)
		if err := queue.Queue(ctx, key, workqueue.Options{}); err != nil {
			logf("Error queuing cleanup for %s PR #%d: %v", w.Repo, w.PRNumber, err)
		}
	}
}
