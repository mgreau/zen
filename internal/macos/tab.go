// Package macos opens tabs in the macOS Terminal.app via AppleScript.
package macos

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OpenTab opens a new Terminal.app tab (or a new window if none exist)
// and runs the given command.
//
// Terminal has no AppleScript "create tab" command. Since High Sierra each tab
// is also its own AppleScript window (`count of tabs of front window` is 1),
// so `do script … in front window` reuses the current tab and `do script`
// with no target opens a real window. New tabs are created by clicking
// Shell → New Tab (same approach as ttab); if that fails, we open a window.
func OpenTab(workDir, command string) error {
	fullCmd := fmt.Sprintf("cd %q && %s", workDir, command)

	// Pass the shell command via env var to avoid AppleScript string escaping.
	tabScript := `tell application "Terminal"
	activate
	repeat 40 times
		if frontmost then exit repeat
		delay 0.05
	end repeat
	if (count of windows) is 0 then
		do script (system attribute "ZEN_MACOS_CMD")
		return
	end if
	set oldWinCount to count of windows
	set oldTTY to tty of selected tab of front window
end tell
tell application "System Events"
	tell process "Terminal"
		set frontmost to true
		tell menu 1 of menu item 2 of menu 1 of menu bar item 3 of menu bar 1
			click (first menu item whose value of attribute "AXMenuItemCmdChar" is "T" and value of attribute "AXMenuItemCmdModifiers" is 0)
		end tell
	end tell
end tell
repeat 20 times
	tell application "Terminal"
		set newTab to selected tab of front window
		if (count of windows) > oldWinCount or tty of newTab is not oldTTY then
			delay 0.1
			do script (system attribute "ZEN_MACOS_CMD") in newTab
			return
		end if
	end tell
	delay 0.05
end repeat
error "new tab was not created"`

	cmd := exec.Command("osascript", "-e", tabScript)
	cmd.Env = append(os.Environ(), "ZEN_MACOS_CMD="+fullCmd)
	if out, err := cmd.CombinedOutput(); err == nil {
		return nil
	} else {
		fmt.Fprintf(os.Stderr, "zen: could not open a Terminal tab (%s); opening a new window. Add Terminal to System Settings → Privacy & Security → Accessibility.\n",
			strings.TrimSpace(string(out)))
		_ = err
	}

	winScript := `tell application "Terminal"
	activate
	do script (system attribute "ZEN_MACOS_CMD")
end tell`
	cmd = exec.Command("osascript", "-e", winScript)
	cmd.Env = append(os.Environ(), "ZEN_MACOS_CMD="+fullCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w: %s", err, string(out))
	}
	return nil
}
