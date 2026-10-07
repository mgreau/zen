package github

import (
	"testing"
	"time"

	gh "github.com/google/go-github/v75/github"
)

func TestPRDetailsFrom_deletedForkHead(t *testing.T) {
	pr := &gh.PullRequest{
		Number: gh.Ptr(42),
		Title:  gh.Ptr("fork gone"),
		State:  gh.Ptr("open"),
		Draft:  gh.Ptr(false),
		Head:   nil,
		Base:   nil,
		User:   nil,
	}
	d := prDetailsFrom(pr)
	if d.Number != 42 || d.Title != "fork gone" {
		t.Fatalf("%+v", d)
	}
	if d.HeadSHA != "" || d.HeadRefName != "" || d.Author != "" || d.IsFork {
		t.Fatalf("nil head/user must not panic or invent fields: %+v", d)
	}
}

func TestPRDetailsFrom_nilPR(t *testing.T) {
	d := prDetailsFrom(nil)
	if d == nil || d.Number != 0 {
		t.Fatalf("%+v", d)
	}
}

func TestPRMergeInfoFrom(t *testing.T) {
	mergedAt := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		pr   *gh.PullRequest
		want PRMergeInfo
	}{
		{
			name: "merged",
			pr: &gh.PullRequest{
				State:    gh.Ptr("closed"),
				Merged:   gh.Ptr(true),
				MergedAt: &gh.Timestamp{Time: mergedAt},
				Head:     &gh.PullRequestBranch{SHA: gh.Ptr("abc123")},
			},
			want: PRMergeInfo{State: "MERGED", HeadSHA: "abc123", MergedAt: mergedAt},
		},
		{
			name: "closed without merging",
			pr: &gh.PullRequest{
				State: gh.Ptr("closed"),
				Head:  &gh.PullRequestBranch{SHA: gh.Ptr("def456")},
			},
			want: PRMergeInfo{State: "CLOSED", HeadSHA: "def456"},
		},
		{
			name: "open with deleted fork head",
			pr:   &gh.PullRequest{State: gh.Ptr("open")},
			want: PRMergeInfo{State: "OPEN"},
		},
		{name: "nil", pr: nil, want: PRMergeInfo{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prMergeInfoFrom(tt.pr)
			if got.State != tt.want.State || got.HeadSHA != tt.want.HeadSHA || !got.MergedAt.Equal(tt.want.MergedAt) {
				t.Errorf("prMergeInfoFrom() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}
