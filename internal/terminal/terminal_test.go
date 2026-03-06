package terminal

import (
	"testing"
)

func TestNewTerminal(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantName    string
		wantErr     bool
	}{
		{"iterm explicit", "iterm", "iTerm2", false},
		{"ghostty", "ghostty", "Ghostty", false},
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
