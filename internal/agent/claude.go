package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mgreau/zen/internal/session"
)

// claudeAgent drives Anthropic's Claude Code CLI. Session discovery and token
// parsing delegate to the internal/session package, which implements Claude's
// per-project ~/.claude/projects/<encoded-path>/*.jsonl layout.
type claudeAgent struct {
	bin string
}

func (a *claudeAgent) Kind() Kind  { return Claude }
func (a *claudeAgent) Bin() string { return a.bin }

func (a *claudeAgent) StartCommand(prompt, model string) string {
	cmd := a.bin
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	if prompt != "" {
		cmd += " " + shellQuote(prompt)
	}
	return cmd
}

func (a *claudeAgent) ResumeCommand(sessionID, model string) string {
	cmd := a.bin
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	return cmd + " --resume " + shellQuote(sessionID)
}

// ContextFile is CLAUDE.local.md so the repo's own CLAUDE.md is never touched.
func (a *claudeAgent) ContextFile() string { return "CLAUDE.local.md" }

func (a *claudeAgent) InjectContext(worktreePath, rendered string) (string, error) {
	ref := a.ContextFile()
	outPath := filepath.Join(worktreePath, ref)
	if err := os.WriteFile(outPath, []byte(rendered), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", outPath, err)
	}
	return ref, nil
}

// ContextPresent is true once CLAUDE.local.md exists. The repo's own CLAUDE.md
// is a separate file and never satisfies this check, but a repo that commits a
// CLAUDE.local.md (unusual — the file is meant to be local) is treated as
// already-injected.
func (a *claudeAgent) ContextPresent(worktreePath string) bool {
	_, err := os.Stat(filepath.Join(worktreePath, a.ContextFile()))
	return err == nil
}

// ReviewPrompt uses the installed /review-pr slash command. Claude reads
// CLAUDE.local.md automatically, so the prompt need not reference it.
func (a *claudeAgent) ReviewPrompt(string) string { return "/review-pr" }

func (a *claudeAgent) PromptsDir() string {
	return filepath.Join(os.Getenv("HOME"), ".claude", "commands")
}

func (a *claudeAgent) EnsurePrompt(name string, content []byte) (bool, error) {
	return ensurePromptFile(a.PromptsDir(), name, content)
}

func (a *claudeAgent) FindSessions(worktreePath string) ([]session.Session, error) {
	return session.FindSessions(worktreePath)
}

func (a *claudeAgent) ParseTokensTail(path string) (string, session.TokenUsage, error) {
	return session.ParseSessionDetailTail(path)
}

func (a *claudeAgent) ParseTokensFull(path string) (string, session.TokenUsage, error) {
	return session.ParseSessionDetailFull(path)
}

func (a *claudeAgent) CleanSessions(worktreePath string) (int, error) {
	sessions, _ := session.FindSessions(worktreePath)
	if len(sessions) == 0 {
		return 0, nil
	}
	dir := session.ProjectDir(worktreePath)
	if dir == "" {
		return 0, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return 0, err
	}
	return len(sessions), nil
}

func (a *claudeAgent) IsProcessRunning(sessionID string) bool {
	return session.IsProcessRunning(sessionID)
}

func (a *claudeAgent) ShortenModel(model string) string {
	return session.ShortenModel(model)
}
