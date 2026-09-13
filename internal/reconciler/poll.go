package reconciler

import (
	"strings"

	wt "github.com/mgreau/zen/internal/worktree"
)

// PollAction is what pollOnce should do for one review-request PR.
type PollAction int

const (
	// PollSkip: no worktree to create, HEAD already matches, or the PR is
	// outside the review search (closed, or draft while ignore_drafts).
	PollSkip PollAction = iota
	// PollCreate: no worktree, author is in authors: — create as today.
	PollCreate
	// PollRefresh: worktree exists but HEAD differs from GitHub head SHA.
	// Does not require the author to be in authors: (zen review can create those).
	PollRefresh
)

func (a PollAction) String() string {
	switch a {
	case PollSkip:
		return "skip"
	case PollCreate:
		return "create"
	case PollRefresh:
		return "refresh"
	default:
		return "unknown"
	}
}

// PollInput is everything the daemon needs to decide create / refresh / skip
// for one PR. InReviewSearch is false when GitHub's review search no longer
// returns the PR (closed, or draft with ignore_drafts: true).
type PollInput struct {
	InReviewSearch bool
	IgnoreDrafts   bool
	IsDraft        bool
	Closed         bool
	WorktreeExists bool
	AuthorInList   bool
	WorktreeHEAD   string
	HeadOID        string
}

// ShouldSyncWorktree is whether the daemon may fetch this PR.
// Closed PRs are left alone (cleanup handles merged). Drafts are left alone
// when ignore_drafts is on — including a race where the PR was queued while
// ready and converted to draft before reconcile. Rewritten history is fetched
// but not reset --hard (CLI prompts).
func ShouldSyncWorktree(ignoreDrafts, isDraft, closed bool) bool {
	if closed {
		return false
	}
	if ignoreDrafts && isDraft {
		return false
	}
	return true
}

// PRStateClosed reports whether a REST PR state string is closed or merged.
func PRStateClosed(state string) bool {
	s := strings.ToUpper(strings.TrimSpace(state))
	return s == "CLOSED" || s == "MERGED"
}

// DecidePoll chooses create / refresh / skip for a PR that is still in the
// review search. Prefer DecideDaemon when closed/draft visibility is known.
func DecidePoll(worktreeExists, authorInList bool, worktreeHEAD, headOID string) PollAction {
	return DecideDaemon(PollInput{
		InReviewSearch: true,
		WorktreeExists: worktreeExists,
		AuthorInList:   authorInList,
		WorktreeHEAD:   worktreeHEAD,
		HeadOID:        headOID,
	})
}

// DecideDaemon is the full daemon policy, including PRs that dropped out of
// the review search. Those are always skip: the worktree is not deleted
// (cleanup is merge-only) and not updated.
func DecideDaemon(in PollInput) PollAction {
	if !in.InReviewSearch || !ShouldSyncWorktree(in.IgnoreDrafts, in.IsDraft, in.Closed) {
		return PollSkip
	}
	if !in.WorktreeExists {
		if in.AuthorInList {
			return PollCreate
		}
		return PollSkip
	}
	if in.HeadOID == "" {
		return PollSkip
	}
	if wt.SHAEqual(in.WorktreeHEAD, in.HeadOID) {
		return PollSkip
	}
	return PollRefresh
}

// ShouldNotifyNew is whether this poll should fire the "new review request"
// banner. authors: only gates auto-create; a first sighting with no local
// worktree still notifies. Existing worktrees are not re-announced (SHA
// refresh uses PRUpdated after a successful move).
func ShouldNotifyNew(alreadyNotified, worktreeExists bool) bool {
	return !alreadyNotified && !worktreeExists
}
