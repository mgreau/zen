package terminal

import (
	"fmt"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ghostty"
	"github.com/mgreau/zen/internal/iterm"
	"github.com/mgreau/zen/internal/kitty"
	"github.com/mgreau/zen/internal/macos"
	"github.com/mgreau/zen/internal/tmux"
	"github.com/mgreau/zen/internal/zenbin"
)

// Terminal represents a terminal emulator that can open tabs/windows.
//
// The command to run is built by the agent layer (see internal/agent), so the
// terminal only needs to launch an arbitrary shell command in a new tab.
type Terminal interface {
	Name() string
	// Unit names what OpenTab opens, such as "tab" or "window".
	Unit() string
	OpenTab(workDir, command string) (Outcome, error)
}

// Outcome says what OpenTab did.
type Outcome int

const (
	// Opened means a new tab was opened and runs the command.
	Opened Outcome = iota
	// Reused means the worktree's existing tab was idle and now runs the
	// command.
	Reused
	// AlreadyRunning means the worktree's existing tab still runs something,
	// so the command was not run; OpenTab switched to that tab instead.
	AlreadyRunning
)

// Describe returns a one-line summary of what OpenTab did, for the user.
func Describe(t Terminal, o Outcome) string {
	switch o {
	case Reused:
		return fmt.Sprintf("Started in the worktree's existing %s %s", t.Name(), t.Unit())
	case AlreadyRunning:
		return fmt.Sprintf("The agent is already running in the worktree's %s %s: switched to it", t.Name(), t.Unit())
	default:
		return fmt.Sprintf("%s %s opened", t.Name(), t.Unit())
	}
}

// opened reports the outcome of a terminal that always opens a new tab.
func opened(err error) (Outcome, error) {
	return Opened, err
}

// BackgroundOpener is implemented by terminals that can open a tab without
// taking focus from the one the user is in.
type BackgroundOpener interface {
	OpenTabInBackground(workDir, command string) (Outcome, error)
}

// OpenInBackground opens command for workDir without taking focus when t
// supports it, and falls back to OpenTab otherwise. It is for tabs opened
// without the user asking at that moment, such as by the daemon.
func OpenInBackground(t Terminal, workDir, command string) (Outcome, error) {
	if b, ok := t.(BackgroundOpener); ok {
		return b.OpenTabInBackground(workDir, command)
	}
	return t.OpenTab(workDir, command)
}

// AgentFor returns ag set up for commands that run in t: in tmux, with the
// hooks that report the agent's state to its window and, unless n turns them
// off, send zen's own notifications (agent.WithTmuxStatus). Commands printed
// for the user to run themselves should use ag as is.
func AgentFor(t Terminal, ag agent.Agent, n config.NotifyConfig) agent.Agent {
	if _, ok := t.(*TmuxTerminal); !ok {
		return ag
	}
	zenBin := ""
	if n.TerminalEnabled() {
		if bin, err := zenbin.Path(); err == nil {
			zenBin = bin
		}
	}
	return agent.WithTmuxStatus(ag, zenBin)
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
	case "tmux":
		return &TmuxTerminal{}, nil
	default:
		return nil, fmt.Errorf("unsupported terminal type: %s", terminalType)
	}
}

// ITermTerminal wraps the iTerm functions.
type ITermTerminal struct{}

func (t *ITermTerminal) Name() string {
	return "iTerm2"
}

func (t *ITermTerminal) Unit() string { return "tab" }

func (t *ITermTerminal) OpenTab(workDir, command string) (Outcome, error) {
	return opened(iterm.OpenTab(workDir, command))
}

// GhosttyTerminal wraps the Ghostty functions.
type GhosttyTerminal struct{}

func (t *GhosttyTerminal) Name() string {
	return "Ghostty"
}

func (t *GhosttyTerminal) Unit() string { return "window" }

func (t *GhosttyTerminal) OpenTab(workDir, command string) (Outcome, error) {
	return opened(ghostty.OpenTab(workDir, command))
}

// KittyTerminal wraps the kitty functions.
type KittyTerminal struct{}

func (t *KittyTerminal) Name() string {
	return "kitty"
}

func (t *KittyTerminal) Unit() string { return "window" }

func (t *KittyTerminal) OpenTab(workDir, command string) (Outcome, error) {
	return opened(kitty.OpenTab(workDir, command))
}

// MacOSTerminal wraps the macOS Terminal.app functions.
type MacOSTerminal struct{}

func (t *MacOSTerminal) Name() string {
	return "Terminal"
}

func (t *MacOSTerminal) Unit() string { return "tab" }

func (t *MacOSTerminal) OpenTab(workDir, command string) (Outcome, error) {
	return opened(macos.OpenTab(workDir, command))
}

// TmuxTerminal opens each session in a window of the current tmux session,
// reusing the window zen already opened for the worktree.
type TmuxTerminal struct{}

func (t *TmuxTerminal) Name() string {
	return "tmux"
}

func (t *TmuxTerminal) Unit() string { return "window" }

func (t *TmuxTerminal) OpenTab(workDir, command string) (Outcome, error) {
	return tmuxOutcome(tmux.OpenWindow(workDir, command))
}

func (t *TmuxTerminal) OpenTabInBackground(workDir, command string) (Outcome, error) {
	return tmuxOutcome(tmux.OpenWindowDetached(workDir, command))
}

func tmuxOutcome(o tmux.Outcome, err error) (Outcome, error) {
	switch o {
	case tmux.Reused:
		return Reused, err
	case tmux.AlreadyRunning:
		return AlreadyRunning, err
	default:
		return Opened, err
	}
}
