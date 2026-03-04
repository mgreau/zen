package ghostty

import (
	"fmt"
	"os"
	"os/exec"
)

// OpenTab opens a new Ghostty window and runs the given command.
// Note: Ghostty on macOS doesn't support creating tabs through AppleScript like iTerm2.
// This function attempts to create a new tab using UI scripting, with fallback to new window.
func OpenTab(workDir, command string) error {
	fullCmd := fmt.Sprintf("cd %q && %s", workDir, command)

	// Try to create a new tab using UI scripting (requires Ghostty to be open)
	// This is the best we can do given Ghostty's limited AppleScript support
	// Pass the command via env var to avoid AppleScript string escaping issues.
	tabScript := `
		tell application "Ghostty"
			activate
		end tell
		tell application "System Events"
			tell process "Ghostty"
				-- Try to create new tab with Command+T
				keystroke "t" using command down
				-- Small delay to allow tab creation
				delay 0.3
				-- Execute the command in the new tab
				keystroke (system attribute "ZEN_GHOSTTY_CMD")
				keystroke return
			end tell
		end tell
	`

	// Try UI scripting approach first
	cmd := exec.Command("osascript", "-e", tabScript)
	cmd.Env = append(os.Environ(), "ZEN_GHOSTTY_CMD="+fullCmd)
	if err := cmd.Run(); err == nil {
		// UI scripting worked - command was sent to new tab
		return nil
	}

	// Fallback to opening in new window if UI scripting fails
	// This happens if Ghostty isn't open or accessibility permissions are missing
	// Use Ghostty's -e flag to execute a shell command
	fallbackCmd := exec.Command("open", "-na", "Ghostty", "--args", "-e", "/bin/bash", "-c", fullCmd)
	out, err := fallbackCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("open: %w: %s", err, string(out))
	}
	return nil
}

// OpenTabWithResume opens a new Ghostty window to resume a Claude session.
func OpenTabWithResume(workDir, sessionID, claudeBin string) error {
	cmd := fmt.Sprintf("%s --resume %s", claudeBin, sessionID)
	return OpenTab(workDir, cmd)
}

// OpenTabWithClaude opens a new Ghostty window with Claude and an initial prompt.
func OpenTabWithClaude(workDir, initialPrompt, claudeBin string) error {
	cmd := fmt.Sprintf("%s %q", claudeBin, initialPrompt)
	return OpenTab(workDir, cmd)
}