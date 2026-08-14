// Package kitty opens windows in the kitty terminal emulator.
// Unlike the iTerm2 and Ghostty backends, which drive macOS via AppleScript,
// kitty is controlled through its own CLI, so this backend works on Linux
// (and anywhere else kitty runs).
package kitty

import (
	"fmt"
	"os"
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
	// Try remote control first: a new OS window from the running kitty instance.
	// Runs the command through the user's own shell so it matches their normal
	// session. --hold keeps the window open after the command exits, so a
	// failure (e.g. claude missing from PATH) stays readable instead of
	// flashing the window shut.
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	rcCmd := exec.Command("kitty", "@", "launch", "--type=os-window", "--hold", "--cwd", workDir, shellPath, "-c", command)
	if err := rcCmd.Run(); err == nil {
		return nil
	}

	// Fallback: a new kitty instance.
	// This happens if zen isn't running inside kitty or remote control is disabled.
	winCmd := exec.Command("kitty", "--hold", "--detach", "--directory", workDir, "/bin/sh", "-c", command)
	out, err := winCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kitty: %w: %s", err, string(out))
	}
	return nil
}
