// Package kitty opens tabs and windows in the kitty terminal emulator.
// Unlike the iTerm2 and Ghostty backends, which drive macOS via AppleScript,
// kitty is controlled through its own CLI, so this backend works on Linux
// (and anywhere else kitty runs).
package kitty

import (
	"fmt"
	"os/exec"
)

// OpenTab opens a new kitty tab and runs the given command.
//
// It first tries kitty's remote control protocol (kitty @ launch --type=tab),
// which creates a tab in the running kitty instance. This requires zen to be
// invoked from inside kitty with allow_remote_control enabled in kitty.conf
// (or a socket configured via listen_on).
//
// If remote control is unavailable, it falls back to opening a new kitty OS
// window, detached from zen's process.
func OpenTab(workDir, command string) error {
	fullCmd := fmt.Sprintf("cd %q && %s", workDir, command)

	// Try remote control first: a new tab in the current kitty instance.
	tabCmd := exec.Command("kitty", "@", "launch", "--type=tab", "--cwd", workDir, "/bin/sh", "-c", fullCmd)
	if err := tabCmd.Run(); err == nil {
		return nil
	}

	// Fallback: new kitty OS window.
	// This happens if zen isn't running inside kitty or remote control is disabled.
	winCmd := exec.Command("kitty", "--detach", "--directory", workDir, "/bin/sh", "-c", fullCmd)
	out, err := winCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kitty: %w: %s", err, string(out))
	}
	return nil
}

// OpenTabWithResume opens a new kitty tab to resume a Claude session.
func OpenTabWithResume(workDir, sessionID, claudeBin, model string) error {
	cmd := claudeBin
	if model != "" {
		cmd += fmt.Sprintf(" --model %s", model)
	}
	cmd += fmt.Sprintf(" --resume %s", sessionID)
	return OpenTab(workDir, cmd)
}

// OpenTabWithClaude opens a new kitty tab with Claude and an initial prompt.
func OpenTabWithClaude(workDir, initialPrompt, claudeBin, model string) error {
	cmd := claudeBin
	if model != "" {
		cmd += fmt.Sprintf(" --model %s", model)
	}
	cmd += fmt.Sprintf(" %q", initialPrompt)
	return OpenTab(workDir, cmd)
}
