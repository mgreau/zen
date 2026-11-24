package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// initialPromptFile is where StageInitialPrompt keeps a worktree's initial
	// prompt, relative to the worktree, inside the zen-owned .zen/ directory.
	initialPromptFile = ".zen/INITIAL_PROMPT.md"
	// initialPromptSentinel records the SHA-256 of the prompt zen wrote, so
	// cleanup can tell zen's copy from one the user has since edited.
	initialPromptSentinel = ".zen/.initial_prompt_staged"
)

// StageInitialPrompt writes prompt into the worktree for StartCommandFromFile
// and returns the file's absolute path. The file stays with the worktree, so
// the prompt can be read again later, and .zen/ is excluded from git. While
// the file is unchanged it counts as zen-generated (see OwnedContextFiles), so
// it doesn't stop the worktree from being removed.
func StageInitialPrompt(worktreePath string, prompt []byte) (string, error) {
	path := filepath.Join(worktreePath, initialPromptFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, prompt, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	sentinel := filepath.Join(worktreePath, initialPromptSentinel)
	if err := os.WriteFile(sentinel, []byte(sha256Hex(prompt)+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing initial prompt marker: %w", err)
	}
	addToGitExclude(worktreePath, ".zen/")
	return path, nil
}

// ownedInitialPrompt returns the initial-prompt files zen can remove: both
// while the prompt is as zen wrote it, the sentinel alone once the prompt is
// gone, and nothing when the prompt was edited or the sentinel is missing.
func ownedInitialPrompt(worktreePath string) []string {
	want, err := os.ReadFile(filepath.Join(worktreePath, initialPromptSentinel))
	if err != nil {
		return nil
	}
	prompt, err := os.ReadFile(filepath.Join(worktreePath, initialPromptFile))
	if os.IsNotExist(err) {
		return []string{initialPromptSentinel}
	}
	if err != nil || sha256Hex(prompt) != strings.TrimSpace(string(want)) {
		return nil
	}
	return []string{initialPromptFile, initialPromptSentinel}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
