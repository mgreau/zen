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
	// ReviewedOid is the commit the current user's latest review was submitted
	// against, or "" when no such review is known or its commit is gone
	// (force-pushed away). Replies and pending reviews do not count; see
	// isRealReview. Set by searchReviewRequests for the re-review query.
	ReviewedOid string `json:"-"`
}

// changedSinceReview reports whether the PR head moved after the current
// user's latest review. An unknown reviewed commit counts as changed, so a
// PR is only ever dropped from the re-review list on positive evidence that
// the user has already reviewed its current head.
func (rr ReviewRequest) changedSinceReview() bool {
	return rr.ReviewedOid == "" || rr.ReviewedOid != rr.HeadRefOid
}

// viewerReview is one of the current user's reviews on a PR, as fetched by
// rereviewSearch.
type viewerReview struct {
	State  string `json:"state"`
	Body   string `json:"body"`
	Commit *struct {
		Oid string `json:"oid"`
	} `json:"commit"`
	Comments struct {
		Nodes []struct {
			ReplyTo *struct {
				ID string `json:"id"`
			} `json:"replyTo"`
		} `json:"nodes"`
	} `json:"comments"`
}

// isRealReview reports whether r records the user looking at the code at its
// commit. GitHub stores every review-thread reply as a COMMENTED review with
// an empty body, attached to whatever the head was when the reply was posted,
// so a "thanks" after the author pushes would otherwise look like a review
// of the new commits. A PENDING review has not been submitted.
//
// A COMMENTED review counts when it has a body or at least one inline comment
// that starts a new thread rather than replying to one; that keeps a single
// new inline comment ("Add single comment") as a review.
func (r viewerReview) isRealReview() bool {
	switch r.State {
	case "PENDING", "":
		return false
	case "COMMENTED":
		if strings.TrimSpace(r.Body) != "" {
			return true
		}
		for _, c := range r.Comments.Nodes {
			if c.ReplyTo == nil {
				return true
			}
		}
		return false
	default: // APPROVED, CHANGES_REQUESTED, DISMISSED
		return true
	}
}

// searchNode is one PR in a search response. The user's reviews are decoded
// here rather than on ReviewRequest so they stay out of the JSON that callers
// such as the MCP inbox tool emit.
type searchNode struct {
	ReviewRequest
	ViewerReviews *struct {
		Nodes []viewerReview `json:"nodes"`
	} `json:"viewerReviews"`
}

// reviewRequest returns the node's ReviewRequest with ReviewedOid set to the
// commit of the user's latest real review. GitHub returns reviews oldest
// first, so the last real one wins.
func (n searchNode) reviewRequest() ReviewRequest {
	rr := n.ReviewRequest
	if n.ViewerReviews == nil {
		return rr
	}
	for i := len(n.ViewerReviews.Nodes) - 1; i >= 0; i-- {
		r := n.ViewerReviews.Nodes[i]
		if !r.isRealReview() {
			continue
		}
		if r.Commit != nil {
			rr.ReviewedOid = r.Commit.Oid
		}
		break
	}
	return rr
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
	out, err := runGH(ctx, "api", "user", "--jq", ".login")
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
//
// The re-review query excludes the user's own PRs: replying to a review
// thread on your own PR records a review by you, so without -author:@me
// every PR of yours that is still awaiting approval would match.
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
	q2 := fmt.Sprintf("is:pr is:open reviewed-by:@me review:required -author:@me%s%s", repoClause, draftClause)
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

// prSearchFields are the PR fields both review searches decode into
// ReviewRequest.
const prSearchFields = `
        number
        title
        author { login }
        repository { name nameWithOwner }
        createdAt
        url
        headRefOid
        isDraft
        closed`

// reviewRequestSearch is one page of a PR search. `first` is inlined because
// gh sends -f values as strings and search(first:) needs an Int; `after` is a
// nullable String, so omitting it on the first page is a valid query.
const reviewRequestSearch = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 50, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {` + prSearchFields + `
      }
    }
  }
}`

// viewerReviewWindow is how many of the user's most recent reviews per PR the
// re-review search fetches. If every one of them is a thread reply, no real
// review is found and the PR stays listed. Each extra review adds a comments
// connection to the query cost: at 10, a 50-PR page costs 6 rate-limit
// points, against 1 for reviewRequestSearch.
const viewerReviewWindow = 10

// rereviewSearch is reviewRequestSearch plus the user's latest reviews, which
// GetReviewRequests needs to tell whether the head moved since the last real
// review. viewerLatestReview is not enough: it includes thread replies.
// `author` takes a login; GitHub does not resolve @me there.
var rereviewSearch = fmt.Sprintf(`query($q: String!, $after: String, $login: String!) {
  search(query: $q, type: ISSUE, first: 50, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {%s
        viewerReviews: reviews(author: $login, last: %d, states: [APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED]) {
          nodes {
            state
            body
            commit { oid }
            comments(first: 10) { nodes { replyTo { id } } }
          }
        }
      }
    }
  }
}`, prSearchFields, viewerReviewWindow)

// searchReviewRequests runs one search query, following pagination to the end.
// Without this a 50-result page silently truncates: review requests in other
// repositories occupy slots and a configured repository's PR disappears from
// the poll, so it is neither set up nor refreshed.
//
// gqlQuery is reviewRequestSearch or rereviewSearch; vars are extra
// "name=value" GraphQL variables, such as rereviewSearch's login.
func searchReviewRequests(ctx context.Context, gqlQuery, query string, vars ...string) ([]ReviewRequest, error) {
	var all []ReviewRequest
	cursor := ""
	for page := 0; page < maxSearchPages; page++ {
		args := []string{"api", "graphql", "-f", "query=" + gqlQuery, "-f", "q=" + query}
		for _, v := range vars {
			args = append(args, "-f", v)
		}
		if cursor != "" {
			args = append(args, "-f", "after="+cursor)
		}
		out, err := runGH(ctx, args...)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("review requests query timed out after %s", apiTimeout)
			}
			return nil, fmt.Errorf("GraphQL query failed: %s", ghError(err))
		}

		var result struct {
			Data struct {
				Search struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []searchNode `json:"nodes"`
				} `json:"search"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out, &result); err != nil {
			return nil, fmt.Errorf("parsing GraphQL response: %w", err)
		}

		for _, n := range result.Data.Search.Nodes {
			all = append(all, n.reviewRequest())
		}
		info := result.Data.Search.PageInfo
		if !info.HasNextPage || info.EndCursor == "" || info.EndCursor == cursor {
			return all, nil
		}
		cursor = info.EndCursor
	}
	return all, nil
}

// GetReviewRequests fetches PRs where the user is a requested reviewer,
// including re-reviews: PRs by someone else that the user has reviewed, that
// still need an approval, and whose head has moved since the user's latest
// real review (see isRealReview). A PR the user reviewed at its current head
// is not waiting on them, so it is left out unless they are explicitly
// requested again.
// Uses GraphQL via `gh api graphql`. When ignoreDrafts
// is true, draft PRs are filtered out at the GitHub search layer. An empty
// repoFilter searches every repository the user can see, which is only safe
// for callers that show what they get; the daemon uses
// GetReviewRequestsForRepos instead.
func GetReviewRequests(ctx context.Context, repoFilter string, ignoreDrafts bool) ([]ReviewRequest, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	login, err := GetCurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	return getReviewRequests(ctx, login, repoFilter, ignoreDrafts)
}

// getReviewRequests is GetReviewRequests for a known login, so
// GetReviewRequestsForRepos looks the user up once rather than per repository.
func getReviewRequests(ctx context.Context, login, repoFilter string, ignoreDrafts bool) ([]ReviewRequest, error) {
	if login == "" {
		return nil, errors.New("fetching current user: gh returned an empty login")
	}
	q1, q2 := buildReviewRequestQueries(repoFilter, ignoreDrafts)
	requested, err := searchReviewRequests(ctx, reviewRequestSearch, q1)
	if err != nil {
		return nil, err
	}
	reviewed, err := searchReviewRequests(ctx, rereviewSearch, q2, "login="+login)
	if err != nil {
		return nil, err
	}
	var rereview []ReviewRequest
	for _, rr := range reviewed {
		if rr.changedSinceReview() {
			rereview = append(rereview, rr)
		}
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
	login := ""
	for _, full := range repoFullNames {
		if full == "" {
			continue
		}
		if login == "" {
			var err error
			if login, err = GetCurrentUser(ctx); err != nil {
				return nil, err
			}
		}
		rctx, cancel := withTimeout(ctx)
		reqs, err := getReviewRequests(rctx, login, full, ignoreDrafts)
		cancel()
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
// own author wants to see their WIP work, not just review-ready PRs.
func buildMyOpenPRsQuery(repoFilter string) string {
	repoClause := ""
	if repoFilter != "" {
		repoClause = " repo:" + repoFilter
	}
	return fmt.Sprintf("is:pr is:open author:@me%s", repoClause)
}

// GetMyOpenPRs fetches the user's own open PRs, enriched with draft state,
// review decision, mergeability, pending review requests, and the latest
// commit's CI check rollup — enough to classify each into a status bucket
// (ready to merge, failing CI, changes requested, in review, in flight, draft).
func GetMyOpenPRs(ctx context.Context, repoFilter string) ([]MyPR, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	query := `query($q: String!) {
  search(query: $q, type: ISSUE, first: 50) {
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

	q := buildMyOpenPRsQuery(repoFilter)

	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "q="+q,
	)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("my open PRs query timed out after %s", apiTimeout)
		}
		return nil, fmt.Errorf("GraphQL query failed: %s", ghError(err))
	}

	var result struct {
		Data struct {
			Search struct {
				Nodes []MyPR `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing GraphQL response: %w", err)
	}

	var filtered []MyPR
	for _, pr := range result.Data.Search.Nodes {
		if pr.Number != 0 {
			filtered = append(filtered, pr)
		}
	}
	return filtered, nil
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
