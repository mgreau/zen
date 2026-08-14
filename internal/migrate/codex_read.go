package migrate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// codexRolloutLine is the outer envelope of a rollout record:
// {"timestamp":"...","type":"...","payload":{...}}.
type codexRolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexInjectedPrefixes mark user-role messages that Codex injects around the
// real conversation (sandbox rules, AGENTS.md content, environment info).
// They describe the Codex runtime, not the work, so they are not migrated.
var codexInjectedPrefixes = []string{
	"<permissions instructions>",
	"<user_instructions>",
	"<environment_context>",
	"<ide_context>",
	"<turn_aborted>",
	"# AGENTS.md instructions", // how Codex 0.142 injects AGENTS.md content
}

// codexImportSentinel is the marker Codex's own importer appends to threads it
// created from Claude sessions; it is meaningless outside Codex.
const codexImportSentinel = "<EXTERNAL SESSION IMPORTED>"

// ReadCodexRollout parses a Codex rollout file into a neutral transcript.
// Records that carry no portable conversation content are skipped: encrypted
// reasoning, event_msg UI replays, turn_context, and Codex-injected context
// messages. Every ToolCall in the result is immediately followed by its
// ToolResult; a placeholder is synthesized when the rollout recorded no output.
func ReadCodexRollout(path string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening rollout: %w", err)
	}
	defer f.Close()

	t := &Transcript{}
	// Tool outputs arrive as separate function_call_output records; collect
	// calls first and attach outputs by call_id so pairs stay adjacent.
	outputs := map[string]string{}
	var entries []Message

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line codexRolloutLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch line.Type {
		case "session_meta":
			var meta struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
				Cwd       string `json:"cwd"`
				Git       struct {
					Branch string `json:"branch"`
				} `json:"git"`
			}
			if json.Unmarshal(line.Payload, &meta) == nil {
				t.Cwd = meta.Cwd
				t.GitBranch = meta.Git.Branch
				if t.SourceID = meta.SessionID; t.SourceID == "" {
					t.SourceID = meta.ID
				}
			}
		case "response_item":
			var item struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				CallID    string `json:"call_id"`
				Output    string `json:"output"`
			}
			if json.Unmarshal(line.Payload, &item) != nil {
				continue
			}
			switch item.Type {
			case "message":
				text := joinCodexContent(item.Content)
				if text == "" || codexInjected(item.Role, text) {
					continue
				}
				kind := UserText
				if item.Role == "assistant" {
					kind = AssistantText
				} else if item.Role != "user" {
					continue // developer/system: Codex runtime context
				}
				entries = append(entries, Message{Kind: kind, Text: text, Time: line.Timestamp})
			case "function_call":
				input := map[string]any{}
				if json.Unmarshal([]byte(item.Arguments), &input) != nil {
					// Unparseable arguments still matter as history.
					input = map[string]any{"arguments": item.Arguments}
				}
				entries = append(entries, Message{Kind: ToolCall, ToolName: item.Name, ToolInput: input, CallID: item.CallID, Time: line.Timestamp})
			case "function_call_output":
				outputs[item.CallID] = item.Output
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading rollout: %w", err)
	}

	for _, m := range entries {
		t.Messages = append(t.Messages, m)
		if m.Kind == ToolCall {
			out, ok := outputs[m.CallID]
			if !ok {
				out = "(tool output not recorded in the source session)"
			}
			t.Messages = append(t.Messages, Message{Kind: ToolResult, Text: out, CallID: m.CallID, Time: m.Time})
		}
	}
	if len(t.Messages) == 0 {
		return nil, fmt.Errorf("rollout %s contains no migratable conversation", path)
	}
	return t, nil
}

func joinCodexContent(content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var parts []string
	for _, c := range content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func codexInjected(role, text string) bool {
	if role == "assistant" {
		return text == codexImportSentinel
	}
	for _, p := range codexInjectedPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}
