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
