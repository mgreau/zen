package terminal

import (
	"fmt"

	"github.com/mgreau/zen/internal/ghostty"
	"github.com/mgreau/zen/internal/iterm"
	"github.com/mgreau/zen/internal/kitty"
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
