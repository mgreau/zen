package board

import (
	"context"
	"errors"
	"strings"
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

func useGetMyOpenPRs(t *testing.T, prs []github.MyPR, err error) {
	t.Helper()
	old := getMyOpenPRs
	getMyOpenPRs = func(context.Context, string) ([]github.MyPR, error) { return prs, err }
	t.Cleanup(func() { getMyOpenPRs = old })
}

func TestFetchMyPRs_partialResultKeepsRows(t *testing.T) {
	useGetMyOpenPRs(t,
		[]github.MyPR{{Number: 7, Repository: github.RepoInfo{NameWithOwner: "me/tools"}}},
		&github.PartialError{Errors: []github.GraphQLError{{Type: "FORBIDDEN", Message: "SAML enforcement"}}},
	)

	rows, err := FetchMyPRs(context.Background(), &config.Config{})

	var partial *github.PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v, want it to wrap *github.PartialError", err)
	}
	if len(rows) != 1 || rows[0].Number != 7 {
		t.Fatalf("rows = %+v, want PR #7 despite the partial error", rows)
	}
}

func TestFetchMyPRs_totalFailureReturnsNoRows(t *testing.T) {
	useGetMyOpenPRs(t, nil, errors.New("GraphQL query failed: HTTP 401"))

	rows, err := FetchMyPRs(context.Background(), &config.Config{})

	if err == nil || rows != nil {
		t.Fatalf("got %v, %v; want nil rows and an error", rows, err)
	}
}

func TestBoard_partialResultShowsRowsAndWarning(t *testing.T) {
	partial := &github.PartialError{Errors: []github.GraphQLError{{Message: "Resource protected by organization SAML enforcement."}}}
	rows := []MyPRRow{{Number: 7, Repo: "me/tools", Title: "mine"}}

	msg := newDataMsg(rows, wrapFetchErr(partial), nil, nil)
	if msg.err != nil {
		t.Fatalf("partial result reported as failure: %v", msg.err)
	}
	updated, _ := NewModel(&config.Config{}).Update(msg)
	m := updated.(Model)

	if len(m.myRows) != 1 {
		t.Fatalf("board shows %d rows, want the 1 that came back", len(m.myRows))
	}
	view := m.View()
	if !strings.Contains(view, "Warning:") || !strings.Contains(view, "SAML") {
		t.Errorf("view does not surface the partial-result warning:\n%s", view)
	}
	if strings.Contains(view, "Error:") {
		t.Errorf("partial result rendered as an error:\n%s", view)
	}
}

func TestBoard_totalFailureShowsError(t *testing.T) {
	msg := newDataMsg(nil, errors.New("fetching your PRs: GraphQL query failed: HTTP 401"), nil, nil)
	if msg.err == nil || msg.warning != nil {
		t.Fatalf("total failure: err = %v, warning = %v; want an error and no warning", msg.err, msg.warning)
	}
	updated, _ := NewModel(&config.Config{}).Update(msg)
	if view := updated.(Model).View(); !strings.Contains(view, "Error:") {
		t.Errorf("view does not report the failure:\n%s", view)
	}
}

// wrapFetchErr wraps err the way FetchMyPRs does.
func wrapFetchErr(err error) error { return errors.Join(errors.New("fetching your PRs"), err) }
