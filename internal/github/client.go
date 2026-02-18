package github

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	gh "github.com/google/go-github/v75/github"
	"golang.org/x/oauth2"
)

// Client wraps go-github with auth from `gh auth token`.
type Client struct {
	gh *gh.Client
}

// NewClient creates a GitHub client using the token from `gh auth token`.
func NewClient(ctx context.Context) (*Client, error) {
	token, err := ghAuthToken()
	if err != nil {
		return nil, fmt.Errorf("getting GitHub token: %w", err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := gh.NewClient(tc)

	return &Client{gh: client}, nil
}

// ghAuthToken runs `gh auth token` and returns the token string.
func ghAuthToken() (string, error) {
	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token failed: %s (is gh CLI installed and authenticated?)", ghError(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// GitHub returns the underlying go-github client.
func (c *Client) GitHub() *gh.Client {
	return c.gh
}
