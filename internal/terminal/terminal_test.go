package terminal

import (
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
)

func TestNewTerminal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantErr  bool
	}{
		{"iterm explicit", "iterm", "iTerm2", false},
		{"ghostty", "ghostty", "Ghostty", false},
		{"kitty", "kitty", "kitty", false},
		{"macos", "macos", "Terminal", false},
		{"tmux", "tmux", "tmux", false},
		{"empty is invalid", "", "", true},
		{"invalid terminal", "invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			term, err := NewTerminal(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTerminal(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if got := term.Name(); got != tt.wantName {
				t.Errorf("NewTerminal(%q).Name() = %q, want %q", tt.input, got, tt.wantName)
			}
		})
	}
}

func TestITermTerminalName(t *testing.T) {
	term := &ITermTerminal{}
	if got := term.Name(); got != "iTerm2" {
		t.Errorf("ITermTerminal.Name() = %q, want %q", got, "iTerm2")
	}
}

func TestGhosttyTerminalName(t *testing.T) {
	term := &GhosttyTerminal{}
	if got := term.Name(); got != "Ghostty" {
		t.Errorf("GhosttyTerminal.Name() = %q, want %q", got, "Ghostty")
	}
}

func TestKittyTerminalName(t *testing.T) {
	term := &KittyTerminal{}
	if got := term.Name(); got != "kitty" {
		t.Errorf("KittyTerminal.Name() = %q, want %q", got, "kitty")
	}
}

func TestMacOSTerminalName(t *testing.T) {
	term := &MacOSTerminal{}
	if got := term.Name(); got != "Terminal" {
		t.Errorf("MacOSTerminal.Name() = %q, want %q", got, "Terminal")
	}
}

func TestDescribe(t *testing.T) {
	tmux := &TmuxTerminal{}
	tests := []struct {
		term Terminal
		o    Outcome
		want string
	}{
		{&ITermTerminal{}, Opened, "iTerm2 tab opened"},
		{tmux, Opened, "tmux window opened"},
		{tmux, Reused, "Started in the worktree's existing tmux window"},
		{tmux, AlreadyRunning, "The agent is already running in the worktree's tmux window: switched to it"},
	}
	for _, tt := range tests {
		if got := Describe(tt.term, tt.o); got != tt.want {
			t.Errorf("Describe(%s, %d) = %q, want %q", tt.term.Name(), tt.o, got, tt.want)
		}
	}
}

func TestAgentFor(t *testing.T) {
	ag := agent.New(agent.Claude, "claude")
	if got := AgentFor(&TmuxTerminal{}, ag, config.NotifyConfig{}).StartCommand("", ""); !strings.Contains(got, "--settings") {
		t.Errorf("tmux: %q, want the status hooks", got)
	}
	off := false
	if got := AgentFor(&TmuxTerminal{}, ag, config.NotifyConfig{Terminal: &off}).StartCommand("", ""); strings.Contains(got, "hook notify") {
		t.Errorf("tmux with notify.terminal off: %q, want no notify hook", got)
	}
	if got := AgentFor(&TmuxTerminal{}, ag, config.NotifyConfig{}).StartCommand("", ""); !strings.Contains(got, "hook notify") {
		t.Errorf("tmux: %q, want the notify hook", got)
	}
	if got := AgentFor(&ITermTerminal{}, ag, config.NotifyConfig{}).StartCommand("", ""); got != "claude" {
		t.Errorf("iTerm: %q, want no hooks", got)
	}
}
