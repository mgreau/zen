package github

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWithTimeout_addsDeadlineWhenNone(t *testing.T) {
	ctx, cancel := withTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > apiTimeout {
		t.Fatalf("expected deadline within %s, got %s remaining", apiTimeout, remaining)
	}
}

func TestWithTimeout_preservesExistingDeadline(t *testing.T) {
	existing := time.Now().Add(5 * time.Second)
	parent, parentCancel := context.WithDeadline(context.Background(), existing)
	defer parentCancel()

	ctx, cancel := withTimeout(parent)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	if !deadline.Equal(existing) {
		t.Fatalf("expected existing deadline %v, got %v", existing, deadline)
	}
}

func TestGetCurrentUser_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := GetCurrentUser(ctx)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestGetReviewRequests_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := GetReviewRequests(ctx, "", false)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestGetApprovedUnmerged_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := GetApprovedUnmerged(ctx, "", false)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestGetMyOpenPRs_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := GetMyOpenPRs(ctx, "")
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestBuildMyOpenPRsQuery(t *testing.T) {
	tests := []struct {
		name       string
		repoFilter string
		want       string
	}{
		{
			name: "no repo",
			want: "is:pr is:open author:@me",
		},
		{
			name:       "with repo",
			repoFilter: "owner/repo",
			want:       "is:pr is:open author:@me repo:owner/repo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildMyOpenPRsQuery(tt.repoFilter)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMyPR_CheckState(t *testing.T) {
	var noCommits MyPR
	if got := noCommits.CheckState(); got != "" {
		t.Errorf("CheckState() with no commits = %q, want empty", got)
	}

	var withCommit MyPR
	withCommit.Commits.Nodes = []struct {
		Commit struct {
			StatusCheckRollup struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}{{}}
	withCommit.Commits.Nodes[0].Commit.StatusCheckRollup.State = "FAILURE"
	if got := withCommit.CheckState(); got != "FAILURE" {
		t.Errorf("CheckState() = %q, want FAILURE", got)
	}
}

func TestMyPR_HasReviewRequests(t *testing.T) {
	var none MyPR
	if none.HasReviewRequests() {
		t.Error("HasReviewRequests() = true, want false for zero total count")
	}

	var some MyPR
	some.ReviewRequests.TotalCount = 2
	if !some.HasReviewRequests() {
		t.Error("HasReviewRequests() = false, want true for non-zero total count")
	}
}

func TestListOpenPRs_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := ListOpenPRs(ctx, "owner/repo", 10, false)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestBuildReviewRequestQueries(t *testing.T) {
	tests := []struct {
		name         string
		repoFilter   string
		ignoreDrafts bool
		wantQ1       string
		wantQ2       string
	}{
		{
			name:   "no repo, drafts allowed",
			wantQ1: "is:pr is:open review-requested:@me",
			wantQ2: "is:pr is:open reviewed-by:@me review:required",
		},
		{
			name:         "no repo, drafts excluded",
			ignoreDrafts: true,
			wantQ1:       "is:pr is:open review-requested:@me draft:false",
			wantQ2:       "is:pr is:open reviewed-by:@me review:required draft:false",
		},
		{
			name:         "repo + drafts excluded",
			repoFilter:   "owner/repo",
			ignoreDrafts: true,
			wantQ1:       "is:pr is:open review-requested:@me repo:owner/repo draft:false",
			wantQ2:       "is:pr is:open reviewed-by:@me review:required repo:owner/repo draft:false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotQ1, gotQ2 := buildReviewRequestQueries(tt.repoFilter, tt.ignoreDrafts)
			if gotQ1 != tt.wantQ1 {
				t.Errorf("q1 = %q, want %q", gotQ1, tt.wantQ1)
			}
			if gotQ2 != tt.wantQ2 {
				t.Errorf("q2 = %q, want %q", gotQ2, tt.wantQ2)
			}
		})
	}
}

func TestMergeReviewRequests(t *testing.T) {
	requested := []ReviewRequest{{Number: 1}, {Number: 2}}
	rereview := []ReviewRequest{{Number: 2}, {Number: 3}}

	got := mergeReviewRequests(requested, rereview)

	want := map[int]string{1: ReviewKindNew, 2: ReviewKindNew, 3: ReviewKindRereview}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(got), len(want), got)
	}
	for _, rr := range got {
		if rr.Kind != want[rr.Number] {
			t.Errorf("PR #%d: Kind = %q, want %q", rr.Number, rr.Kind, want[rr.Number])
		}
	}
}

func TestBuildApprovedUnmergedQuery(t *testing.T) {
	tests := []struct {
		name         string
		repoFilter   string
		ignoreDrafts bool
		want         string
	}{
		{
			name: "no repo, drafts allowed",
			want: "is:pr is:open author:@me review:approved",
		},
		{
			name:         "drafts excluded",
			ignoreDrafts: true,
			want:         "is:pr is:open author:@me review:approved draft:false",
		},
		{
			name:         "repo + drafts excluded",
			repoFilter:   "owner/repo",
			ignoreDrafts: true,
			want:         "is:pr is:open author:@me review:approved repo:owner/repo draft:false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildApprovedUnmergedQuery(tt.repoFilter, tt.ignoreDrafts)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
