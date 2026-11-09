package terminal

import (
	"fmt"

	"github.com/mgreau/zen/internal/agentdeck"
	"github.com/mgreau/zen/internal/ghostty"
	"github.com/mgreau/zen/internal/iterm"
	"github.com/mgreau/zen/internal/kitty"
	"github.com/mgreau/zen/internal/macos"
)

// Terminal represents a terminal emulator that can open tabs/windows.
//
// The command to run is built by the agent layer (see internal/agent), so the
// terminal only needs to launch an arbitrary shell command in a new tab.
type Terminal interface {
	Name() string
	OpenTab(workDir, command string) error
}

// NewTerminal creates a new terminal instance based on the terminal type.
func NewTerminal(terminalType string) (Terminal, error) {
	switch terminalType {
	case "iterm":
		return &ITermTerminal{}, nil
	case "ghostty":
		return &GhosttyTerminal{}, nil
	case "kitty":
		return &KittyTerminal{}, nil
	case "macos":
		return &MacOSTerminal{}, nil
	case "agent-deck":
		return &AgentDeckTerminal{}, nil
	default:
		return nil, fmt.Errorf("unsupported terminal type: %s", terminalType)
	}
}

// ITermTerminal wraps the iTerm functions.
type ITermTerminal struct{}

func (t *ITermTerminal) Name() string {
	return "iTerm2"
}

func (t *ITermTerminal) OpenTab(workDir, command string) error {
	return iterm.OpenTab(workDir, command)
}

// GhosttyTerminal wraps the Ghostty functions.
type GhosttyTerminal struct{}

func (t *GhosttyTerminal) Name() string {
	return "Ghostty"
}

func (t *GhosttyTerminal) OpenTab(workDir, command string) error {
	return ghostty.OpenTab(workDir, command)
}

// KittyTerminal wraps the kitty functions.
type KittyTerminal struct{}

func (t *KittyTerminal) Name() string {
	return "kitty"
}

func (t *KittyTerminal) OpenTab(workDir, command string) error {
	return kitty.OpenTab(workDir, command)
}

// MacOSTerminal wraps the macOS Terminal.app functions.
type MacOSTerminal struct{}

func (t *MacOSTerminal) Name() string {
	return "Terminal"
}

func (t *MacOSTerminal) OpenTab(workDir, command string) error {
	return macos.OpenTab(workDir, command)
}

// AgentDeckTerminal opens sessions in agent-deck rather than a terminal
// emulator. It is not a terminal strictly, but it takes the same workDir and
// command, so it slots in behind the same interface.
type AgentDeckTerminal struct{}

func (t *AgentDeckTerminal) Name() string {
	return "agent-deck"
}

func (t *AgentDeckTerminal) OpenTab(workDir, command string) error {
	return agentdeck.OpenTab(workDir, command)
}
