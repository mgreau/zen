package github

import (
	"context"
	"fmt"
	"strings"

	gh "github.com/google/go-github/v75/github"
)

// PRDetails holds basic PR information.
type PRDetails struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	State       string `json:"state"`
	HeadRefName string `json:"head_ref_name"`
	BaseRefName string `json:"base_ref_name"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
	IsFork      bool   `json:"is_fork"`
}

// GetPRDetails fetches details for a specific PR.
func (c *Client) GetPRDetails(ctx context.Context, fullRepo string, prNumber int) (*PRDetails, error) {
	owner, repo := splitRepo(fullRepo)
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("fetching PR #%d: %w", prNumber, err)
	}

	return &PRDetails{
		Number:      pr.GetNumber(),
		Title:       pr.GetTitle(),
		Author:      pr.GetUser().GetLogin(),
		State:       pr.GetState(),
		HeadRefName: pr.GetHead().GetRef(),
		BaseRefName: pr.GetBase().GetRef(),
		Body:        pr.GetBody(),
		CreatedAt:   pr.GetCreatedAt().Format("2006-01-02T15:04:05Z"),
		URL:         pr.GetHTMLURL(),
		IsFork:      pr.GetHead().GetRepo().GetFork(),
	}, nil
}

// GetPRState returns the state of a PR: OPEN, CLOSED, or MERGED.
func (c *Client) GetPRState(ctx context.Context, fullRepo string, prNumber int) (string, error) {
	owner, repo := splitRepo(fullRepo)
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return "", fmt.Errorf("fetching PR state: %w", err)
	}

	if pr.GetMerged() {
		return "MERGED", nil
	}
	return strings.ToUpper(pr.GetState()), nil
}

// GetPRAuthor returns the login of the PR author.
func (c *Client) GetPRAuthor(ctx context.Context, fullRepo string, prNumber int) (string, error) {
	owner, repo := splitRepo(fullRepo)
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return "", err
	}
	return pr.GetUser().GetLogin(), nil
}

// GetPRTitle returns the title of a PR.
func (c *Client) GetPRTitle(ctx context.Context, fullRepo string, prNumber int) (string, error) {
	owner, repo := splitRepo(fullRepo)
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return "", err
	}
	return pr.GetTitle(), nil
}

// GetPRFiles returns the list of changed file paths for a PR.
func (c *Client) GetPRFiles(ctx context.Context, fullRepo string, prNumber int) ([]string, error) {
	owner, repo := splitRepo(fullRepo)
	var allFiles []string
	opts := &gh.ListOptions{PerPage: 100}

	for {
		files, resp, err := c.gh.PullRequests.ListFiles(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			allFiles = append(allFiles, f.GetFilename())
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allFiles, nil
}

// GetReviewStatus returns the user's latest review state on a PR.
func (c *Client) GetReviewStatus(ctx context.Context, fullRepo string, prNumber int) (string, error) {
	owner, repo := splitRepo(fullRepo)

	user, _, err := c.gh.Users.Get(ctx, "")
	if err != nil {
		return "", err
	}

	reviews, _, err := c.gh.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return "", err
	}

	login := user.GetLogin()
	var latest string
	for _, r := range reviews {
		if r.GetUser().GetLogin() == login {
			latest = r.GetState()
		}
	}
	return latest, nil
}

// GetPRStateByBranch looks up PRs by head branch name and returns the state
// ("MERGED", "CLOSED", "OPEN") and PR number of the first match.
// Returns ("", 0, nil) if no PR found for that branch.
func (c *Client) GetPRStateByBranch(ctx context.Context, fullRepo, branch string) (string, int, error) {
	owner, repo := splitRepo(fullRepo)
	prs, _, err := c.gh.PullRequests.List(ctx, owner, repo, &gh.PullRequestListOptions{
		Head:  owner + ":" + branch,
		State: "all",
	})
	if err != nil {
		return "", 0, fmt.Errorf("listing PRs for branch %s: %w", branch, err)
	}
	if len(prs) == 0 {
		return "", 0, nil
	}
	pr := prs[0]
	if pr.GetMerged() {
		return "MERGED", pr.GetNumber(), nil
	}
	return strings.ToUpper(pr.GetState()), pr.GetNumber(), nil
}

// IsRequestedReviewer checks if the given user login is a requested reviewer on a PR.
func (c *Client) IsRequestedReviewer(ctx context.Context, fullRepo string, prNumber int, login string) (bool, error) {
	owner, repo := splitRepo(fullRepo)
	reviewers, _, err := c.gh.PullRequests.ListReviewers(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return false, err
	}
	for _, u := range reviewers.Users {
		if u.GetLogin() == login {
			return true, nil
		}
	}
	return false, nil
}

func splitRepo(fullRepo string) (string, string) {
	parts := strings.SplitN(fullRepo, "/", 2)
	if len(parts) != 2 {
		return fullRepo, ""
	}
	return parts[0], parts[1]
}
