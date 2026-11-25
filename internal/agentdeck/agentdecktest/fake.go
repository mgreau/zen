// Package agentdecktest installs a fake agent-deck executable for tests of
// packages that reach agent-deck through a terminal.
package agentdecktest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/agentdeck"
)

// Install points agentdeck.Bin at a script that lists one exited zen session
// for workDir, reports it exited to session show and accepts its removal.
// It returns a function that reads back the argv of every call, one call per
// line.
func Install(t *testing.T, workDir string) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls")
	row := `[{"id": "zen-1", "title": "` + filepath.Base(workDir) + `", "path": "` + workDir + `", "group": "` + agentdeck.Group + `"}]`
	script := `#!/bin/sh
echo "$*" >> '` + logFile + `'
case "$1 $2" in
"list -json") echo '` + row + `' ;;
"session show") echo '{"id": "zen-1", "tool": "shell", "status": "idle", "substate": "process-exited"}' ;;
"session remove") echo '{"success": true, "id": "zen-1"}' ;;
*) echo '{"success": false, "error": "unexpected call"}'; exit 1 ;;
esac
`
	bin := filepath.Join(dir, "agent-deck")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := agentdeck.Bin
	agentdeck.Bin = bin
	t.Cleanup(func() { agentdeck.Bin = orig })

	return func() []string {
		data, err := os.ReadFile(logFile)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
}

// Removed is the call sequence Install's script sees when the session is
// found, checked and removed.
var Removed = []string{"list -json", "session show -json zen-1", "session remove -force -json zen-1"}
