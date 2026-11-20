package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWithTmuxStatus(t *testing.T) {
	plain := New(Claude, "claude")
	ag := WithTmuxStatus(plain, "")
	quoted := shellQuote(tmuxStatusSettings(""))

	if got, want := ag.StartCommand("/review-pr", ""), "claude --settings "+quoted+" '/review-pr'"; got != want {
		t.Errorf("StartCommand =\n  %s\nwant\n  %s", got, want)
	}
	if got, want := ag.ResumeCommand("abc", "opus"), "claude --settings "+quoted+" --model 'opus' --resume 'abc'"; got != want {
		t.Errorf("ResumeCommand =\n  %s\nwant\n  %s", got, want)
	}

	// The original agent is unchanged, for commands printed to the user.
	if got := plain.StartCommand("", ""); got != "claude" {
		t.Errorf("plain StartCommand after WithTmuxStatus = %q", got)
	}
	// Codex has no hooks and is returned as is.
	if got := WithTmuxStatus(New(Codex, "codex"), "/bin/zen").StartCommand("", ""); got != "codex" {
		t.Errorf("Codex StartCommand with WithTmuxStatus = %q", got)
	}
}

func TestTmuxStatusSettings(t *testing.T) {
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(tmuxStatusSettings("")), &s); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SessionStart":       "waiting",
		"Notification":       "waiting",
		"Stop":               "waiting",
		"StopFailure":        "waiting",
		"UserPromptSubmit":   "running",
		"PostToolUse":        "running",
		"PostToolUseFailure": "running",
		"SessionEnd":         "stopped",
	}
	if len(s.Hooks) != len(want) {
		t.Errorf("hooks for %d events, want %d", len(s.Hooks), len(want))
	}
	for event, status := range want {
		groups := s.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s: %+v, want one hook", event, groups)
		}
		h := groups[0].Hooks[0]
		// SessionEnd gives the status up; the others claim it.
		flag := "@zen-hooks 1"
		if status == "stopped" {
			flag = "-u -t \"$TMUX_PANE\" @zen-hooks"
		}
		if h.Type != "command" || !strings.Contains(h.Command, "@zen-status "+status+" ") ||
			!strings.Contains(h.Command, flag) {
			t.Errorf("%s hook = %+v, want it to set %s and %s", event, h, status, flag)
		}
	}
	if m := s.Hooks["Notification"][0].Matcher; m != waitingNotifications {
		t.Errorf("Notification status matcher = %q, want the waiting types", m)
	}
	if m := s.Hooks["SessionStart"][0].Matcher; strings.Contains(m, "compact") {
		t.Errorf("SessionStart matcher %q includes compact, which happens mid-turn", m)
	}
}

// hookCommand returns the command the generated settings run for event.
func hookCommand(t *testing.T, event string) string {
	t.Helper()
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(tmuxStatusSettings("")), &s); err != nil {
		t.Fatal(err)
	}
	return s.Hooks[event][0].Hooks[0].Command
}

// Outside tmux the hook must succeed and do nothing, even without tmux.
func TestTmuxStatusHookOutsideTmux(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", hookCommand(t, "Stop"))
	cmd.Env = []string{"PATH=/nonexistent"}
	if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
		t.Errorf("hook outside tmux: err=%v out=%q", err, out)
	}
}

// Inside a tmux pane, the hook marks the pane's window.
func TestTmuxStatusHookInTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("", "zt") // short: socket paths are length-limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	t.Setenv("TMUX", "")
	tm := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", sock, "-f", "/dev/null"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	tm("new-session", "-d", "-s", "t")
	defer exec.Command("tmux", "-S", sock, "kill-server").Run()

	// The pane's environment carries $TMUX and $TMUX_PANE, as Claude's would.
	tm("send-keys", "-t", "t", hookCommand(t, "Stop")+"; tmux wait-for -S done", "Enter")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "tmux", "-S", sock, "wait-for", "done").Run(); err != nil {
		t.Fatalf("waiting for the hook to run: %v", err)
	}
	if got := tm("show-options", "-w", "-v", "-t", "t", "@zen-status"); got != "waiting" {
		t.Errorf("@zen-status after Stop hook = %q, want waiting", got)
	}
	if got := tm("show-options", "-w", "-v", "-t", "t", "@zen-hooks"); got != "1" {
		t.Errorf("@zen-hooks after Stop hook = %q, want 1", got)
	}
}

func TestTmuxStatusSettingsWithNotify(t *testing.T) {
	var s struct {
		Channel string `json:"preferredNotifChannel"`
		Hooks   map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(tmuxStatusSettings("/opt/my zen/zen")), &s); err != nil {
		t.Fatal(err)
	}
	if s.Channel != "notifications_disabled" {
		t.Errorf("preferredNotifChannel = %q, want notifications_disabled", s.Channel)
	}
	for event, want := range map[string]bool{"Notification": true, "Stop": true, "StopFailure": true, "SessionStart": false, "UserPromptSubmit": false, "PostToolUse": false} {
		got := false
		for _, g := range s.Hooks[event] {
			for _, h := range g.Hooks {
				got = got || (h.Command == `'/opt/my zen/zen' hook notify` && h.Async)
			}
		}
		if got != want {
			t.Errorf("%s: notify hook present = %v, want %v (%+v)", event, got, want, s.Hooks[event])
		}
	}
	// The notify hook must not inherit the status hook's matcher, or
	// notify.terminal_events could not pick other types.
	if n := s.Hooks["Notification"]; len(n) != 2 || len(n[1].Hooks) != 1 {
		t.Errorf("Notification groups = %+v, want the notify hook in a group of its own", n)
	}

	// Without zen's notifications, Claude keeps its own.
	if strings.Contains(tmuxStatusSettings(""), "preferredNotifChannel") {
		t.Error("settings without zenBin turn off Claude's notifications")
	}
}

func TestAgentsCommand(t *testing.T) {
	if got := New(Claude, "/opt/claude").AgentsCommand(); got != "/opt/claude agents" {
		t.Errorf("Claude AgentsCommand = %q", got)
	}
	if got := New(Codex, "codex").AgentsCommand(); got != "codex agents" {
		t.Errorf("Codex AgentsCommand = %q", got)
	}
	// The agent view is not a session zen opened: no status hooks.
	if got := WithTmuxStatus(New(Claude, "claude"), "/bin/zen").AgentsCommand(); got != "claude agents" {
		t.Errorf("AgentsCommand with tmux status = %q", got)
	}
}
