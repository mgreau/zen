package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mgreau/zen/internal/config"
	slackpkg "github.com/mgreau/zen/internal/slack"
	"github.com/spf13/cobra"
)

var (
	slackLoginClientID  string
	slackLoginPort      int
	slackLoginNoBrowser bool
	slackLoginNoListen  bool
)

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Manage the Slack sign-in used by the Slack task watcher",
}

var slackLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to Slack with OAuth (PKCE) and store a rotating token",
	Long: `Signs in to Slack through your Slack app's OAuth flow, using PKCE so no
client secret is needed, and stores the rotating user token as
slack_token.json in zen's state directory (~/.zen/state, or $ZEN_HOME/state),
readable only by you. The daemon refreshes it automatically; Slack requires a
new sign-in every 30 days.

The Slack app needs PKCE turned on and this redirect URL added (8765 is the
default; --port or slack.redirect_port changes it):

  http://localhost:8765/callback

On a remote workstation, the browser's redirect lands on your laptop's
localhost. Either forward the port when you connect (ssh -L 8765:localhost:8765)
so zen receives it directly, or copy the URL of the page that fails to load
from the address bar and paste it into zen.

ZEN_SLACK_TOKEN, when set, still takes precedence over the stored token.`,
	RunE: runSlackLogin,
}

var slackStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which Slack token zen uses and when it expires",
	RunE:  runSlackStatus,
}

var slackLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke and delete the stored Slack token",
	RunE:  runSlackLogout,
}

func init() {
	slackLoginCmd.Flags().StringVar(&slackLoginClientID, "client-id", "", "Slack app client ID (default: slack.client_id from config)")
	slackLoginCmd.Flags().IntVar(&slackLoginPort, "port", 0, "Localhost port for the redirect (default: slack.redirect_port, or 8765)")
	slackLoginCmd.Flags().BoolVar(&slackLoginNoBrowser, "no-browser", false, "Print the sign-in URL without trying to open a browser")
	slackLoginCmd.Flags().BoolVar(&slackLoginNoListen, "no-listen", false, "Don't listen for the redirect; only accept a pasted URL")
	slackCmd.AddCommand(slackLoginCmd, slackStatusCmd, slackLogoutCmd)
	rootCmd.AddCommand(slackCmd)
}

// slackTokenFile is where zen slack login keeps the rotating token.
func slackTokenFile() string {
	return filepath.Join(config.StateDir(), "slack_token.json")
}

// slackTokenSource picks the token the Slack client should use:
// ZEN_SLACK_TOKEN if set, otherwise the stored OAuth token. source describes
// which one was chosen, for log lines. refreshing is nil for the env token.
func slackTokenSource() (src slackpkg.TokenSource, refreshing *slackpkg.RefreshingSource, source string, err error) {
	if tok := os.Getenv("ZEN_SLACK_TOKEN"); tok != "" {
		return slackpkg.StaticToken(tok), nil, "ZEN_SLACK_TOKEN", nil
	}
	rs, err := slackpkg.NewRefreshingSource(slackTokenFile())
	if err != nil {
		return nil, nil, "", err
	}
	return rs, rs, "zen slack login", nil
}

type callbackResult struct {
	code string
	err  error
}

func runSlackLogin(cmd *cobra.Command, args []string) error {
	clientID := slackLoginClientID
	if clientID == "" {
		clientID = cfg.Slack.ClientID
	}
	if clientID == "" {
		return errors.New("no Slack app client ID: set slack.client_id in config.yaml or pass --client-id")
	}
	port := slackLoginPort
	if port == 0 {
		port = cfg.Slack.GetRedirectPort()
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	pkce, err := slackpkg.NewPKCE()
	if err != nil {
		return err
	}
	state, err := slackpkg.NewState()
	if err != nil {
		return err
	}

	// Slack's authorization code expires after ten minutes.
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	results := make(chan callbackResult, 2)

	listening := false
	if !slackLoginNoListen {
		srv, err := listenForCallback(port, state, results)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Not listening for the redirect (%v); paste the URL instead.\n", err)
		} else {
			listening = true
			defer srv.Close()
		}
	}

	authURL := slackpkg.AuthorizeURL(clientID, redirectURI, state, pkce)
	fmt.Println("Open this URL to sign in to Slack:")
	fmt.Println()
	fmt.Println("  " + authURL)
	fmt.Println()
	if !slackLoginNoBrowser {
		_ = openBrowser(authURL)
	}
	if listening {
		fmt.Printf("Waiting for Slack to redirect to %s ...\n", redirectURI)
		fmt.Println("If the browser shows a page that fails to load, copy its URL from the address bar and paste it here.")
	} else {
		fmt.Println("After approving, the browser lands on a page that fails to load. Copy its URL from the address bar and paste it here.")
	}
	go readPastedCallback(state, results, listening)

	var code string
	select {
	case r := <-results:
		if r.err != nil {
			return r.err
		}
		code = r.code
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("timed out waiting for the Slack redirect; run zen slack login again")
		}
		return errors.New("sign-in cancelled")
	}

	exCtx, exCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer exCancel()
	tok, err := slackpkg.NewOAuthClient(clientID).Exchange(exCtx, code, pkce.Verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("exchanging the authorization code: %w", err)
	}

	info, err := slackpkg.NewClient(tok.AccessToken).Identity(exCtx)
	if err != nil {
		return fmt.Errorf("checking the new token with auth.test: %w", err)
	}
	if tok.UserID == "" {
		tok.UserID = info.UserID
	}
	if tok.TeamID == "" {
		tok.TeamID, tok.TeamName = info.TeamID, info.Team
	}

	if err := config.EnsureDirs(); err != nil {
		return err
	}
	path := slackTokenFile()
	unlock, err := slackpkg.LockTokenFile(exCtx, path)
	if err != nil {
		return err
	}
	err = slackpkg.SaveToken(path, tok)
	unlock()
	if err != nil {
		return fmt.Errorf("saving token: %w", err)
	}

	fmt.Println()
	fmt.Printf("Signed in to %s as %s.\n", info.Team, info.User)
	fmt.Printf("Token saved to %s. zen refreshes it automatically; sign in again by %s.\n",
		path, tok.ReloginBy().Local().Format("Mon Jan 2"))
	if os.Getenv("ZEN_SLACK_TOKEN") != "" {
		fmt.Println("Note: ZEN_SLACK_TOKEN is set and takes precedence; unset it to use this sign-in.")
	}
	fmt.Println("A running daemon picks this up on its own. If it started before you had ever signed in,")
	fmt.Println("restart it: zen watch stop && zen watch start.")
	return nil
}

// listenForCallback serves the OAuth redirect on localhost:port. It binds
// both IPv4 and IPv6 loopback because "localhost" (in the browser, or in an
// ssh -L forward) may resolve to either. If another program holds the port
// on either address, the redirect could go there, so that's an error; only a
// host without IPv6 may skip ::1.
func listenForCallback(port int, state string, results chan<- callbackResult) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code, err := slackpkg.ParseCallback(r.URL.RawQuery, state)
		if errors.Is(err, slackpkg.ErrStateMismatch) {
			// Not from this sign-in; keep waiting for the real redirect.
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err != nil {
			fmt.Fprintf(w, "Slack sign-in failed: %v\nYou can close this tab.\n", err)
		} else {
			fmt.Fprintln(w, "Signed in to Slack. You can close this tab and return to the terminal.")
		}
		select {
		case results <- callbackResult{code: code, err: err}:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	var listeners []net.Listener
	closeAll := func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
		if err != nil {
			if host == "::1" && !errors.Is(err, syscall.EADDRINUSE) {
				continue
			}
			closeAll()
			return nil, fmt.Errorf("port %d on %s is unavailable: %w", port, host, err)
		}
		listeners = append(listeners, ln)
	}
	for _, ln := range listeners {
		go srv.Serve(ln)
	}
	return srv, nil
}

// readPastedCallback reads redirect URLs pasted on stdin until one parses.
// If stdin ends first and nothing is listening for the redirect, the sign-in
// can't finish, so it reports that instead of leaving login to time out.
func readPastedCallback(state string, results chan<- callbackResult, listening bool) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		code, err := slackpkg.ParseCallback(line, state)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Couldn't use that URL: %v\nPaste the full URL from the browser's address bar:\n", err)
			continue
		}
		results <- callbackResult{code: code}
		return
	}
	if !listening {
		results <- callbackResult{err: errors.New("stdin closed before a redirect URL was pasted")}
	}
}

// openBrowser tries to open url. On a workstation reached with
// cg work ssh --forward-browser, BROWSER or xdg-open forwards it to the
// laptop. Failure is fine: the URL is printed too.
func openBrowser(url string) error {
	if b := os.Getenv("BROWSER"); b != "" {
		return exec.Command(b, url).Start()
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return err
		}
		return exec.Command("xdg-open", url).Start()
	}
	return fmt.Errorf("opening URLs not supported on %s", runtime.GOOS)
}

type slackStatus struct {
	Source       string    `json:"source"`
	User         string    `json:"user,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	Team         string    `json:"team,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"access_token_expires_at,omitzero"`
	ReloginBy    time.Time `json:"relogin_by,omitzero"`
	Valid        bool      `json:"valid"`
	Error        string    `json:"error,omitempty"`
	TokenFile    string    `json:"token_file"`
	WatcherReady bool      `json:"watcher_enabled"`
}

func runSlackStatus(cmd *cobra.Command, args []string) error {
	st := slackStatus{TokenFile: slackTokenFile(), WatcherReady: cfg.Slack.Enabled}
	src, rs, source, err := slackTokenSource()
	if err != nil {
		st.Source = "none"
		st.Error = err.Error()
		return printSlackStatus(st)
	}
	st.Source = source

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	info, err := slackpkg.NewClientWithSource(src).Identity(ctx)
	if err != nil {
		st.Error = err.Error()
	} else {
		st.Valid = true
		st.User, st.UserID, st.Team = info.User, info.UserID, info.Team
	}
	if rs != nil {
		tok := rs.Current()
		st.Scope = tok.Scope
		st.ExpiresAt = tok.ExpiresAt
		st.ReloginBy = tok.ReloginBy()
	}
	return printSlackStatus(st)
}

func printSlackStatus(st slackStatus) error {
	if jsonFlag {
		printJSON(st)
		return nil
	}
	fmt.Printf("Token source:  %s\n", st.Source)
	if st.Valid {
		fmt.Printf("Signed in as:  %s (%s) in %s\n", st.User, st.UserID, st.Team)
	}
	if st.Scope != "" {
		fmt.Printf("Scopes:        %s\n", st.Scope)
	}
	if !st.ExpiresAt.IsZero() {
		if st.Valid {
			fmt.Printf("Access token:  expires %s (refreshed automatically)\n", st.ExpiresAt.Local().Format(time.DateTime))
		} else {
			fmt.Printf("Access token:  expired %s and could not be refreshed\n", st.ExpiresAt.Local().Format(time.DateTime))
		}
	}
	if !st.ReloginBy.IsZero() {
		if left := time.Until(st.ReloginBy); left > 0 {
			fmt.Printf("Sign in again: by %s (%s left)\n", st.ReloginBy.Local().Format("Mon Jan 2"), formatDaysHours(left))
		} else {
			fmt.Printf("Sign in again: now; the 30-day sign-in ended %s. Run zen slack login.\n", st.ReloginBy.Local().Format("Mon Jan 2"))
		}
	}
	if st.Error != "" {
		fmt.Printf("Problem:       %s\n", st.Error)
	}
	if !st.WatcherReady {
		fmt.Println("Slack watcher: disabled (slack.enabled is false)")
	}
	return nil
}

// formatDaysHours renders d as "12d 5h", or "5h" under a day.
func formatDaysHours(d time.Duration) string {
	h := int(d.Round(time.Hour).Hours())
	if h < 24 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd %dh", h/24, h%24)
}

func runSlackLogout(cmd *cobra.Command, args []string) error {
	path := slackTokenFile()
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	unlock, err := slackpkg.LockTokenFile(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()

	tok, err := slackpkg.LoadToken(path)
	if errors.Is(err, slackpkg.ErrNotLoggedIn) {
		fmt.Println("Not signed in.")
		return nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't read the stored token (%v); deleting it without revoking.\n", err)
	} else if rerr := revokeStoredToken(ctx, tok); rerr != nil {
		fmt.Fprintf(os.Stderr, "Couldn't revoke the token with Slack (%v); deleting it locally anyway.\n", rerr)
		fmt.Fprintln(os.Stderr, "The sign-in may stay valid on Slack's side until it expires; you can remove it under your Slack account's authorized apps.")
	}
	if err := slackpkg.DeleteToken(path); err != nil {
		return err
	}
	fmt.Printf("Deleted %s.\n", path)
	return nil
}

// revokeStoredToken revokes the stored access token itself, which is the one
// a running daemon is using. Refreshing first would revoke only a new token
// and leave the daemon's copy valid, so it refreshes only if the stored
// token has already expired. The caller holds the token file lock.
func revokeStoredToken(ctx context.Context, tok *slackpkg.Token) error {
	access := tok.AccessToken
	if !time.Now().Before(tok.ExpiresAt) {
		next, err := slackpkg.NewOAuthClient(tok.ClientID).Refresh(ctx, tok)
		if err != nil {
			return err
		}
		access = next.AccessToken
	}
	return slackpkg.NewClient(access).Revoke(ctx)
}
