package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

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
	cmd := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("fetching current user: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// GetReviewRequests fetches PRs where the user is a requested reviewer,
// including re-reviews. Uses GraphQL via `gh api graphql`.
func GetReviewRequests(ctx context.Context, repoFilter string) ([]ReviewRequest, error) {
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

	repoClause := ""
	if repoFilter != "" {
		repoClause = " repo:" + repoFilter
	}

	q1 := fmt.Sprintf("is:pr is:open review-requested:@me%s", repoClause)
	q2 := fmt.Sprintf("is:pr is:open reviewed-by:@me review:required%s", repoClause)

	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "q1="+q1,
		"-f", "q2="+q2,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("GraphQL query failed: %w", err)
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
func GetApprovedUnmerged(ctx context.Context, repoFilter string) ([]ApprovedPR, error) {
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

	repoClause := ""
	if repoFilter != "" {
		repoClause = " repo:" + repoFilter
	}

	q := fmt.Sprintf("is:pr is:open author:@me review:approved%s", repoClause)

	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "q="+q,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("GraphQL query failed: %w", err)
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

// ListOpenPRs lists open PRs for a repository using `gh pr list`.
func ListOpenPRs(ctx context.Context, fullRepo string, limit int) ([]ReviewRequest, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"-R", fullRepo,
		"--state", "open",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,createdAt,url",
	)
	out, err := cmd.Output()
	if err != nil {
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
