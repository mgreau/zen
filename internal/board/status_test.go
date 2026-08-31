package board

import (
	"testing"

	"github.com/mgreau/zen/internal/github"
)

func withCheckState(pr github.MyPR, state string) github.MyPR {
	pr.Commits.Nodes = []struct {
		Commit struct {
			StatusCheckRollup struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}{{}}
	pr.Commits.Nodes[0].Commit.StatusCheckRollup.State = state
	return pr
}

func TestClassifyMyPR(t *testing.T) {
	tests := []struct {
		name string
		pr   github.MyPR
		want Status
	}{
		{
			name: "draft wins over everything else",
			pr: withCheckState(github.MyPR{
				IsDraft:        true,
				ReviewDecision: "APPROVED",
			}, "FAILURE"),
			want: StatusDraft,
		},
		{
			name: "failing CI wins over approved",
			pr:   withCheckState(github.MyPR{ReviewDecision: "APPROVED"}, "FAILURE"),
			want: StatusFailingCI,
		},
		{
			name: "error check state counts as failing",
			pr:   withCheckState(github.MyPR{}, "ERROR"),
			want: StatusFailingCI,
		},
		{
			name: "changes requested",
			pr:   github.MyPR{ReviewDecision: "CHANGES_REQUESTED"},
			want: StatusChangesRequested,
		},
		{
			name: "approved and passing is ready to merge",
			pr:   withCheckState(github.MyPR{ReviewDecision: "APPROVED"}, "SUCCESS"),
			want: StatusReady,
		},
		{
			name: "review requested with no decision yet",
			pr: func() github.MyPR {
				pr := github.MyPR{}
				pr.ReviewRequests.TotalCount = 1
				return pr
			}(),
			want: StatusInReview,
		},
		{
			name: "open with no reviewers requested",
			pr:   github.MyPR{},
			want: StatusInFlight,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyMyPR(tt.pr); got != tt.want {
				t.Errorf("ClassifyMyPR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatusPriorityOrdering(t *testing.T) {
	// Every bucket must have a distinct priority so sorting is well-defined.
	seen := make(map[int]Status)
	for _, s := range []Status{
		StatusDraft, StatusFailingCI, StatusChangesRequested,
		StatusReady, StatusInReview, StatusInFlight,
	} {
		if s.Icon() == "" || s.Label() == "" {
			t.Errorf("status %v missing icon or label", s)
		}
		if other, ok := seen[s.Priority()]; ok {
			t.Errorf("priority %d used by both %v and %v", s.Priority(), s, other)
		}
		seen[s.Priority()] = s
	}
}
