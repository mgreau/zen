package github

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	gh "github.com/google/go-github/v75/github"
	"golang.org/x/oauth2"
)

const apiTimeout = 30 * time.Second

// Client wraps go-github with auth from `gh auth token`.
type Client struct {
	gh *gh.Client
}

// NewClient creates a GitHub client using the token from `gh auth token`.
func NewClient(ctx context.Context) (*Client, error) {
	token, err := ghAuthToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting GitHub token: %w", err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	tc.Timeout = apiTimeout
	client := gh.NewClient(tc)

	return &Client{gh: client}, nil
}

// ghAuthToken runs `gh auth token` and returns the token string.
func ghAuthToken(ctx context.Context) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("gh auth token timed out after %s", apiTimeout)
		}
		return "", fmt.Errorf("gh auth token failed: %s (is gh CLI installed and authenticated?)", ghError(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// GitHub returns the underlying go-github client.
func (c *Client) GitHub() *gh.Client {
	return c.gh
}
