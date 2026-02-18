package iterm

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
)

// Tab color presets — pleasant palette for iTerm tab identification.
var palette = [][3]int{
	{66, 133, 244},  // blue
	{52, 168, 83},   // green
	{251, 188, 4},   // yellow
	{234, 67, 53},   // red
	{171, 71, 188},  // purple
	{0, 172, 193},   // teal
	{255, 112, 67},  // orange
	{124, 179, 66},  // lime
	{38, 166, 154},  // cyan
	{236, 64, 122},  // pink
}

// RandomColor returns an escape sequence for a random iTerm tab color.
func RandomColor() string {
	c := palette[rand.Intn(len(palette))]
	return fmt.Sprintf(
		`\e]6;1;bg;red;brightness;%d\a\e]6;1;bg;green;brightness;%d\a\e]6;1;bg;blue;brightness;%d\a`,
		c[0], c[1], c[2],
	)
}

// OpenTab opens a new iTerm2 tab, sets a random color, and runs the given command.
func OpenTab(workDir, command string) error {
	c := palette[rand.Intn(len(palette))]
	colorCmd := fmt.Sprintf(
		`printf '\e]6;1;bg;red;brightness;%d\a\e]6;1;bg;green;brightness;%d\a\e]6;1;bg;blue;brightness;%d\a'`,
		c[0], c[1], c[2],
	)
	fullCmd := fmt.Sprintf("cd %s && %s && %s", workDir, colorCmd, command)

	// Pass the shell command via env var to avoid AppleScript string escaping
	// issues with quotes and backslashes in printf escape sequences.
	script := `tell application "iTerm2"
    activate
    tell current window
        create tab with default profile
        tell current session of current tab
            write text (system attribute "ZEN_ITERM_CMD")
        end tell
    end tell
end tell`

	cmd := exec.Command("osascript", "-e", script)
	cmd.Env = append(os.Environ(), "ZEN_ITERM_CMD="+fullCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w: %s", err, string(out))
	}
	return nil
}

// OpenTabWithResume opens a new iTerm2 tab to resume a Claude session.
func OpenTabWithResume(workDir, sessionID, claudeBin string) error {
	cmd := fmt.Sprintf("%s --resume %s", claudeBin, sessionID)
	return OpenTab(workDir, cmd)
}

// OpenTabWithClaude opens a new iTerm2 tab with Claude and an initial prompt.
func OpenTabWithClaude(workDir, initialPrompt, claudeBin string) error {
	cmd := fmt.Sprintf("%s %q", claudeBin, initialPrompt)
	return OpenTab(workDir, cmd)
}
