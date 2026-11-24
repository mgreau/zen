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

// OwnedContextFiles returns context files that a successful Zen injection, or
// zen work new --context-file (see StageInitialPrompt), marked as generated.
// Files without the corresponding sentinel are not Zen owned, even when their
// names match Zen's context filenames.
func OwnedContextFiles(worktreePath string) []string {
	var paths []string
	if pathExists(filepath.Join(worktreePath, claudeContextSentinel)) {
		paths = append(paths, claudeContextFile, claudeContextSentinel)
	}
	if sentinel := filepath.Join(worktreePath, codexSentinel); pathExists(sentinel) {
		if owned := codexOwnedContextFile(sentinel); owned != "" {
			paths = append(paths, owned)
		}
		paths = append(paths, codexSentinel)
	}
	return append(paths, ownedInitialPrompt(worktreePath)...)
}

func codexOwnedContextFile(sentinel string) string {
	if data, err := os.ReadFile(sentinel); err == nil {
		switch strings.TrimSpace(string(data)) {
		case codexContextFile:
			return codexContextFile
		case codexSideContextFile:
			return codexSideContextFile
		}
	}

	// Empty legacy sentinels did not record which context path Zen created.
	// Claim neither possible file so cleanup preserves both conservatively.
	return ""
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
