package context

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	prCtx := PRContext{
		Number:     42,
		Title:      "Add user authentication",
		Author:     "alice",
		URL:        "https://github.com/org/repo/pull/42",
		HeadBranch: "feature/auth",
		BaseBranch: "main",
		IsFork:     false,
		Body:       "This PR adds JWT-based authentication.\n\nIncludes middleware and tests.",
		ChangedFiles: []string{
			"auth/middleware.go",
			"auth/middleware_test.go",
			"cmd/server.go",
		},
	}

	out, err := Render(prCtx)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	checks := []string{
		"# PR Review: #42",
		"Add user authentication",
		"alice",
		"https://github.com/org/repo/pull/42",
		"`feature/auth`",
		"`main`",
		"JWT-based authentication",
		"`auth/middleware.go`",
		"`auth/middleware_test.go`",
		"`cmd/server.go`",
		"You are reviewing PR #42",
	}

	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}

	// Fork field should NOT appear when IsFork is false
	if strings.Contains(out, "| **Fork**") {
		t.Error("output should not contain fork row when IsFork=false")
	}
}

func TestRender_Fork(t *testing.T) {
	prCtx := PRContext{
		Number:       10,
		Title:        "Fix typo",
		Author:       "bob",
		URL:          "https://github.com/org/repo/pull/10",
		HeadBranch:   "fix-typo",
		BaseBranch:   "main",
		IsFork:       true,
		Body:         "",
		ChangedFiles: []string{"README.md"},
	}

	out, err := Render(prCtx)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if !strings.Contains(out, "| **Fork** | Yes |") {
		t.Error("output should contain fork row when IsFork=true")
	}

	if !strings.Contains(out, "_No description provided._") {
		t.Error("output should show placeholder when body is empty")
	}
}
