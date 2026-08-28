//go:build e2e && darwin

package macos

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Not part of `go test ./...` or `go build` (build tag e2e+darwin).
// Run: ZEN_E2E_MACOS=1 go test -tags e2e ./internal/macos -run TestOpenTabE2E
//
// A marker file alone is not enough: `do script … in front window` reuses the
// selected tab, so touch still succeeds. On current macOS each tab is its own
// AppleScript window, so a new window id means a new tab (or a new window).
func TestOpenTabE2E(t *testing.T) {
	if os.Getenv("ZEN_E2E_MACOS") == "" {
		t.Skip("set ZEN_E2E_MACOS=1 to open Terminal.app")
	}

	before := terminalWindowIDs(t)

	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	if err := OpenTab(dir, "touch opened"); err != nil {
		t.Fatalf("OpenTab: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			after := terminalWindowIDs(t)
			for id := range after {
				if _, existed := before[id]; !existed {
					return
				}
			}
			t.Fatalf("created %s but reused an existing Terminal session (window ids %v); OpenTab must open a new tab or window", marker, mapsKeys(after))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Terminal.app did not create %s within 15s (Automation for osascript → Terminal, and Accessibility for New Tab?)", marker)
}

func terminalWindowIDs(t *testing.T) map[string]struct{} {
	t.Helper()
	// Do not tell app "Terminal" unless it is already running — that would launch it.
	script := `if application "Terminal" is running then
	tell application "Terminal"
		if (count of windows) is 0 then return ""
		set ids to {}
		repeat with w in windows
			set end of ids to (id of w as text)
		end repeat
		return ids
	end tell
end if
return ""`
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("listing Terminal windows: %v: %s", err, out)
	}
	ids := make(map[string]struct{})
	for _, id := range strings.Split(strings.TrimSpace(string(out)), ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func mapsKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
