// Package agent abstracts the AI coding agent that zen launches in worktrees.
//
// zen was originally hardwired to Claude Code. This package isolates every
// agent-specific behaviour behind the Agent interface so additional agents
// (currently Claude Code and OpenAI Codex) can be supported without touching
// the command, daemon, or MCP layers.
//
// The touchpoints an agent owns:
//   - the launch / resume shell commands (binary, model flag, resume syntax)
//   - the per-worktree context file it reads (CLAUDE.local.md vs AGENTS.md)
//   - the directory custom slash-command prompts are installed into
//   - how its sessions are discovered on disk and how token usage is parsed
package agent

import "github.com/mgreau/zen/internal/session"

// Kind identifies a supported agent.
type Kind string

const (
	// Claude is Anthropic's Claude Code CLI (the historical default).
	Claude Kind = "claude"
	// Codex is OpenAI's Codex CLI.
	Codex Kind = "codex"
)

// Valid reports whether k is a recognised agent kind.
func (k Kind) Valid() bool {
	return k == Claude || k == Codex
}

// Agent encapsulates everything zen needs to drive a specific coding agent.
type Agent interface {
	// Kind returns the agent identity.
	Kind() Kind
	// Bin returns the resolved executable name/path.
	Bin() string

	// StartCommand builds the shell command to launch the agent with an
	// optional initial prompt and model. An empty prompt or model is omitted.
	StartCommand(prompt, model string) string
	// ResumeCommand builds the shell command to resume a recorded session.
	ResumeCommand(sessionID, model string) string

	// ContextFile returns the default filename the agent reads project context
	// from inside a worktree (e.g. "CLAUDE.local.md", "AGENTS.md").
	ContextFile() string
	// InjectContext writes the rendered PR-review context into the worktree
	// following the agent's conventions and returns the worktree-relative file
	// reference the agent will read it from (which may differ from
	// ContextFile when a pre-existing file forced a fallback location).
	InjectContext(worktreePath, rendered string) (ref string, err error)
	// ContextPresent reports whether zen has already injected PR context into
	// the worktree. It must be true after InjectContext. Detection is
	// best-effort: an implementation may be unable to distinguish a
	// repo-shipped context file from an injected one (see the Claude
	// implementation); Codex uses a sentinel to disambiguate AGENTS.md.
	ContextPresent(worktreePath string) bool
	// ReviewPrompt returns the initial prompt used to start a PR review in a
	// worktree whose context has already been injected.
	ReviewPrompt(worktreePath string) string

	// PromptsDir returns the directory custom slash-command prompts live in.
	PromptsDir() string
	// EnsurePrompt installs a slash-command prompt (named without extension)
	// from the given content if it is not already present. It reports whether
	// a file was written.
	EnsurePrompt(name string, content []byte) (installed bool, err error)

	// FindSessions returns the agent's sessions for a worktree, newest first.
	FindSessions(worktreePath string) ([]session.Session, error)
	// ParseTokensTail extracts the model and token usage cheaply (tail/recent).
	ParseTokensTail(path string) (model string, tokens session.TokenUsage, err error)
	// ParseTokensFull extracts the model and accurate token usage (whole file).
	ParseTokensFull(path string) (model string, tokens session.TokenUsage, err error)
	// CleanSessions removes the agent's session files for a worktree and
	// returns the number removed.
	CleanSessions(worktreePath string) (int, error)
	// IsProcessRunning best-effort reports whether a process for sessionID is alive.
	IsProcessRunning(sessionID string) bool
	// ShortenModel renders a model identifier in compact form for display.
	ShortenModel(model string) string
}

// New constructs an Agent for the given kind. bin overrides the default
// executable name when non-empty. Unknown kinds fall back to Claude so callers
// never get a nil agent.
func New(kind Kind, bin string) Agent {
	switch kind {
	case Codex:
		if bin == "" {
			bin = "codex"
		}
		return &codexAgent{bin: bin}
	default:
		if bin == "" {
			bin = "claude"
		}
		return &claudeAgent{bin: bin}
	}
}
