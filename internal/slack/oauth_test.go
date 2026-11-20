package slack

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPKCE(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(p.Verifier); n < 43 || n > 128 {
		t.Errorf("verifier length = %d, want 43..128", n)
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); p.Challenge != want {
		t.Errorf("challenge = %q, want %q", p.Challenge, want)
	}
}

func TestAuthorizeURL(t *testing.T) {
	u, err := url.Parse(AuthorizeURL("123.456", "http://localhost:8765/callback", "st", PKCE{Challenge: "ch"}))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id":             "123.456",
		"redirect_uri":          "http://localhost:8765/callback",
		"state":                 "st",
		"code_challenge":        "ch",
		"code_challenge_method": "S256",
		"user_scope":            strings.Join(UserScopes, ","),
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if q.Has("scope") {
		t.Error("authorize URL must not request bot scopes")
	}
}

func TestParseCallback(t *testing.T) {
	tests := []struct {
		name, raw, wantCode, wantErr string
	}{
		{name: "full URL", raw: "http://localhost:8765/callback?code=abc&state=st", wantCode: "abc"},
		{name: "query only", raw: "  code=abc&state=st\n", wantCode: "abc"},
		{name: "fragment ignored", raw: "http://localhost:8765/callback?code=abc&state=st#x", wantCode: "abc"},
		{name: "bad state", raw: "http://localhost/callback?code=abc&state=other", wantErr: "state"},
		{name: "missing state", raw: "http://localhost/callback?code=abc", wantErr: "state"},
		{name: "no code", raw: "http://localhost/callback?state=st", wantErr: "no code"},
		{name: "error without state", raw: "http://localhost/callback?error=access_denied", wantErr: "state"},
		{name: "denied", raw: "http://localhost/callback?error=access_denied&state=st", wantErr: "access_denied"},
		{name: "empty", raw: " ", wantErr: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, err := ParseCallback(tt.raw, "st")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func newTestOAuth(t *testing.T, now time.Time, handler http.HandlerFunc) *OAuthClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	o := NewOAuthClient("123.456")
	o.baseURL = srv.URL + "/"
	o.now = func() time.Time { return now }
	return o
}

func TestExchange(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	o := newTestOAuth(t, now, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth.v2.access" || r.Method != http.MethodPost {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for k, want := range map[string]string{
			"grant_type":    "authorization_code",
			"client_id":     "123.456",
			"code":          "the-code",
			"code_verifier": "the-verifier",
			"redirect_uri":  "http://localhost:8765/callback",
		} {
			if got := r.PostForm.Get(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		if r.PostForm.Has("client_secret") {
			t.Error("public client must not send client_secret")
		}
		w.Write([]byte(`{"ok":true,"team":{"id":"T1","name":"Acme"},"authed_user":{"id":"U1","scope":"chat:write","access_token":"xoxe.xoxp-1","refresh_token":"xoxe-1-r","expires_in":43200,"token_type":"user"}}`))
	})
	tok, err := o.Exchange(context.Background(), "the-code", "the-verifier", "http://localhost:8765/callback")
	if err != nil {
		t.Fatal(err)
	}
	want := Token{
		ClientID: "123.456", AccessToken: "xoxe.xoxp-1", RefreshToken: "xoxe-1-r",
		ExpiresAt: now.Add(12 * time.Hour), UserID: "U1", TeamID: "T1", TeamName: "Acme",
		Scope: "chat:write", LoginAt: now,
	}
	if *tok != want {
		t.Errorf("token = %+v\nwant    %+v", *tok, want)
	}
}

func TestExchange_NonRotatingToken(t *testing.T) {
	o := newTestOAuth(t, time.Now(), func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"authed_user":{"id":"U1","access_token":"xoxp-1"}}`))
	})
	_, err := o.Exchange(context.Background(), "c", "v", "r")
	if err == nil || !strings.Contains(err.Error(), "non-rotating") {
		t.Fatalf("err = %v, want non-rotating error", err)
	}
}

func TestExchange_APIError(t *testing.T) {
	o := newTestOAuth(t, time.Now(), func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error":"invalid_code_verifier"}`))
	})
	_, err := o.Exchange(context.Background(), "c", "v", "r")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_code_verifier" {
		t.Fatalf("err = %v, want invalid_code_verifier", err)
	}
}

// refreshFixture is a token file plus a fake oauth.v2.access that rotates.
type refreshFixture struct {
	path     string
	src      *RefreshingSource
	now      time.Time
	calls    atomic.Int32
	failWith string
}

func newRefreshFixture(t *testing.T, expiresIn time.Duration) *refreshFixture {
	t.Helper()
	f := &refreshFixture{
		path: filepath.Join(t.TempDir(), "slack_token.json"),
		now:  time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	}
	login := f.now.Add(-24 * time.Hour)
	if err := SaveToken(f.path, &Token{
		ClientID: "123.456", AccessToken: "access-0", RefreshToken: "refresh-0",
		ExpiresAt: f.now.Add(expiresIn), UserID: "U1", TeamID: "T1", LoginAt: login,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if f.failWith != "" {
			w.Write([]byte(`{"ok":false,"error":"` + f.failWith + `"}`))
			return
		}
		n := f.calls.Add(1)
		if got, want := r.PostForm.Get("refresh_token"), "refresh-"+strconv.Itoa(int(n)-1); got != want {
			t.Errorf("refresh_token = %q, want %q", got, want)
		}
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("client_id") != "123.456" {
			t.Errorf("unexpected refresh form %v", r.PostForm)
		}
		w.Write([]byte(`{"ok":true,"authed_user":{"access_token":"access-` + strconv.Itoa(int(n)) + `","refresh_token":"refresh-` + strconv.Itoa(int(n)) + `","expires_in":43200}}`))
	}))
	t.Cleanup(srv.Close)

	src, err := NewRefreshingSource(f.path)
	if err != nil {
		t.Fatal(err)
	}
	src.now = func() time.Time { return f.now }
	src.oauth = func(clientID string) *OAuthClient {
		o := NewOAuthClient(clientID)
		o.baseURL = srv.URL + "/"
		o.now = src.now
		return o
	}
	f.src = src
	return f
}

func TestRefreshingSource_FreshTokenNotRefreshed(t *testing.T) {
	f := newRefreshFixture(t, time.Hour)
	tok, err := f.src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "access-0" || f.calls.Load() != 0 {
		t.Errorf("token = %q after %d refreshes, want access-0 with none", tok, f.calls.Load())
	}
}

func TestRefreshingSource_RefreshesAndPersists(t *testing.T) {
	f := newRefreshFixture(t, 2*time.Minute) // inside the 5m skew
	tok, err := f.src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "access-1" {
		t.Errorf("token = %q, want access-1", tok)
	}
	disk, err := LoadToken(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if disk.RefreshToken != "refresh-1" || disk.UserID != "U1" || !disk.LoginAt.Equal(f.now.Add(-24*time.Hour)) {
		t.Errorf("persisted token = %+v", disk)
	}
	info, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}

	// Second call uses the cached token.
	if tok, _ := f.src.Token(context.Background()); tok != "access-1" || f.calls.Load() != 1 {
		t.Errorf("token = %q after %d refreshes, want access-1 after 1", tok, f.calls.Load())
	}
}

func TestRefreshingSource_UsesTokenRotatedByAnotherProcess(t *testing.T) {
	f := newRefreshFixture(t, time.Minute)
	other := &Token{
		ClientID: "123.456", AccessToken: "access-other", RefreshToken: "refresh-other",
		ExpiresAt: f.now.Add(12 * time.Hour), LoginAt: f.now,
	}
	if err := SaveToken(f.path, other); err != nil {
		t.Fatal(err)
	}
	tok, err := f.src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "access-other" || f.calls.Load() != 0 {
		t.Errorf("token = %q after %d refreshes, want access-other with none", tok, f.calls.Load())
	}
}

func TestRefreshingSource_FailureCallbacksFireOnce(t *testing.T) {
	f := newRefreshFixture(t, time.Minute)
	f.failWith = "invalid_refresh_token"
	var failures, recoveries int
	f.src.OnFailure = func(error) { failures++ }
	f.src.OnRecover = func() { recoveries++ }

	for range 3 {
		if _, err := f.src.Token(context.Background()); !errors.Is(err, ErrReloginRequired) {
			t.Fatalf("err = %v, want ErrReloginRequired", err)
		}
	}
	if failures != 1 {
		t.Errorf("OnFailure called %d times, want 1", failures)
	}

	f.failWith = ""
	if _, err := f.src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recoveries != 1 {
		t.Errorf("OnRecover called %d times, want 1", recoveries)
	}
}

func TestRefreshingSource_TransientFailureDoesNotAlert(t *testing.T) {
	f := newRefreshFixture(t, time.Minute)
	f.failWith = "internal_error"
	failures := 0
	f.src.OnFailure = func(error) { failures++ }
	_, err := f.src.Token(context.Background())
	if err == nil || errors.Is(err, ErrReloginRequired) {
		t.Fatalf("err = %v, want a transient error", err)
	}
	if failures != 0 {
		t.Errorf("OnFailure called %d times for a transient error, want 0", failures)
	}
}

func TestRefreshingSource_LockRespectsContext(t *testing.T) {
	f := newRefreshFixture(t, time.Minute)
	unlock, err := LockTokenFile(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := f.src.Token(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded while the lock is held", err)
	}
}

func TestClient_RetriesOnceAfterTokenExpired(t *testing.T) {
	f := newRefreshFixture(t, time.Hour)
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seen = append(seen, auth)
		if auth == "Bearer access-0" {
			w.Write([]byte(`{"ok":false,"error":"token_expired"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"user_id":"U1"}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClientWithSource(f.src)
	c.baseURL = srv.URL + "/"

	id, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id != "U1" || len(seen) != 2 || seen[1] != "Bearer access-1" {
		t.Errorf("user = %q, requests = %v", id, seen)
	}
}

func TestClient_StaticTokenExpiredNotRetried(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"ok":false,"error":"token_expired"}`))
	})
	if _, err := c.AuthTest(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
