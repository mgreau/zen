package github

import (
	"context"
	"encoding/json"
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
}

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

// GetReviewRequests fetches PRs where the user is a requested reviewer,
// including re-reviews. Uses GraphQL via `gh api graphql`. When ignoreDrafts
// is true, draft PRs are filtered out at the GitHub search layer.
func GetReviewRequests(ctx context.Context, repoFilter string, ignoreDrafts bool) ([]ReviewRequest, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	query := `query($q1: String!, $q2: String!) {
  requested: search(query: $q1, type: ISSUE, first: 50) {
    nodes {
      ... on PullRequest {
        number
        title
        author { login }
        repository { name nameWithOwner }
        createdAt
        url
      }
    }
  }
  rereview: search(query: $q2, type: ISSUE, first: 50) {
    nodes {
      ... on PullRequest {
        number
        title
        author { login }
        repository { name nameWithOwner }
        createdAt
        url
      }
    }
  }
}`

	q1, q2 := buildReviewRequestQueries(repoFilter, ignoreDrafts)

	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "q1="+q1,
		"-f", "q2="+q2,
	)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("review requests query timed out after %s", apiTimeout)
		}
		return nil, fmt.Errorf("GraphQL query failed: %s", ghError(err))
	}

	var result struct {
		Data struct {
			Requested struct {
				Nodes []ReviewRequest `json:"nodes"`
			} `json:"requested"`
			Rereview struct {
				Nodes []ReviewRequest `json:"nodes"`
			} `json:"rereview"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing GraphQL response: %w", err)
	}

	// Merge and deduplicate
	seen := make(map[int]bool)
	var merged []ReviewRequest
	for _, lists := range [][]ReviewRequest{result.Data.Requested.Nodes, result.Data.Rereview.Nodes} {
		for _, rr := range lists {
			if rr.Number == 0 {
				continue
			}
			if !seen[rr.Number] {
				seen[rr.Number] = true
				merged = append(merged, rr)
			}
		}
	}
	return merged, nil
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
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Author    struct {
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
			Number:    pr.Number,
			Title:     pr.Title,
			Author:    AuthorInfo{Login: pr.Author.Login},
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
