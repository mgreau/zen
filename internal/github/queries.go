package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// withTimeout returns a context with apiTimeout applied, unless the caller
// already set a deadline.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, apiTimeout)
}

// runGH executes gh and returns its stdout. A package var so tests can drive
// the query builders and pagination without a network or a gh binary.
var runGH = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "gh", args...).Output()
}

// ghError extracts stderr from an exec.ExitError for better error messages.
func ghError(err error) string {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return err.Error()
}

// ReviewRequest represents a PR review request.
type ReviewRequest struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Author     AuthorInfo `json:"author"`
	Repository RepoInfo   `json:"repository"`
	CreatedAt  string     `json:"createdAt"`
	URL        string     `json:"url"`
	HeadRefOid string     `json:"headRefOid"`
	IsDraft    bool       `json:"isDraft"`
	Closed     bool       `json:"closed"`
	// Kind is "new" or "rereview", set by GetReviewRequests to record which
	// of its two queries matched. Not part of the GitHub API response.
	Kind string `json:"-"`
}

// Review request kinds, set on ReviewRequest.Kind by GetReviewRequests.
const (
	ReviewKindNew      = "new"
	ReviewKindRereview = "rereview"
)

// AuthorInfo holds author login info.
type AuthorInfo struct {
	Login string `json:"login"`
}

// RepoInfo holds repository identification.
type RepoInfo struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"nameWithOwner"`
}

// ApprovedPR represents a user's approved but unmerged PR.
type ApprovedPR struct {
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	Author         AuthorInfo `json:"author"`
	Repository     RepoInfo   `json:"repository"`
	CreatedAt      string     `json:"createdAt"`
	URL            string     `json:"url"`
	ReviewDecision string     `json:"reviewDecision"`
}

// MyPR represents one of the current user's open pull requests, enriched
// with review and CI status for `zen board`'s status classification.
type MyPR struct {
	Number         int      `json:"number"`
	Title          string   `json:"title"`
	Repository     RepoInfo `json:"repository"`
	CreatedAt      string   `json:"createdAt"`
	URL            string   `json:"url"`
	IsDraft        bool     `json:"isDraft"`
	ReviewDecision string   `json:"reviewDecision"`
	Mergeable      string   `json:"mergeable"`
	// BaseRefName and HeadRefName let callers detect stacked PRs: PR A
	// depends on PR B when A's BaseRefName equals B's HeadRefName.
	BaseRefName    string `json:"baseRefName"`
	HeadRefName    string `json:"headRefName"`
	ReviewRequests struct {
		TotalCount int `json:"totalCount"`
	} `json:"reviewRequests"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// CheckState returns the latest commit's CI status-check rollup state
// (e.g. "SUCCESS", "FAILURE", "PENDING"), or "" if no checks are reported.
func (p MyPR) CheckState() string {
	if len(p.Commits.Nodes) == 0 {
		return ""
	}
	return p.Commits.Nodes[0].Commit.StatusCheckRollup.State
}

// HasReviewRequests reports whether any reviewers are currently requested.
func (p MyPR) HasReviewRequests() bool {
	return p.ReviewRequests.TotalCount > 0
}

// GetCurrentUser returns the authenticated GitHub user's login.
func GetCurrentUser(ctx context.Context) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("fetching current user timed out after %s", apiTimeout)
		}
		return "", fmt.Errorf("fetching current user: %s", ghError(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// mergeReviewRequests merges and deduplicates the results of the two
// GetReviewRequests queries, tagging each with which one matched. Dedup is
// by owner/repo + number because PR numbers are not unique across
// repositories. A PR appearing in both queries keeps the "new" tag, since
// requested is merged first.
func mergeReviewRequests(requested, rereview []ReviewRequest) []ReviewRequest {
	seen := make(map[string]bool)
	var merged []ReviewRequest
	kinds := []string{ReviewKindNew, ReviewKindRereview}
	for i, lists := range [][]ReviewRequest{requested, rereview} {
		for _, rr := range lists {
			rr.Kind = kinds[i]
			merged = appendUniqueReviewRequest(merged, seen, rr)
		}
	}
	return merged
}

// appendUniqueReviewRequest adds rr unless its repository and number were
// already seen. PR numbers repeat across repositories, so the key needs both.
func appendUniqueReviewRequest(dst []ReviewRequest, seen map[string]bool, rr ReviewRequest) []ReviewRequest {
	if rr.Number == 0 {
		return dst
	}
	key := fmt.Sprintf("%s#%d", rr.Repository.NameWithOwner, rr.Number)
	if seen[key] {
		return dst
	}
	seen[key] = true
	return append(dst, rr)
}

// buildReviewRequestQueries returns the two GitHub search query strings used
// by GetReviewRequests: requested-reviews and re-review queries.
func buildReviewRequestQueries(repoFilter string, ignoreDrafts bool) (string, string) {
	repoClause := ""
	if repoFilter != "" {
		repoClause = " repo:" + repoFilter
	}
	draftClause := ""
	if ignoreDrafts {
		draftClause = " draft:false"
	}
	q1 := fmt.Sprintf("is:pr is:open review-requested:@me%s%s", repoClause, draftClause)
	q2 := fmt.Sprintf("is:pr is:open reviewed-by:@me review:required%s%s", repoClause, draftClause)
	return q1, q2
}

// buildApprovedUnmergedQuery returns the GitHub search query string for
// GetApprovedUnmerged.
func buildApprovedUnmergedQuery(repoFilter string, ignoreDrafts bool) string {
	repoClause := ""
	if repoFilter != "" {
		repoClause = " repo:" + repoFilter
	}
	draftClause := ""
	if ignoreDrafts {
		draftClause = " draft:false"
	}
	return fmt.Sprintf("is:pr is:open author:@me review:approved%s%s", repoClause, draftClause)
}

// searchPageSize is the page size for PR searches. GitHub caps search
// connections at 100; 50 keeps each response small.
const searchPageSize = 50

// maxSearchPages bounds a paginated search so a pathological result set cannot
// stall a poll forever. 20 pages is 1000 PRs.
const maxSearchPages = 20

// reviewRequestSearch is one page of a PR search. `first` is inlined because
// gh sends -f values as strings and search(first:) needs an Int; `after` is a
// nullable String, so omitting it on the first page is a valid query.
const reviewRequestSearch = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 50, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        number
        title
        author { login }
        repository { name nameWithOwner }
        createdAt
        url
        headRefOid
        isDraft
        closed
      }
    }
  }
}`

// searchReviewRequests runs one search query, following pagination to the end.
// Without this a 50-result page silently truncates: review requests in other
// repositories occupy slots and a configured repository's PR disappears from
// the poll, so it is neither set up nor refreshed.
//
// Any GraphQL error fails the whole search, partial or not: the daemon acts on
// these results, and acting on an incomplete set is worse than skipping a poll.
func searchReviewRequests(ctx context.Context, query string) ([]ReviewRequest, error) {
	all, err := searchAll[ReviewRequest](ctx, reviewRequestSearch, query, "review requests")
	if err != nil {
		return nil, err
	}
	return all, nil
}

// GraphQLError is one entry in a GraphQL response's top-level errors array.
type GraphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// PartialError reports that GitHub answered a GraphQL query with data and
// errors together, for example FORBIDDEN on results in an organization that
// enforces SAML SSO the gh token has not been authorized for. The results
// returned alongside a PartialError are real and complete except for whatever
// the errors describe.
type PartialError struct {
	Errors []GraphQLError
}

func (e *PartialError) Error() string {
	seen := make(map[string]bool)
	var msgs []string
	for _, ge := range e.Errors {
		if ge.Message != "" && !seen[ge.Message] {
			seen[ge.Message] = true
			msgs = append(msgs, ge.Message)
		}
	}
	if len(msgs) == 0 {
		return "GitHub returned partial results"
	}
	return "GitHub returned partial results: " + strings.Join(msgs, "; ")
}

// searchAll runs a paginated GraphQL search, following pageInfo to the end
// (bounded by maxSearchPages). gqlQuery must take $q and $after and select
// search { pageInfo { hasNextPage endCursor } nodes { ... } }. what names the
// search in error messages.
//
// `gh api graphql` exits non-zero whenever the response has an errors array,
// but still prints the response, data included, on stdout. So a non-zero exit
// whose stdout carries search data is a partial result: searchAll keeps the
// nodes, keeps paginating, and returns everything it got with a *PartialError.
// A response with no search data at all is a hard failure and returns nil.
func searchAll[T any](ctx context.Context, gqlQuery, q, what string) ([]T, error) {
	var all []T
	var partial []GraphQLError
	cursor := ""
	for page := 0; page < maxSearchPages; page++ {
		args := []string{"api", "graphql", "-f", "query=" + gqlQuery, "-f", "q=" + q}
		if cursor != "" {
			args = append(args, "-f", "after="+cursor)
		}
		out, runErr := runGH(ctx, args...)
		if runErr != nil && ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s query timed out after %s", what, apiTimeout)
		}

		var result struct {
			Data *struct {
				Search *struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []T `json:"nodes"`
				} `json:"search"`
			} `json:"data"`
			Errors []GraphQLError `json:"errors"`
		}
		parseErr := json.Unmarshal(out, &result)
		hasData := parseErr == nil && result.Data != nil && result.Data.Search != nil

		switch {
		case runErr != nil && !(hasData && len(result.Errors) > 0):
			return nil, fmt.Errorf("GraphQL query failed: %s", ghError(runErr))
		case parseErr != nil:
			return nil, fmt.Errorf("parsing GraphQL response: %w", parseErr)
		case !hasData:
			return nil, fmt.Errorf("GraphQL response for %s has no search data", what)
		}
		partial = append(partial, result.Errors...)

		search := result.Data.Search
		all = append(all, search.Nodes...)
		info := search.PageInfo
		if !info.HasNextPage || info.EndCursor == "" || info.EndCursor == cursor {
			break
		}
		cursor = info.EndCursor
	}
	if len(partial) > 0 {
		return all, &PartialError{Errors: partial}
	}
	return all, nil
}

// GetReviewRequests fetches PRs where the user is a requested reviewer,
// including re-reviews. Uses GraphQL via `gh api graphql`. When ignoreDrafts
// is true, draft PRs are filtered out at the GitHub search layer. An empty
// repoFilter searches every repository the user can see, which is only safe
// for callers that show what they get; the daemon uses
// GetReviewRequestsForRepos instead.
func GetReviewRequests(ctx context.Context, repoFilter string, ignoreDrafts bool) ([]ReviewRequest, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	q1, q2 := buildReviewRequestQueries(repoFilter, ignoreDrafts)
	requested, err := searchReviewRequests(ctx, q1)
	if err != nil {
		return nil, err
	}
	rereview, err := searchReviewRequests(ctx, q2)
	if err != nil {
		return nil, err
	}
	return mergeReviewRequests(requested, rereview), nil
}

// GetReviewRequestsForRepos searches each configured repository separately and
// merges the results. One global search cannot serve the daemon: its page is
// filled on GitHub's terms, so requests in unconfigured repositories can crowd
// out a configured repository's PR. Scoping per repository also keeps every
// query well under GitHub's 256-character search limit, which a combined
// `repo:a repo:b ...` query would eventually cross.
//
// A repository that fails is reported but does not hide the others: the
// returned slice holds every repository that answered, alongside the error.
func GetReviewRequestsForRepos(ctx context.Context, repoFullNames []string, ignoreDrafts bool) ([]ReviewRequest, error) {
	seen := make(map[string]bool)
	var all []ReviewRequest
	var errs []error
	for _, full := range repoFullNames {
		if full == "" {
			continue
		}
		reqs, err := GetReviewRequests(ctx, full, ignoreDrafts)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", full, err))
			continue
		}
		for _, rr := range reqs {
			all = appendUniqueReviewRequest(all, seen, rr)
		}
	}
	return all, errors.Join(errs...)
}

// GetApprovedUnmerged fetches the user's own PRs that are approved but not yet merged.
// When ignoreDrafts is true, draft PRs are excluded at the GitHub search layer.
func GetApprovedUnmerged(ctx context.Context, repoFilter string, ignoreDrafts bool) ([]ApprovedPR, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	query := `query($q: String!) {
  search(query: $q, type: ISSUE, first: 50) {
    nodes {
      ... on PullRequest {
        number
        title
        author { login }
        repository { name nameWithOwner }
        createdAt
        url
        reviewDecision
      }
    }
  }
}`

	q := buildApprovedUnmergedQuery(repoFilter, ignoreDrafts)

	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "q="+q,
	)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("approved PRs query timed out after %s", apiTimeout)
		}
		return nil, fmt.Errorf("GraphQL query failed: %s", ghError(err))
	}

	var result struct {
		Data struct {
			Search struct {
				Nodes []ApprovedPR `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing GraphQL response: %w", err)
	}

	var filtered []ApprovedPR
	for _, pr := range result.Data.Search.Nodes {
		if pr.Number != 0 {
			filtered = append(filtered, pr)
		}
	}
	return filtered, nil
}

// buildMyOpenPRsQuery returns the GitHub search query string for
// GetMyOpenPRs. Unlike other queries, drafts are always included — a PR's
// own author wants to see their WIP work, not just review-ready PRs. Results
// are sorted oldest first so pagination walks a stable order, and if
// maxSearchPages ever cuts the list short, the newest PRs are the ones left
// out rather than an arbitrary set.
func buildMyOpenPRsQuery(repoFilter string) string {
	repoClause := ""
	if repoFilter != "" {
		repoClause = " repo:" + repoFilter
	}
	return fmt.Sprintf("is:pr is:open author:@me%s sort:created-asc", repoClause)
}

// myOpenPRsSearch is one page of GetMyOpenPRs. See reviewRequestSearch for why
// first is inlined and after may be omitted.
const myOpenPRsSearch = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 50, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        number
        title
        repository { name nameWithOwner }
        createdAt
        url
        isDraft
        reviewDecision
        mergeable
        baseRefName
        headRefName
        reviewRequests { totalCount }
        commits(last: 1) {
          nodes {
            commit {
              statusCheckRollup { state }
            }
          }
        }
      }
    }
  }
}`

// GetMyOpenPRs fetches the user's own open PRs, enriched with draft state,
// review decision, mergeability, pending review requests, and the latest
// commit's CI check rollup — enough to classify each into a status bucket
// (ready to merge, failing CI, changes requested, in review, in flight, draft).
//
// When GitHub answers with some results and some errors (see PartialError),
// GetMyOpenPRs returns the results it got together with the *PartialError, so
// one inaccessible organization does not hide every other PR.
func GetMyOpenPRs(ctx context.Context, repoFilter string) ([]MyPR, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	nodes, err := searchAll[MyPR](ctx, myOpenPRsSearch, buildMyOpenPRsQuery(repoFilter), "my open PRs")
	var partial *PartialError
	if err != nil && !errors.As(err, &partial) {
		return nil, err
	}

	// A node GitHub withheld (null, or not a PullRequest) decodes to the zero
	// MyPR; drop those.
	var filtered []MyPR
	for _, pr := range nodes {
		if pr.Number != 0 {
			filtered = append(filtered, pr)
		}
	}
	return filtered, err
}

// ListOpenPRs lists open PRs for a repository using `gh pr list`. When
// ignoreDrafts is true, drafts are excluded via `--draft=false`.
func ListOpenPRs(ctx context.Context, fullRepo string, limit int, ignoreDrafts bool) ([]ReviewRequest, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	args := []string{
		"pr", "list",
		"-R", fullRepo,
		"--state", "open",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,createdAt,url",
	}
	if ignoreDrafts {
		args = append(args, "--draft=false")
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("listing open PRs timed out after %s", apiTimeout)
		}
		return nil, err
	}

	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		CreatedAt string `json:"createdAt"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, err
	}

	var result []ReviewRequest
	for _, pr := range prs {
		parts := strings.SplitN(fullRepo, "/", 2)
		repoName := fullRepo
		if len(parts) == 2 {
			repoName = parts[1]
		}
		result = append(result, ReviewRequest{
			Number: pr.Number,
			Title:  pr.Title,
			Author: AuthorInfo{Login: pr.Author.Login},
			Repository: RepoInfo{
				Name:          repoName,
				NameWithOwner: fullRepo,
			},
			CreatedAt: pr.CreatedAt,
			URL:       pr.URL,
		})
	}
	return result, nil
}
