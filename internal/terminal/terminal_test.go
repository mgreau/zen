package terminal

import (
	"reflect"
	"testing"

	"github.com/mgreau/zen/internal/agentdeck/agentdecktest"
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
		{"agent-deck", "agent-deck", "agent-deck", false},
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

func TestForgetSession(t *testing.T) {
	calls := agentdecktest.Install(t, "/src/zen-pr-32")

	for _, other := range []string{"iterm", "ghostty", "kitty", "macos", "tmux"} {
		if removed, err := ForgetSession(other, "/src/zen-pr-32"); err != nil || removed {
			t.Errorf("ForgetSession(%q) = %v, %v; want a no-op", other, removed, err)
		}
	}
	if got := calls(); got != nil {
		t.Fatalf("non-agent-deck terminals called agent-deck: %q", got)
	}

	removed, err := ForgetSession("agent-deck", "/src/zen-pr-32")
	if err != nil || !removed {
		t.Fatalf("ForgetSession(agent-deck) = %v, %v; want true, nil", removed, err)
	}
	if got := calls(); !reflect.DeepEqual(got, agentdecktest.Removed) {
		t.Errorf("agent-deck calls = %q, want %q", got, agentdecktest.Removed)
	}
}
