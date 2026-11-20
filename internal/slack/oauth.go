package slack

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// UserScopes are the user-token scopes the task watcher needs. PKCE
// (desktop-redirect) installs cannot request bot scopes, so everything zen
// does runs as the signed-in user.
var UserScopes = []string{
	"reactions:read",
	"channels:history",
	"groups:history",
	"im:history",
	"mpim:history",
	"chat:write",
}

const (
	defaultAuthorizeURL = "https://slack.com/oauth/v2/authorize"

	// RefreshTokenLifetime is how long a refresh token issued to a PKCE
	// (public-client) app stays valid. Slack doesn't document whether the
	// window slides on each refresh, so zen measures it from login.
	RefreshTokenLifetime = 30 * 24 * time.Hour
)

// PKCE is an RFC 7636 code verifier and its S256 challenge.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a fresh verifier (64 URL-safe characters, within RFC
// 7636's 43–128 range) and its S256 challenge.
func NewPKCE() (PKCE, error) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return PKCE{}, fmt.Errorf("generating code verifier: %w", err)
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(v))
	return PKCE{Verifier: v, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// NewState returns a random value for the OAuth state parameter.
func NewState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthorizeURL builds the Slack authorize URL for a user-scopes-only PKCE
// sign-in.
func AuthorizeURL(clientID, redirectURI, state string, p PKCE) string {
	q := url.Values{
		"client_id":             {clientID},
		"user_scope":            {strings.Join(UserScopes, ",")},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
	}
	return defaultAuthorizeURL + "?" + q.Encode()
}

// ParseCallback extracts the authorization code from the redirect Slack sent
// the browser to. raw may be the full URL copied from the address bar or just
// its query string. The state must match want; a mismatch means the redirect
// didn't come from this sign-in.
func ParseCallback(raw, want string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty redirect URL")
	}
	query := raw
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		query = raw[i+1:]
	}
	if i := strings.IndexByte(query, '#'); i >= 0 {
		query = query[:i]
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", fmt.Errorf("parsing redirect URL: %w", err)
	}
	// Check state before anything else, so an unrelated request can't end
	// the sign-in by sending ?error=... without it.
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(want)) != 1 {
		return "", ErrStateMismatch
	}
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("slack returned error: %s", e)
	}
	code := q.Get("code")
	if code == "" {
		return "", errors.New("redirect URL has no code parameter")
	}
	return code, nil
}

// ErrStateMismatch means a redirect didn't come from this sign-in attempt.
var ErrStateMismatch = errors.New("state parameter does not match this sign-in; use the URL from the latest zen slack login")

// Token is a rotating Slack user token plus what's needed to refresh it.
type Token struct {
	ClientID     string    `json:"client_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       string    `json:"user_id,omitempty"`
	TeamID       string    `json:"team_id,omitempty"`
	TeamName     string    `json:"team_name,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	// LoginAt is when the user last completed zen slack login. The refresh
	// token chain expires RefreshTokenLifetime after it.
	LoginAt time.Time `json:"login_at"`
}

// ReloginBy is the latest time a fresh login will be needed.
func (t *Token) ReloginBy() time.Time {
	return t.LoginAt.Add(RefreshTokenLifetime)
}

// OAuthClient talks to Slack's oauth.v2.access endpoint for a public (PKCE)
// app. It never sends a client secret.
type OAuthClient struct {
	ClientID string
	http     *http.Client
	baseURL  string
	now      func() time.Time
}

// NewOAuthClient returns an OAuthClient for the Slack app clientID.
func NewOAuthClient(clientID string) *OAuthClient {
	return &OAuthClient{
		ClientID: clientID,
		http:     &http.Client{Timeout: 15 * time.Second},
		baseURL:  defaultBaseURL,
		now:      time.Now,
	}
}

type userTokenFields struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type oauthResponse struct {
	envelope
	userTokenFields
	AuthedUser *userTokenFields `json:"authed_user"`
	Team       *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
}

// Exchange trades an authorization code for a user token.
func (o *OAuthClient) Exchange(ctx context.Context, code, verifier, redirectURI string) (*Token, error) {
	tok, err := o.call(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {o.ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
	if err != nil {
		return nil, err
	}
	tok.LoginAt = o.now()
	return tok, nil
}

// Refresh redeems prev's refresh token for a new access and refresh token.
// Slack revokes the old refresh token once redeemed, so the result must be
// persisted before it's used.
func (o *OAuthClient) Refresh(ctx context.Context, prev *Token) (*Token, error) {
	tok, err := o.call(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {o.ClientID},
		"refresh_token": {prev.RefreshToken},
	})
	if err != nil {
		return nil, err
	}
	tok.LoginAt = prev.LoginAt
	if tok.UserID == "" {
		tok.UserID = prev.UserID
	}
	if tok.TeamID == "" {
		tok.TeamID, tok.TeamName = prev.TeamID, prev.TeamName
	}
	if tok.Scope == "" {
		tok.Scope = prev.Scope
	}
	return tok, nil
}

func (o *OAuthClient) call(ctx context.Context, form url.Values) (*Token, error) {
	const method = "oauth.v2.access"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+method, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s response: %w", method, err)
	}
	var r oauthResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parsing %s response: %w", method, err)
	}
	if !r.OK {
		return nil, &APIError{Method: method, Code: r.Error}
	}

	// User tokens come back under authed_user; fall back to top-level in
	// case a user-only response isn't wrapped.
	u := r.userTokenFields
	if r.AuthedUser != nil && r.AuthedUser.AccessToken != "" {
		u = *r.AuthedUser
	}
	if u.AccessToken == "" {
		return nil, fmt.Errorf("%s response has no user access token", method)
	}
	if u.RefreshToken == "" || u.ExpiresIn <= 0 {
		return nil, fmt.Errorf("%s returned a non-rotating token; turn on PKCE (and token rotation) in the Slack app settings", method)
	}
	tok := &Token{
		ClientID:     o.ClientID,
		AccessToken:  u.AccessToken,
		RefreshToken: u.RefreshToken,
		ExpiresAt:    o.now().Add(time.Duration(u.ExpiresIn) * time.Second),
		UserID:       u.ID,
		Scope:        u.Scope,
	}
	if r.Team != nil {
		tok.TeamID, tok.TeamName = r.Team.ID, r.Team.Name
	}
	return tok, nil
}
