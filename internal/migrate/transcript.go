// Package migrate converts coding-agent sessions between Claude Code and
// OpenAI Codex so work started with one agent can be resumed with the other.
//
// The two directions are deliberately asymmetric:
//
//   - Codex -> Claude: Claude Code has no import machinery, so zen translates
//     the Codex rollout itself into a Claude-format session file placed where
//     `claude --resume` discovers it (see codex_read.go / claude_write.go).
//   - Claude -> Codex: Codex ships its own Claude Code importer, so zen drives
//     it over the `codex app-server` JSON-RPC API and never writes Codex's
//     files itself (see codex_import.go).
//
// Both session formats are undocumented internals of their CLIs. The record
// shapes handled here were verified against Claude Code 2.1.200 and Codex CLI
// 0.142.0; unknown record types are skipped rather than failing the migration.
package migrate

// MsgKind classifies a transcript message.
type MsgKind int

const (
	// UserText is a plain user message.
	UserText MsgKind = iota
	// AssistantText is a plain assistant message.
	AssistantText
	// ToolCall is an assistant tool invocation. In a well-formed transcript it
	// is immediately followed by its ToolResult (readers guarantee adjacency,
	// synthesizing a placeholder result when the source recorded none).
	ToolCall
	// ToolResult carries the output of the preceding ToolCall.
	ToolResult
)

// Message is one conversation step in the agent-neutral transcript.
type Message struct {
	Kind MsgKind
	// Text holds the message body for UserText/AssistantText and the output
	// for ToolResult.
	Text string
	// Time is the source record's RFC3339 timestamp, empty if unknown.
	Time string

	// ToolName and ToolInput describe a ToolCall in the source agent's terms
	// (e.g. Codex "exec_command" with {"cmd": ...}).
	ToolName  string
	ToolInput map[string]any
	// CallID pairs a ToolCall with its ToolResult.
	CallID string
}

// Transcript is the neutral intermediate representation of a session.
type Transcript struct {
	// SourceID is the session/thread id in the source agent.
	SourceID string
	// Cwd is the working directory the source session recorded.
	Cwd string
	// GitBranch is the branch the source session recorded, if any.
	GitBranch string
	Messages  []Message
}
