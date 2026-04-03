package reconciler

import (
	"fmt"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/notify"
	"github.com/mgreau/zen/internal/worktree"
)

// SendDigest reads cached state files and sends a compact summary notification
// if there is anything actionable (waiting sessions or pending PR reviews).
// Silent if everything is quiet.
func SendDigest(cfg *config.Config) {
	// Count waiting sessions from the cached snapshot (2-minute freshness window)
	var waitingSessions int
	snapshot, err := ReadSessionSnapshot()
	if err == nil && IsSnapshotFresh(snapshot, 2*time.Minute) {
		for _, s := range snapshot.Sessions {
			if s.Status == "waiting" {
				waitingSessions++
			}
		}
	}

	// Count worktrees by type
	wts, err := worktree.ListAll(cfg)
	if err != nil {
		fmt.Printf("[%s] Digest: error listing worktrees: %v\n",
			time.Now().Format(time.RFC3339), err)
		return
	}
	var pendingReviews, featureWork int
	for _, wt := range wts {
		switch wt.Type {
		case worktree.TypePRReview:
			pendingReviews++
		case worktree.TypeFeature:
			featureWork++
		}
	}

	if err := notify.Digest(waitingSessions, pendingReviews, featureWork); err != nil {
		fmt.Printf("[%s] Digest notify error: %v\n", time.Now().Format(time.RFC3339), err)
	}
}
