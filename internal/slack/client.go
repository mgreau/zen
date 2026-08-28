// Package slack is a minimal Slack Web API client for zen's Slack task
// watcher. It only implements the handful of methods the watcher needs
// (reactions.list, conversations.replies, reactions.add, chat.postMessage,
// chat.getPermalink, auth.test) — it is not a general-purpose Slack SDK.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const defaultBaseURL = "https://slack.com/api/"

// Client is a thin wrapper around the Slack Web API, authenticated with a
// single user token (scopes: reactions:read, channels:history, groups:history,
// im:history, mpim:history, chat:write).
type Client struct {
	token   string
	http    *http.Client
	baseURL string
}

// NewClient creates a Slack API client authenticated with token.
func NewClient(token string) *Client {
	return &Client{
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: defaultBaseURL,
	}
}

// envelope is the {ok, error} shape every Slack Web API response embeds.
type envelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *Client) get(ctx context.Context, method string, params url.Values) ([]byte, error) {
	u := c.baseURL + method
	if params != nil {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.do(req, method)
}

func (c *Client) post(ctx context.Context, method string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return c.do(req, method)
}

func (c *Client) do(req *http.Request, method string) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s response: %w", method, err)
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parsing %s response: %w", method, err)
	}
	if !env.OK {
		return nil, &APIError{Method: method, Code: env.Error}
	}
	return body, nil
}

// APIError is returned when Slack responds with {"ok": false}.
type APIError struct {
	Method string
	Code   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("slack %s: %s", e.Method, e.Code)
}

// Reaction is a single named reaction on a message, with the count and users
// who added it.
type Reaction struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Users []string `json:"users"`
}

type reactionItem struct {
	Channel string `json:"channel"`
	Message struct {
		Ts        string     `json:"ts"`
		Text      string     `json:"text"`
		Reactions []Reaction `json:"reactions"`
	} `json:"message"`
}

type reactionsListResponse struct {
	Items            []reactionItem `json:"items"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// ReactionHit identifies a message carrying the emoji ListReactions searched for.
type ReactionHit struct {
	ChannelID string
	MessageTS string
}

// ListReactions pages through reactions.list for the authenticated user and
// returns every message that carries a reaction named emoji, across up to
// maxPages pages of 100 items each. reactions.list documents no sort order or
// since-timestamp filter, so callers must dedupe against their own seen-set
// rather than relying on pagination order or an early cutoff.
func (c *Client) ListReactions(ctx context.Context, emoji string, maxPages int) ([]ReactionHit, error) {
	var hits []ReactionHit
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := url.Values{"limit": {"100"}, "full": {"true"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		body, err := c.get(ctx, "reactions.list", params)
		if err != nil {
			return nil, err
		}
		var resp reactionsListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parsing reactions.list response: %w", err)
		}
		for _, item := range resp.Items {
			for _, r := range item.Message.Reactions {
				if r.Name == emoji {
					hits = append(hits, ReactionHit{ChannelID: item.Channel, MessageTS: item.Message.Ts})
				}
			}
		}
		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}
	return hits, nil
}

// Message is a single Slack message, as returned by conversations.replies.
type Message struct {
	User string `json:"user"`
	Text string `json:"text"`
	Ts   string `json:"ts"`
}

// ConversationsReplies returns the parent message plus all replies in a thread.
func (c *Client) ConversationsReplies(ctx context.Context, channel, ts string) ([]Message, error) {
	params := url.Values{"channel": {channel}, "ts": {ts}, "limit": {"200"}}
	body, err := c.get(ctx, "conversations.replies", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing conversations.replies response: %w", err)
	}
	return resp.Messages, nil
}

// AddReaction adds a reaction to a message. Slack's "already_reacted" error
// is treated as success — idempotent under daemon restarts re-processing the
// same message.
func (c *Client) AddReaction(ctx context.Context, channel, ts, name string) error {
	_, err := c.post(ctx, "reactions.add", map[string]string{
		"channel":   channel,
		"timestamp": ts,
		"name":      name,
	})
	if apiErr, ok := err.(*APIError); ok && apiErr.Code == "already_reacted" {
		return nil
	}
	return err
}

// PostMessage sends a message to a channel or, when channel is a user ID, a
// direct message to that user.
func (c *Client) PostMessage(ctx context.Context, channel, text string) error {
	_, err := c.post(ctx, "chat.postMessage", map[string]string{
		"channel": channel,
		"text":    text,
	})
	return err
}

// Permalink returns the permalink URL for a message.
func (c *Client) Permalink(ctx context.Context, channel, ts string) (string, error) {
	params := url.Values{"channel": {channel}, "message_ts": {ts}}
	body, err := c.get(ctx, "chat.getPermalink", params)
	if err != nil {
		return "", err
	}
	var resp struct {
		Permalink string `json:"permalink"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing chat.getPermalink response: %w", err)
	}
	return resp.Permalink, nil
}

// AuthTest returns the Slack user ID the token authenticates as.
func (c *Client) AuthTest(ctx context.Context) (userID string, err error) {
	body, err := c.get(ctx, "auth.test", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing auth.test response: %w", err)
	}
	return resp.UserID, nil
}

// HasReaction reports whether a message already carries a reaction named name.
func (c *Client) HasReaction(ctx context.Context, channel, ts, name string) (bool, error) {
	params := url.Values{"channel": {channel}, "timestamp": {ts}, "full": {"true"}}
	body, err := c.get(ctx, "reactions.get", params)
	if err != nil {
		return false, err
	}
	var resp struct {
		Message struct {
			Reactions []Reaction `json:"reactions"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, fmt.Errorf("parsing reactions.get response: %w", err)
	}
	for _, r := range resp.Message.Reactions {
		if r.Name == name {
			return true, nil
		}
	}
	return false, nil
}
