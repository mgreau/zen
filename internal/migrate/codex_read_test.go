package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRollout mirrors the record shapes of a real Codex 0.142.0 rollout:
// session_meta first, then interleaved response_item / event_msg lines.
const fixtureRollout = `{"timestamp":"2026-07-03T22:00:56.979Z","type":"session_meta","payload":{"session_id":"019f29ff-a884-7573-a377-186663b3396c","id":"019f29ff-a884-7573-a377-186663b3396c","cwd":"/work/tree","git":{"branch":"pr-42"},"base_instructions":{"text":"You are Codex..."}}}
{"timestamp":"2026-07-03T22:00:56.986Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"timestamp":"2026-07-03T22:00:59.504Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>\nsandbox stuff"}]}}
{"timestamp":"2026-07-03T22:00:59.600Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<user_instructions>\nAGENTS.md content"}]}}
{"timestamp":"2026-07-03T22:01:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Review this PR please"}]}}
{"timestamp":"2026-07-03T22:01:05.000Z","type":"response_item","payload":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"gAAAA=="}}
{"timestamp":"2026-07-03T22:01:06.000Z","type":"response_item","payload":{"type":"function_call","id":"fc_1","name":"exec_command","arguments":"{\"cmd\": \"git diff --stat\"}","call_id":"call_1"}}
{"timestamp":"2026-07-03T22:01:07.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"3 files changed"}}
{"timestamp":"2026-07-03T22:01:08.000Z","type":"response_item","payload":{"type":"function_call","id":"fc_2","name":"update_plan","arguments":"{\"steps\": [\"a\"]}","call_id":"call_2"}}
{"timestamp":"2026-07-03T22:01:10.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The diff touches 3 files."}]}}
{"timestamp":"2026-07-03T22:01:11.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100}}}}
`

func writeFixtureRollout(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-2026-07-03T22-00-48-019f29ff.jsonl")
	if err := os.WriteFile(path, []byte(fixtureRollout), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadCodexRollout(t *testing.T) {
	tr, err := ReadCodexRollout(writeFixtureRollout(t))
	if err != nil {
		t.Fatal(err)
	}

	if tr.SourceID != "019f29ff-a884-7573-a377-186663b3396c" {
		t.Errorf("SourceID = %q", tr.SourceID)
	}
	if tr.Cwd != "/work/tree" || tr.GitBranch != "pr-42" {
		t.Errorf("Cwd/GitBranch = %q/%q", tr.Cwd, tr.GitBranch)
	}

	// Injected context, reasoning, and event_msg records must be gone; tool
	// calls must be immediately followed by their result.
	wantKinds := []MsgKind{UserText, ToolCall, ToolResult, ToolCall, ToolResult, AssistantText}
	if len(tr.Messages) != len(wantKinds) {
		t.Fatalf("got %d messages, want %d: %+v", len(tr.Messages), len(wantKinds), tr.Messages)
	}
	for i, want := range wantKinds {
		if tr.Messages[i].Kind != want {
			t.Errorf("message %d kind = %v, want %v", i, tr.Messages[i].Kind, want)
		}
	}

	if got := tr.Messages[0].Text; got != "Review this PR please" {
		t.Errorf("user text = %q", got)
	}
	call := tr.Messages[1]
	if call.ToolName != "exec_command" || call.ToolInput["cmd"] != "git diff --stat" || call.CallID != "call_1" {
		t.Errorf("tool call = %+v", call)
	}
	if got := tr.Messages[2].Text; got != "3 files changed" {
		t.Errorf("tool result = %q", got)
	}
	// call_2 never produced output: a placeholder keeps the pair well-formed.
	if got := tr.Messages[4].Text; !strings.Contains(got, "not recorded") {
		t.Errorf("missing-output placeholder = %q", got)
	}
}

func TestReadCodexRolloutEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-empty.jsonl")
	meta := `{"timestamp":"2026-07-03T22:00:56.979Z","type":"session_meta","payload":{"id":"x","cwd":"/w"}}` + "\n"
	if err := os.WriteFile(path, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCodexRollout(path); err == nil {
		t.Error("expected error for rollout with no conversation")
	}
}
