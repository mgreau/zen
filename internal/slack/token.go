package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// TokenSource supplies the bearer token for each Slack API call.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a fixed token, such as one from ZEN_SLACK_TOKEN.
type StaticToken string

// Token returns the fixed token.
func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

// invalidator is implemented by sources that can drop a token Slack rejected
// as expired and fetch a new one.
type invalidator interface {
	Invalidate(stale string)
}

// ErrNotLoggedIn is returned by LoadToken when no token file exists.
var ErrNotLoggedIn = errors.New("not signed in to Slack; run zen slack login")

// LoadToken reads a token file written by SaveToken.
func LoadToken(path string) (*Token, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, err
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if t.AccessToken == "" || t.RefreshToken == "" || t.ClientID == "" {
		return nil, fmt.Errorf("%s is incomplete; run zen slack login", path)
	}
	return &t, nil
}

// SaveToken writes t to path, readable only by the current user. The write
// is atomic so a crash never leaves a half-written file behind — losing the
// rotated refresh token would force a new login.
func SaveToken(path string, t *Token) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".slack_token-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DeleteToken removes the token file. A missing file is not an error.
func DeleteToken(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ErrReloginRequired marks a refresh failure that only a new zen slack login
// can fix, as opposed to a transient network or server error.
var ErrReloginRequired = errors.New("Slack sign-in expired or was revoked; run zen slack login")

// LockTokenFile takes an exclusive flock beside path so that two zen
// processes never redeem the same refresh token; Slack revokes it on first
// use, and the loser would be signed out. It gives up when ctx is done.
func LockTokenFile(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("waiting for %s.lock: %w", path, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// RefreshingSource serves the access token from a token file and refreshes
// it shortly before it expires, persisting each rotated refresh token.
type RefreshingSource struct {
	path string
	// Skew is how long before expiry a token is refreshed. Default 5m.
	Skew time.Duration
	// OnFailure is called once when a refresh fails in a way that needs a
	// new login (ErrReloginRequired), and OnRecover once when refreshing
	// works again. Transient failures only surface as returned errors.
	// Both run without the source's lock held and may be nil.
	OnFailure func(error)
	OnRecover func()

	mu      sync.Mutex
	tok     *Token
	failing bool
	now     func() time.Time
	oauth   func(clientID string) *OAuthClient
}

// NewRefreshingSource loads the token file at path.
func NewRefreshingSource(path string) (*RefreshingSource, error) {
	tok, err := LoadToken(path)
	if err != nil {
		return nil, err
	}
	return &RefreshingSource{
		path:  path,
		Skew:  5 * time.Minute,
		tok:   tok,
		now:   time.Now,
		oauth: NewOAuthClient,
	}, nil
}

// Current returns a copy of the token currently in use.
func (s *RefreshingSource) Current() Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.tok
}

// Token returns a valid access token, refreshing it first if needed.
func (s *RefreshingSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.fresh(s.tok) {
		tok := s.tok.AccessToken
		s.mu.Unlock()
		return tok, nil
	}
	err := s.doRefresh(ctx, s.tok.AccessToken)
	tok := s.tok.AccessToken
	notify := s.transition(err)
	s.mu.Unlock()

	if notify != nil {
		notify()
	}
	if err != nil {
		return "", err
	}
	return tok, nil
}

// transition updates the failing state after a refresh attempt and returns
// the callback to run, if any. s.mu must be held.
func (s *RefreshingSource) transition(err error) func() {
	switch {
	case err == nil && s.failing:
		s.failing = false
		return s.OnRecover
	case errors.Is(err, ErrReloginRequired) && !s.failing:
		s.failing = true
		if s.OnFailure != nil {
			return func() { s.OnFailure(err) }
		}
	}
	return nil
}

// Invalidate marks stale as unusable, so the next Token call refreshes even
// if the local expiry hasn't passed yet.
func (s *RefreshingSource) Invalidate(stale string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok.AccessToken == stale {
		s.tok.ExpiresAt = time.Time{}
	}
}

func (s *RefreshingSource) fresh(t *Token) bool {
	return s.now().Add(s.Skew).Before(t.ExpiresAt)
}

// doRefresh must be called with s.mu held. stale is the access token the
// caller found unusable.
func (s *RefreshingSource) doRefresh(ctx context.Context, stale string) error {
	unlock, err := LockTokenFile(ctx, s.path)
	if err != nil {
		return fmt.Errorf("locking Slack token file: %w", err)
	}
	defer unlock()

	// Another zen process (or a new login) may already have rotated the
	// token; use theirs rather than redeeming a revoked refresh token.
	disk, err := LoadToken(s.path)
	if errors.Is(err, ErrNotLoggedIn) {
		return fmt.Errorf("%w (token file was removed)", ErrReloginRequired)
	}
	if err != nil {
		return fmt.Errorf("reloading Slack token: %w", err)
	}
	if disk.AccessToken != stale && s.fresh(disk) {
		s.tok = disk
		return nil
	}

	next, err := s.oauth(disk.ClientID).Refresh(ctx, disk)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			switch apiErr.Code {
			case "invalid_refresh_token", "token_expired", "token_revoked", "invalid_grant", "invalid_client_id":
				return fmt.Errorf("%w: %w", ErrReloginRequired, err)
			}
		}
		return fmt.Errorf("refreshing Slack token: %w", err)
	}
	if err := SaveToken(s.path, next); err != nil {
		return fmt.Errorf("saving refreshed Slack token: %w", err)
	}
	s.tok = next
	return nil
}
