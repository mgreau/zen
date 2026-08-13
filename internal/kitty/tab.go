// Package kitty opens windows in the kitty terminal emulator.
// Unlike the iTerm2 and Ghostty backends, which drive macOS via AppleScript,
// kitty is controlled through its own CLI, so this backend works on Linux
// (and anywhere else kitty runs).
package kitty

import (
	"fmt"
	"os/exec"
)

// OpenTab opens a new kitty OS window and runs the given command.
//
// It first tries kitty's remote control protocol (kitty @ launch
// --type=os-window), which opens the window from the running kitty instance.
// This requires zen to be invoked from inside kitty with allow_remote_control
// enabled in kitty.conf (or a socket configured via listen_on).
//
// If remote control is unavailable, it falls back to starting a new kitty
// instance, detached from zen's process.
func OpenTab(workDir, command string) error {
	// Exec into the user's shell after the command finishes, so the window ends up
	// in a real interactive shell (matching iTerm/Ghostty) instead of closing the
	// instant the command exits — which would also hide errors like claude missing
	// from PATH.
	fullCmd := fmt.Sprintf(`%s; exec "$SHELL"`, command)

	// Try remote control first: a new OS window from the running kitty instance.
	rcCmd := exec.Command("kitty", "@", "launch", "--type=os-window", "--cwd", workDir, "/bin/sh", "-c", fullCmd)
	if err := rcCmd.Run(); err == nil {
		return nil
	}

	// Fallback: a new kitty instance.
	// This happens if zen isn't running inside kitty or remote control is disabled.
	winCmd := exec.Command("kitty", "--detach", "--directory", workDir, "/bin/sh", "-c", fullCmd)
	out, err := winCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kitty: %w: %s", err, string(out))
	}
	return nil
}

// OpenTabWithResume opens a new kitty OS window to resume a Claude session.
func OpenTabWithResume(workDir, sessionID, claudeBin, model string) error {
	cmd := claudeBin
	if model != "" {
		cmd += fmt.Sprintf(" --model %s", model)
	}
	cmd += fmt.Sprintf(" --resume %s", sessionID)
	return OpenTab(workDir, cmd)
}

// OpenTabWithClaude opens a new kitty OS window with Claude and an initial prompt.
func OpenTabWithClaude(workDir, initialPrompt, claudeBin, model string) error {
	cmd := claudeBin
	if model != "" {
		cmd += fmt.Sprintf(" --model %s", model)
	}
	cmd += fmt.Sprintf(" %q", initialPrompt)
	return OpenTab(workDir, cmd)
}
