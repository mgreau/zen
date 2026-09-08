package github

import (
	"testing"

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
