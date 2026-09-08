package worktree

import (
	"fmt"
	"path/filepath"
)

// PRName is the directory name of a PR review worktree: <repo>-pr-<number>.
// Every caller goes through here so the layout has one definition to change.
func PRName(repoShort string, prNumber int) string {
	return fmt.Sprintf("%s-pr-%d", repoShort, prNumber)
}

// PRPath is where a PR review worktree for repoShort lives under basePath.
func PRPath(basePath, repoShort string, prNumber int) string {
	return filepath.Join(basePath, PRName(repoShort, prNumber))
}
