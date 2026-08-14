package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/mgreau/zen/internal/session"
)

// claudeFormatVersion is the Claude Code version the synthesized record shape
// was last verified against (a required-looking but tolerant envelope field).
const claudeFormatVersion = "2.1.200"

// claudeMigrationModel is stamped on synthesized assistant messages. Resume
// reads the session's model from the transcript and hangs indefinitely when
// no assistant message carries one (verified on 2.1.200); the value only
// serves as the resumed session's default-model hint.
const claudeMigrationModel = "claude-sonnet-5"

// maxClaudeSessionBytes caps a synthesized session file. Claude Code's resume
// fast path discards everything before the last compact boundary once a file
// exceeds 5,242,880 bytes; staying well under keeps the whole history visible.
const maxClaudeSessionBytes = 4 << 20

// maxToolResultBytes truncates a single migrated tool output. Full command
// output rarely matters after a hand-off and dominates file size otherwise.
const maxToolResultBytes = 32 << 10

// codexShellTools are the Codex tool names translated to Claude's native Bash
// tool. Anything else is flattened to text: Claude has no equivalent tool, and
// an unknown tool name in history is more confusing than a textual trace.
var codexShellTools = map[string]bool{
	"exec_command": true,
	"shell":        true,
	"local_shell":  true,
}

// claudeRecord is one line of a Claude Code session file. Field set per the
// minimal spec verified against Claude Code 2.1.200: type/uuid/message are
// required, parentUuid chains the records, sessionId must match the filename,
// and isSidechain must be false for the session to be resumable.
type claudeRecord struct {
	ParentUUID  *string        `json:"parentUuid"`
	IsSidechain bool           `json:"isSidechain"`
	UserType    string         `json:"userType"`
	Cwd         string         `json:"cwd"`
	SessionID   string         `json:"sessionId"`
	Version     string         `json:"version"`
	GitBranch   string         `json:"gitBranch"`
	Type        string         `json:"type"`
	Message     map[string]any `json:"message"`
	UUID        string         `json:"uuid"`
	Timestamp   string         `json:"timestamp"`
}

// CodexToClaude migrates a Codex rollout into a new Claude Code session for
// the worktree and returns the session id to pass to `claude --resume`.
func CodexToClaude(rolloutPath, worktreePath string) (sessionID string, err error) {
	t, err := ReadCodexRollout(rolloutPath)
	if err != nil {
		return "", err
	}
	return WriteClaudeSession(t, worktreePath)
}

// WriteClaudeSession renders a transcript as a Claude Code session file under
// ~/.claude/projects/<munged worktree>/<session-id>.jsonl and returns the
// session id. The worktree must exist: Claude derives the directory name from
// the symlink-resolved path, and resume only finds files in that exact dir.
func WriteClaudeSession(t *Transcript, worktreePath string) (string, error) {
	cwd := worktreePath
	if resolved, err := filepath.EvalSymlinks(worktreePath); err == nil {
		cwd = resolved
	} else {
		return "", fmt.Errorf("resolving worktree path (must exist for Claude to find the session): %w", err)
	}

	bodies := transcriptToClaudeBodies(t)
	bodies = capBodies(bodies)

	sessionID := uuid.NewString()
	dir := session.ProjectDirPath(worktreePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating Claude project dir: %w", err)
	}

	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("creating session file: %w", err)
	}
	defer f.Close()

	var parent *string
	// Records missing a source timestamp (preamble, truncation note) inherit
	// the nearest known one so timestamps never jump backwards mid-file.
	lastTime := time.Now().UTC()
	for _, b := range bodies {
		if ts, err := time.Parse(time.RFC3339, b.time); err == nil {
			lastTime = ts
			break
		}
	}
	enc := json.NewEncoder(f)
	for _, b := range bodies {
		if ts, err := time.Parse(time.RFC3339, b.time); err == nil {
			lastTime = ts
		}
		rec := claudeRecord{
			ParentUUID:  parent,
			IsSidechain: false,
			UserType:    "external",
			Cwd:         cwd,
			SessionID:   sessionID,
			Version:     claudeFormatVersion,
			GitBranch:   t.GitBranch,
			Type:        b.recordType,
			Message:     b.message,
			UUID:        uuid.NewString(),
			Timestamp:   lastTime.Format(time.RFC3339),
		}
		if err := enc.Encode(rec); err != nil {
			os.Remove(path)
			return "", fmt.Errorf("writing session file: %w", err)
		}
		u := rec.UUID
		parent = &u
	}
	return sessionID, nil
}

// recordBody is a session record before the envelope (uuid chain, timestamps)
// is applied — kept separate so size capping can drop records and the chain
// is still built over exactly what gets written.
type recordBody struct {
	recordType string // "user" or "assistant"
	message    map[string]any
	time       string
	// pairedWithPrev marks a tool_result that must not survive without the
	// tool_use record before it (and vice versa) when capping drops records.
	pairedWithPrev bool
}

func transcriptToClaudeBodies(t *Transcript) []recordBody {
	preamble := fmt.Sprintf(
		"[Session migrated to Claude Code from an OpenAI Codex CLI session (thread %s) by zen. "+
			"The conversation below was originally conducted with Codex: tool calls were translated "+
			"and may not match Claude Code's native tools exactly.]", t.SourceID)
	bodies := []recordBody{{
		recordType: "user",
		message:    map[string]any{"role": "user", "content": preamble},
	}}

	toolIDs := map[string]string{} // Codex call_id -> synthesized Claude tool_use id
	flattened := map[string]bool{} // call_ids whose call was flattened to text
	for i, m := range t.Messages {
		switch m.Kind {
		case UserText:
			bodies = append(bodies, recordBody{
				recordType: "user",
				message:    map[string]any{"role": "user", "content": m.Text},
				time:       m.Time,
			})
		case AssistantText:
			bodies = append(bodies, recordBody{
				recordType: "assistant",
				message:    assistantMessage([]map[string]any{{"type": "text", "text": m.Text}}, i),
				time:       m.Time,
			})
		case ToolCall:
			if codexShellTools[m.ToolName] {
				id := fmt.Sprintf("toolu_zenmigrate%04d", i)
				toolIDs[m.CallID] = id
				bodies = append(bodies, recordBody{
					recordType: "assistant",
					message: assistantMessage([]map[string]any{{
						"type":  "tool_use",
						"id":    id,
						"name":  "Bash",
						"input": shellToolInput(m.ToolInput),
					}}, i),
					time: m.Time,
				})
			} else {
				// Mirror the wrapper Codex's own importer uses in the other
				// direction, so flattened calls are clearly marked as history.
				input, _ := json.Marshal(m.ToolInput)
				flattened[m.CallID] = true
				bodies = append(bodies, recordBody{
					recordType: "assistant",
					message: assistantMessage([]map[string]any{{
						"type": "text",
						"text": fmt.Sprintf("[migrated_tool_call: %s]\n%s\n[/migrated_tool_call]", m.ToolName, input),
					}}, i),
					time: m.Time,
				})
			}
		case ToolResult:
			out := m.Text
			if len(out) > maxToolResultBytes {
				out = out[:maxToolResultBytes] + "\n… (output truncated during migration)"
			}
			if id, ok := toolIDs[m.CallID]; ok {
				bodies = append(bodies, recordBody{
					recordType: "user",
					message: map[string]any{
						"role": "user",
						"content": []map[string]any{{
							"type":        "tool_result",
							"tool_use_id": id,
							"content":     out,
						}},
					},
					time:           m.Time,
					pairedWithPrev: true,
				})
			} else if flattened[m.CallID] {
				bodies = append(bodies, recordBody{
					recordType: "user",
					message: map[string]any{
						"role":    "user",
						"content": fmt.Sprintf("[migrated_tool_result]\n%s\n[/migrated_tool_result]", out),
					},
					time: m.Time,
				})
			}
		}
	}
	return bodies
}

// assistantMessage builds an assistant message with the metadata Claude Code
// expects on native records. The model field is load-bearing (see
// claudeMigrationModel); id/type/stop_reason mirror the native shape.
func assistantMessage(blocks []map[string]any, seq int) map[string]any {
	return map[string]any{
		"model":         claudeMigrationModel,
		"id":            fmt.Sprintf("msg_zenmigrate%04d", seq),
		"type":          "message",
		"role":          "assistant",
		"content":       blocks,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
	}
}

// shellToolInput maps a Codex shell-tool invocation to Claude's Bash input.
func shellToolInput(input map[string]any) map[string]any {
	cmd := ""
	if c, ok := input["cmd"].(string); ok {
		cmd = c
	} else if c, ok := input["command"].(string); ok {
		cmd = c
	} else if raw, err := json.Marshal(input); err == nil {
		cmd = string(raw)
	}
	return map[string]any{"command": cmd}
}

// capBodies keeps the newest records within maxClaudeSessionBytes, preserving
// the migration preamble and never splitting a tool_use/tool_result pair. When
// records are dropped, a truncation note takes their place.
func capBodies(bodies []recordBody) []recordBody {
	sizes := make([]int, len(bodies))
	total := 0
	for i, b := range bodies {
		raw, _ := json.Marshal(b.message)
		sizes[i] = len(raw) + 512 // envelope overhead estimate per record
		total += sizes[i]
	}
	if total <= maxClaudeSessionBytes {
		return bodies
	}

	note := recordBody{
		recordType: "user",
		message: map[string]any{
			"role":    "user",
			"content": "[Earlier history from the Codex session was truncated during migration to keep the session resumable.]",
		},
	}
	// bodies[0] is the preamble; find the oldest index we can keep.
	keepFrom := 1
	for total > maxClaudeSessionBytes && keepFrom < len(bodies) {
		total -= sizes[keepFrom]
		keepFrom++
	}
	// Never lead with the result half of a split pair.
	for keepFrom < len(bodies) && bodies[keepFrom].pairedWithPrev {
		keepFrom++
	}
	return append([]recordBody{bodies[0], note}, bodies[keepFrom:]...)
}
