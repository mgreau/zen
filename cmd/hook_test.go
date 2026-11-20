package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/worktree"
)

func TestHookNotification(t *testing.T) {
	events := config.DefaultTerminalEvents
	tests := []struct {
		name     string
		in       hookInput
		wantBody string // "" means no notification
	}{
		{"turn ended", hookInput{Event: "Stop", LastAssistantMessage: "Done: tests pass."}, "Done: tests pass."},
		{"turn ended, no text", hookInput{Event: "Stop"}, "Turn finished"},
		{"stop hook continuing", hookInput{Event: "Stop", StopHookActive: true, LastAssistantMessage: "x"}, ""},
		{"turn failed", hookInput{Event: "StopFailure", Error: "rate_limit"}, "Turn failed: rate_limit"},
		{"permission", hookInput{Event: "Notification", NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"}, "Claude needs your permission to use Bash"},
		{"idle, not configured", hookInput{Event: "Notification", NotificationType: "idle_prompt", Message: "Claude is waiting for your input"}, ""},
		{"empty message", hookInput{Event: "Notification", NotificationType: "permission_prompt"}, ""},
		{"no type, as older Claude sent", hookInput{Event: "Notification", Message: "Claude needs your permission to use Bash"}, "Claude needs your permission to use Bash"},
		{"no type, idle notice", hookInput{Event: "Notification", Message: "Claude is waiting for your input"}, ""},
	}
	for _, tt := range tests {
		n, ok := hookNotification(tt.in, events, "app#42 · Fix it")
		if tt.wantBody == "" {
			if ok {
				t.Errorf("%s: got %+v, want no notification", tt.name, n)
			}
			continue
		}
		if !ok || n.Body != tt.wantBody || n.Title != "Claude · app#42 · Fix it" {
			t.Errorf("%s: got %+v, %v; want body %q", tt.name, n, ok, tt.wantBody)
		}
	}

	if _, ok := hookNotification(hookInput{Event: "Stop", LastAssistantMessage: "x"}, []string{}, "x"); ok {
		t.Error("empty terminal_events still notified")
	}
}

func TestWorktreeFor(t *testing.T) {
	wts := []worktree.Worktree{
		{Path: "/w/app", Name: "app"},
		{Path: "/w/app-pr-42", Name: "app-pr-42"},
		{Path: "/w/app/nested", Name: "nested"},
	}
	for dir, want := range map[string]string{
		"/w/app":             "app",
		"/w/app/src/x":       "app",
		"/w/app-pr-42/cmd":   "app-pr-42",
		"/w/app/nested/deep": "nested",
	} {
		if wt, ok := worktreeFor(wts, dir); !ok || wt.Name != want {
			t.Errorf("worktreeFor(%s) = %s, %v; want %s", dir, wt.Name, ok, want)
		}
	}
	if _, ok := worktreeFor(wts, "/w/application"); ok {
		t.Error("worktreeFor matched a sibling with a common prefix")
	}
	if got := worktreeLabel(nil, "/some/where/proj"); got != "proj" {
		t.Errorf("worktreeLabel outside any worktree = %q, want proj", got)
	}
}

func TestSessionCommands(t *testing.T) {
	none, dashboard := "", "zen dashboard"
	tests := []struct {
		name                string
		cfg                 config.Config
		wantFirst, wantAgts string
	}{
		{"defaults", config.Config{Agent: "claude"}, "zen inbox", ""},
		{"own first command", config.Config{Agent: "claude", Tmux: config.TmuxConfig{FirstWindowCommand: &dashboard}}, "zen dashboard", ""},
		{"no first command", config.Config{Agent: "claude", Tmux: config.TmuxConfig{FirstWindowCommand: &none}}, "", ""},
		{"agents window", config.Config{Agent: "claude", Tmux: config.TmuxConfig{AgentsWindow: true}}, "zen inbox", "claude agents"},
		{"codex", config.Config{Agent: "codex", Tmux: config.TmuxConfig{AgentsWindow: true}}, "zen inbox", "codex agents"},
		{"own agents command", config.Config{Agent: "claude", Tmux: config.TmuxConfig{AgentsWindow: true, AgentsWindowCommand: "claude"}}, "zen inbox", "claude"},
		{"agents command, window off", config.Config{Agent: "claude", Tmux: config.TmuxConfig{AgentsWindowCommand: "claude"}}, "zen inbox", ""},
	}
	for _, tt := range tests {
		first, agents := sessionCommands(&tt.cfg, "zen")
		if first != tt.wantFirst || agents != tt.wantAgts {
			t.Errorf("%s: sessionCommands = %q, %q; want %q, %q", tt.name, first, agents, tt.wantFirst, tt.wantAgts)
		}
	}
}

func TestLogHookErrorCapped(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	path := filepath.Join(config.StateDir(), "tmux-hook.log")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, make([]byte, maxHookLog+1), 0o644)
	logHookError("session-created", errors.New("boom"))
	info, err := os.Stat(path)
	if err != nil || info.Size() > 200 {
		t.Errorf("log after passing its cap: %v, %v bytes", err, info.Size())
	}
}
