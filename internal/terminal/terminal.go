package terminal

import (
	"fmt"

	"github.com/mgreau/zen/internal/ghostty"
	"github.com/mgreau/zen/internal/iterm"
)

// Terminal represents a terminal emulator that can open tabs/windows.
type Terminal interface {
	Name() string
	OpenTab(workDir, command string) error
	OpenTabWithResume(workDir, sessionID, claudeBin string) error
	OpenTabWithClaude(workDir, initialPrompt, claudeBin string) error
}

// NewTerminal creates a new terminal instance based on the terminal type.
func NewTerminal(terminalType string) (Terminal, error) {
	switch terminalType {
	case "iterm":
		return &ITermTerminal{}, nil
	case "ghostty":
		return &GhosttyTerminal{}, nil
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

func (t *ITermTerminal) OpenTabWithResume(workDir, sessionID, claudeBin string) error {
	return iterm.OpenTabWithResume(workDir, sessionID, claudeBin)
}

func (t *ITermTerminal) OpenTabWithClaude(workDir, initialPrompt, claudeBin string) error {
	return iterm.OpenTabWithClaude(workDir, initialPrompt, claudeBin)
}

// GhosttyTerminal wraps the Ghostty functions.
type GhosttyTerminal struct{}

func (t *GhosttyTerminal) Name() string {
	return "Ghostty"
}

func (t *GhosttyTerminal) OpenTab(workDir, command string) error {
	return ghostty.OpenTab(workDir, command)
}

func (t *GhosttyTerminal) OpenTabWithResume(workDir, sessionID, claudeBin string) error {
	return ghostty.OpenTabWithResume(workDir, sessionID, claudeBin)
}

func (t *GhosttyTerminal) OpenTabWithClaude(workDir, initialPrompt, claudeBin string) error {
	return ghostty.OpenTabWithClaude(workDir, initialPrompt, claudeBin)
}