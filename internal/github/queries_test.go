package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

func TestIsNotFound(t *testing.T) {
	if IsNotFound(nil) {
		t.Fatal("nil")
	}
	if !IsNotFound(fmt.Errorf("fetching PR #1: GET https://api.github.com/repos/o/r/pulls/1: 404 Not Found []")) {
		t.Fatal("expected 404 to match")
	}
	if !IsNotFound(fmt.Errorf("GET https://api.github.com/repos/o/r/pulls/1: 404 Not Found []")) {
		t.Fatal("bare 404")
	}
	if IsNotFound(fmt.Errorf("git fetch timed out: context deadline exceeded")) {
		t.Fatal("timeout is not a 404")
	}
}

func TestReviewRequest_graphQLOmitsOptionalFields(t *testing.T) {
	var pr ReviewRequest
	if err := json.Unmarshal([]byte(`{"number":12,"title":"n","author":{"login":"a"},"repository":{"name":"r","nameWithOwner":"o/r"}}`), &pr); err != nil {
		t.Fatal(err)
	}
	if pr.HeadRefOid != "" || pr.IsDraft || pr.Closed {
		t.Fatalf("omitted fields must be zero: %+v", pr)
	}
}

func TestReviewRequest_graphQLNullHeadAndGhostAuthor(t *testing.T) {
	raw := `{
		"number": 3,
		"title": "from a deleted user",
		"author": null,
		"repository": {"name": "r", "nameWithOwner": "o/r"},
		"headRefOid": null,
		"isDraft": true,
		"closed": false
	}`
	var pr ReviewRequest
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		t.Fatal(err)
	}
	if pr.HeadRefOid != "" {
		t.Fatalf("null headRefOid = %q", pr.HeadRefOid)
	}
	if pr.Author.Login != "" {
		t.Fatalf("null author login = %q", pr.Author.Login)
	}
	if !pr.IsDraft {
		t.Fatal("isDraft")
	}
}

func TestMergeReviewRequests_dedupByRepoAndNumber(t *testing.T) {
	a := ReviewRequest{Number: 1, Title: "one", Repository: RepoInfo{NameWithOwner: "org/foo"}}
	b := ReviewRequest{Number: 1, Title: "dup", Repository: RepoInfo{NameWithOwner: "org/foo"}}
	c := ReviewRequest{Number: 1, Title: "other-repo", Repository: RepoInfo{NameWithOwner: "org/bar"}}
	got := mergeReviewRequests([]ReviewRequest{a}, []ReviewRequest{b, c})
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Repository.NameWithOwner != "org/foo" || got[1].Repository.NameWithOwner != "org/bar" {
		t.Fatalf("unexpected merge: %+v", got)
	}
}

// fakeSearch stands in for GitHub's PR search: it honours a repo: clause and
// paginates at searchPageSize, so a test can build a result set larger than one
// page and see what each query actually gets back.
type fakeSearch struct {
	corpus  []ReviewRequest
	queries []string
	afters  []string
}

// repoClauseOf extracts the owner/repo from a "repo:owner/name" clause.
func repoClauseOf(q string) string {
	for _, field := range strings.Fields(q) {
		if after, ok := strings.CutPrefix(field, "repo:"); ok {
			return after
		}
	}
	return ""
}

func (f *fakeSearch) run(_ context.Context, args ...string) ([]byte, error) {
	var q, after string
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "q="); ok {
			q = v
		}
		if v, ok := strings.CutPrefix(a, "after="); ok {
			after = v
		}
	}
	f.queries = append(f.queries, q)
	f.afters = append(f.afters, after)

	var matched []ReviewRequest
	// Only the requested-reviews query matches in this fake; the re-review
	// query returns an empty page, as it would for a user with no re-reviews.
	if strings.Contains(q, "review-requested:@me") {
		repo := repoClauseOf(q)
		for _, rr := range f.corpus {
			if repo == "" || rr.Repository.NameWithOwner == repo {
				matched = append(matched, rr)
			}
		}
	}

	start := 0
	if after != "" {
		var err error
		start, err = strconv.Atoi(after)
		if err != nil {
			return nil, fmt.Errorf("fake: bad cursor %q", after)
		}
	}
	end := min(start+searchPageSize, len(matched))
	page := matched[start:end]

	var resp struct {
		Data struct {
			Search struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []ReviewRequest `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	resp.Data.Search.Nodes = page
	resp.Data.Search.PageInfo.HasNextPage = end < len(matched)
	resp.Data.Search.PageInfo.EndCursor = strconv.Itoa(end)
	return json.Marshal(resp)
}

func useFakeSearch(t *testing.T, f *fakeSearch) {
	t.Helper()
	old := runGH
	runGH = f.run
	t.Cleanup(func() { runGH = old })
}

// corpus builds n review requests in repo, numbered from start.
func corpus(repo string, start, n int) []ReviewRequest {
	var out []ReviewRequest
	for i := range n {
		out = append(out, ReviewRequest{
			Number:     start + i,
			Title:      fmt.Sprintf("PR %d", start+i),
			Repository: RepoInfo{Name: strings.SplitN(repo, "/", 2)[1], NameWithOwner: repo},
		})
	}
	return out
}

func TestSearchReviewRequests_followsPagination(t *testing.T) {
	// 120 results is three pages; a single first:50 query would return 50.
	f := &fakeSearch{corpus: corpus("owner/mono", 1, 120)}
	useFakeSearch(t, f)

	got, err := searchReviewRequests(context.Background(), "is:pr is:open review-requested:@me")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 120 {
		t.Fatalf("got %d results, want 120 (pagination stopped early)", len(got))
	}
	if got[119].Number != 120 {
		t.Fatalf("last result = #%d, want #120", got[119].Number)
	}
	if len(f.afters) != 3 {
		t.Fatalf("made %d requests, want 3 pages", len(f.afters))
	}
	if f.afters[0] != "" || f.afters[1] != "50" || f.afters[2] != "100" {
		t.Fatalf("cursors = %q, want [\"\", \"50\", \"100\"]", f.afters)
	}
}

func TestGetReviewRequests_moreThanOnePageOfResults(t *testing.T) {
	f := &fakeSearch{corpus: corpus("owner/mono", 1, 87)}
	useFakeSearch(t, f)

	got, err := GetReviewRequests(context.Background(), "owner/mono", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 87 {
		t.Fatalf("got %d results, want 87", len(got))
	}
	for _, rr := range got {
		if rr.Kind != ReviewKindNew {
			t.Fatalf("PR #%d kind = %q, want %q", rr.Number, rr.Kind, ReviewKindNew)
		}
	}
}

func TestGetReviewRequestsForRepos_configuredPRSurvivesUnconfiguredVolume(t *testing.T) {
	// The regression: 60 review requests in repos zen knows nothing about,
	// sorted ahead of the configured repo's PR. A global first:50 search never
	// returns #999, so the daemon neither creates nor refreshes its worktree.
	all := corpus("other/noise", 1, 60)
	all = append(all, corpus("owner/mono", 999, 1)...)
	f := &fakeSearch{corpus: all}
	useFakeSearch(t, f)

	got, err := GetReviewRequestsForRepos(context.Background(), []string{"owner/mono"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 999 {
		t.Fatalf("got %d results (%v), want just PR #999", len(got), got)
	}
	for _, q := range f.queries {
		if !strings.Contains(q, "repo:owner/mono") {
			t.Fatalf("query %q is not scoped to the configured repo", q)
		}
	}
}

func TestGetReviewRequestsForRepos_queriesEveryRepo(t *testing.T) {
	all := corpus("owner/mono", 1, 2)
	all = append(all, corpus("owner/tools", 1, 2)...) // same PR numbers, other repo
	f := &fakeSearch{corpus: all}
	useFakeSearch(t, f)

	got, err := GetReviewRequestsForRepos(context.Background(), []string{"owner/mono", "owner/tools"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d results, want 4 (PR numbers repeat across repos)", len(got))
	}
	seen := map[string]bool{}
	for _, rr := range got {
		seen[fmt.Sprintf("%s#%d", rr.Repository.NameWithOwner, rr.Number)] = true
	}
	for _, want := range []string{"owner/mono#1", "owner/mono#2", "owner/tools#1", "owner/tools#2"} {
		if !seen[want] {
			t.Fatalf("missing %s from %v", want, seen)
		}
	}
}

func TestGetReviewRequestsForRepos_partialFailureKeepsOtherRepos(t *testing.T) {
	f := &fakeSearch{corpus: corpus("owner/mono", 1, 1)}
	useFakeSearch(t, f)
	good := runGH
	runGH = func(ctx context.Context, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.Contains(a, "repo:owner/broken") {
				return nil, errors.New("gh: repository not found")
			}
		}
		return good(ctx, args...)
	}

	got, err := GetReviewRequestsForRepos(context.Background(), []string{"owner/broken", "owner/mono"}, false)
	if err == nil {
		t.Fatal("expected the failing repo to be reported")
	}
	if !strings.Contains(err.Error(), "owner/broken") {
		t.Fatalf("error does not name the failing repo: %v", err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("got %v, want the healthy repo's PR despite the failure", got)
	}
}

func TestGetReviewRequestsForRepos_noRepos(t *testing.T) {
	f := &fakeSearch{corpus: corpus("owner/mono", 1, 5)}
	useFakeSearch(t, f)

	got, err := GetReviewRequestsForRepos(context.Background(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d results for no configured repos", len(got))
	}
	if len(f.queries) != 0 {
		t.Fatalf("queried GitHub with no configured repos: %v", f.queries)
	}
}
