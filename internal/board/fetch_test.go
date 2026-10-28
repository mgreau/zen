package board

import (
	"context"
	"testing"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/github"
)

func TestTouchesWatchPaths(t *testing.T) {
	tests := []struct {
		name       string
		files      []string
		watchPaths []string
		want       bool
	}{
		{"file under watched dir", []string{"driftlessaf/foo.go"}, []string{"driftlessaf"}, true},
		{"file is exactly the watched name", []string{"driftlessaf"}, []string{"driftlessaf"}, true},
		{"no match", []string{"other/foo.go"}, []string{"driftlessaf"}, false},
		{"one of several files matches", []string{"a/b.go", "driftlessaf/c.go"}, []string{"driftlessaf"}, true},
		{"one of several watch paths matches", []string{"agents/x.go"}, []string{"driftlessaf", "agents"}, true},
		{"no watch paths configured", []string{"driftlessaf/foo.go"}, nil, false},
		{"no files", nil, []string{"driftlessaf"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := touchesWatchPaths(tt.files, tt.watchPaths); got != tt.want {
				t.Errorf("touchesWatchPaths(%v, %v) = %v, want %v", tt.files, tt.watchPaths, got, tt.want)
			}
		})
	}
}

func TestAssignReviewTiers_authorTierNeedsNoNetwork(t *testing.T) {
	// No watch_paths configured, so non-author rows must resolve to
	// reviewTierOther without ever needing a GitHub client.
	cfg := &config.Config{Authors: []string{"alice"}}
	rows := []ReviewRow{
		{Author: "alice", Number: 1},
		{Author: "bob", Number: 2},
	}

	assignReviewTiers(context.Background(), cfg, rows)

	if rows[0].tier != reviewTierAuthor {
		t.Errorf("alice's PR tier = %d, want reviewTierAuthor", rows[0].tier)
	}
	if rows[1].tier != reviewTierOther {
		t.Errorf("bob's PR tier = %d, want reviewTierOther", rows[1].tier)
	}
}

func TestAssignReviewTiers_emptyRows(t *testing.T) {
	cfg := &config.Config{Authors: []string{"alice"}, WatchPaths: []string{"driftlessaf"}}
	// Must not panic or hang when there's nothing to tier.
	assignReviewTiers(context.Background(), cfg, nil)
}

func TestMyPRRows_labelsConfiguredReposByShortName(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"workstation": {FullName: "imkarrer/flox-workstation"},
	}}
	prs := []github.MyPR{
		{Number: 1, Repository: github.RepoInfo{NameWithOwner: "imkarrer/flox-workstation"}},
		{Number: 2, Repository: github.RepoInfo{NameWithOwner: "agent-stacks/agent-pkgs"}},
	}

	rows := myPRRows(cfg, prs)

	if got := rows[0].Repo; got != "workstation" {
		t.Errorf("configured repo labelled %q, want its short name %q", got, "workstation")
	}
	if got := rows[1].Repo; got != "agent-stacks/agent-pkgs" {
		t.Errorf("unconfigured repo labelled %q, want its owner/repo", got)
	}
	// The summary panel resolves the label back through RepoFullName, so an
	// unconfigured PR must still reach the right repository.
	if got := cfg.RepoFullName(rows[1].Repo); got != "agent-stacks/agent-pkgs" {
		t.Errorf("RepoFullName(%q) = %q, want agent-stacks/agent-pkgs", rows[1].Repo, got)
	}
}

func TestMyPRRows_sameRepoNameUnderTwoOwnersStaysDistinct(t *testing.T) {
	// Stacked-PR detection keys on Repo+HeadRef, so two unconfigured repos
	// that share a name must not collapse into one.
	prs := []github.MyPR{
		{Number: 1, HeadRefName: "a", Repository: github.RepoInfo{NameWithOwner: "flox/zen"}},
		{Number: 2, BaseRefName: "a", Repository: github.RepoInfo{NameWithOwner: "mgreau/zen"}},
	}

	rows := orderWithDependencies(myPRRows(&config.Config{}, prs))

	for _, r := range rows {
		if r.Depth != 0 {
			t.Errorf("PR #%d stacked at depth %d across different repos", r.Number, r.Depth)
		}
	}
}
