// Package board implements `zen board`, a live view of the user's own open
// pull requests and the PRs they've been asked to review.
package board

import "github.com/mgreau/zen/internal/github"

// Status is a display bucket for one of the user's own open pull requests.
type Status string

// The status buckets for "My PRs", in display priority order (most
// actionable-by-you first).
const (
	StatusDraft            Status = "draft"
	StatusFailingCI        Status = "failing_ci"
	StatusChangesRequested Status = "changes_requested"
	StatusReady            Status = "ready"
	StatusInReview         Status = "in_review"
	StatusInFlight         Status = "in_flight"
)

// statusInfo holds a bucket's icon, label, and sort priority.
type statusInfo struct {
	Icon     string
	Label    string
	Priority int
}

var statusTable = map[Status]statusInfo{
	StatusReady:            {"✅", "Ready to merge", 0},
	StatusFailingCI:        {"❌", "Failing CI", 1},
	StatusChangesRequested: {"🔧", "Changes requested", 2},
	StatusInReview:         {"👀", "In review", 3},
	StatusInFlight:         {"🚀", "In flight", 4},
	StatusDraft:            {"💤", "Draft", 5},
}

// Icon returns the status's display icon.
func (s Status) Icon() string { return statusTable[s].Icon }

// Label returns the status's human-readable label.
func (s Status) Label() string { return statusTable[s].Label }

// Priority returns the status's sort order — lower sorts first.
func (s Status) Priority() int { return statusTable[s].Priority }

// ClassifyMyPR maps a MyPR's draft/review/CI state into a single display
// bucket, checked in this order:
//  1. Draft — not ready for review, everything else is moot
//  2. Failing CI — checks broke, the most urgent thing to fix regardless
//     of review state
//  3. Changes requested — a reviewer is waiting on you
//  4. Ready to merge — approved and nothing blocking
//  5. In review — reviewers requested, no verdict yet
//  6. In flight — open, nobody's been asked to review yet
func ClassifyMyPR(pr github.MyPR) Status {
	if pr.IsDraft {
		return StatusDraft
	}
	if state := pr.CheckState(); state == "FAILURE" || state == "ERROR" {
		return StatusFailingCI
	}
	switch pr.ReviewDecision {
	case "CHANGES_REQUESTED":
		return StatusChangesRequested
	case "APPROVED":
		return StatusReady
	}
	if pr.HasReviewRequests() {
		return StatusInReview
	}
	return StatusInFlight
}
