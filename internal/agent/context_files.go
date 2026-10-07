package agent

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	claudeContextFile     = "CLAUDE.local.md"
	claudeContextSentinel = ".zen/.claude_context_injected"
)

// OwnedContextFiles returns context files that a successful Zen injection
// marked as generated. Files without the corresponding sentinel are not Zen
// owned, even when their names match Zen's context filenames.
func OwnedContextFiles(worktreePath string) []string {
	var paths []string
	if pathExists(filepath.Join(worktreePath, claudeContextSentinel)) {
		paths = append(paths, claudeContextFile, claudeContextSentinel)
	}
	if sentinel := filepath.Join(worktreePath, codexSentinel); pathExists(sentinel) {
		if owned := codexOwnedContextFile(worktreePath, sentinel); owned != "" {
			paths = append(paths, owned)
		}
		paths = append(paths, codexSentinel)
	}
	return paths
}

func codexOwnedContextFile(worktreePath, sentinel string) string {
	data, err := os.ReadFile(sentinel)
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(data)) {
	case codexContextFile:
		return codexContextFile
	case codexSideContextFile:
		return codexSideContextFile
	case "":
		// Sentinels written before Zen recorded the injected path are empty,
		// so they cannot say whether Zen or the user created AGENTS.md; leave
		// it unclaimed so cleanup keeps it. .zen/PR_CONTEXT.md is different:
		// it lives in Zen's own excluded .zen/ directory and is not a file
		// users author, so claim it. Otherwise every review worktree from
		// those builds looks dirty and is never cleaned up.
		if pathExists(filepath.Join(worktreePath, codexSideContextFile)) {
			return codexSideContextFile
		}
	}
	return ""
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
