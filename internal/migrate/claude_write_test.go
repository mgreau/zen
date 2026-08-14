package migrate

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/session"
)

// readSessionRecords parses every line of a written Claude session file.
func readSessionRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var recs []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("invalid JSONL line: %v", err)
		}
		recs = append(recs, r)
	}
	return recs
}

func TestWriteClaudeSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktree := filepath.Join(home, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	tr := &Transcript{
		SourceID:  "codex-thread-1",
		GitBranch: "pr-42",
		Messages: []Message{
			{Kind: UserText, Text: "Review this PR", Time: "2026-07-03T22:01:00Z"},
			{Kind: ToolCall, ToolName: "exec_command", ToolInput: map[string]any{"cmd": "git diff"}, CallID: "call_1", Time: "2026-07-03T22:01:06Z"},
			{Kind: ToolResult, Text: "3 files changed", CallID: "call_1", Time: "2026-07-03T22:01:07Z"},
			{Kind: ToolCall, ToolName: "update_plan", ToolInput: map[string]any{"steps": []any{"a"}}, CallID: "call_2", Time: "2026-07-03T22:01:08Z"},
			{Kind: ToolResult, Text: "plan updated", CallID: "call_2", Time: "2026-07-03T22:01:09Z"},
			{Kind: AssistantText, Text: "Done reviewing.", Time: "2026-07-03T22:01:10Z"},
		},
	}

	id, err := WriteClaudeSession(tr, worktree)
	if err != nil {
		t.Fatal(err)
	}

	// The file must land where Claude's resume discovery looks for it.
	dir := session.ProjectDirPath(worktree)
	path := filepath.Join(dir, id+".jsonl")
	recs := readSessionRecords(t, path)
	if len(recs) == 0 {
		t.Fatalf("no records written to %s", path)
	}

	// Envelope invariants on every record.
	var prevUUID any
	for i, r := range recs {
		if r["sessionId"] != id {
			t.Errorf("record %d sessionId = %v, want %v", i, r["sessionId"], id)
		}
		if r["isSidechain"] != false {
			t.Errorf("record %d isSidechain = %v", i, r["isSidechain"])
		}
		if r["parentUuid"] != prevUUID {
			t.Errorf("record %d parentUuid = %v, want %v", i, r["parentUuid"], prevUUID)
		}
		if r["uuid"] == "" || r["uuid"] == nil {
			t.Errorf("record %d has no uuid", i)
		}
		if r["gitBranch"] != "pr-42" {
			t.Errorf("record %d gitBranch = %v", i, r["gitBranch"])
		}
		prevUUID = r["uuid"]
	}

	// Record 0 is the migration preamble naming the source thread.
	if msg := recs[0]["message"].(map[string]any); !strings.Contains(msg["content"].(string), "codex-thread-1") {
		t.Errorf("preamble = %v", msg["content"])
	}

	// Every assistant message must carry a model: resume reads the session's
	// model from the transcript and hangs indefinitely when none is present
	// (verified against Claude Code 2.1.200).
	for i, r := range recs {
		if r["type"] != "assistant" {
			continue
		}
		if m := r["message"].(map[string]any); m["model"] != claudeMigrationModel {
			t.Errorf("assistant record %d model = %v, want %v", i, m["model"], claudeMigrationModel)
		}
	}

	// The shell tool call becomes a native Bash tool_use, and the record after
	// it must be a user record carrying the matching tool_result.
	var bashIdx = -1
	for i, r := range recs {
		blocks, ok := r["message"].(map[string]any)["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			block := b.(map[string]any)
			if block["type"] == "tool_use" {
				bashIdx = i
				if block["name"] != "Bash" {
					t.Errorf("tool_use name = %v", block["name"])
				}
				if input := block["input"].(map[string]any); input["command"] != "git diff" {
					t.Errorf("tool_use input = %v", input)
				}
			}
		}
	}
	if bashIdx == -1 {
		t.Fatal("no Bash tool_use written")
	}
	next := recs[bashIdx+1]
	if next["type"] != "user" {
		t.Fatalf("record after tool_use is %v, want user", next["type"])
	}
	resBlocks := next["message"].(map[string]any)["content"].([]any)
	res := resBlocks[0].(map[string]any)
	callBlocks := recs[bashIdx]["message"].(map[string]any)["content"].([]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != callBlocks[0].(map[string]any)["id"] {
		t.Errorf("tool_result = %v", res)
	}

	// The non-shell tool (update_plan) must be flattened to text, never a
	// dangling tool_use Claude's API would reject.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "update_plan\",") && strings.Contains(string(raw), `"type":"tool_use","name":"update_plan"`) {
		t.Error("update_plan was written as a native tool_use")
	}
	if !strings.Contains(string(raw), "[migrated_tool_call: update_plan]") {
		t.Error("update_plan was not flattened to a marked text block")
	}
}

func TestWriteClaudeSessionMissingWorktree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tr := &Transcript{Messages: []Message{{Kind: UserText, Text: "hi"}}}
	if _, err := WriteClaudeSession(tr, "/does/not/exist"); err == nil {
		t.Error("expected error for non-existent worktree")
	}
}

func TestCapBodiesDropsOldestKeepsPairs(t *testing.T) {
	// Build bodies: preamble + many filler records + a tool pair at the cap
	// boundary, and verify no leading orphan tool_result survives.
	big := strings.Repeat("x", 512*1024)
	bodies := []recordBody{{recordType: "user", message: map[string]any{"role": "user", "content": "preamble"}}}
	for i := 0; i < 12; i++ {
		bodies = append(bodies,
			recordBody{recordType: "assistant", message: map[string]any{"role": "assistant", "content": big}},
			recordBody{recordType: "user", message: map[string]any{"role": "user", "content": "r"}, pairedWithPrev: true},
		)
	}

	capped := capBodies(bodies)
	if len(capped) >= len(bodies) {
		t.Fatalf("capBodies did not drop records (%d -> %d)", len(bodies), len(capped))
	}
	if capped[0].message["content"] != "preamble" {
		t.Error("preamble was dropped")
	}
	if !strings.Contains(capped[1].message["content"].(string), "truncated") {
		t.Error("no truncation note inserted")
	}
	if capped[2].pairedWithPrev {
		t.Error("capped output leads with the result half of a tool pair")
	}
	total := 0
	for _, b := range capped {
		raw, _ := json.Marshal(b.message)
		total += len(raw) + 512
	}
	if total > maxClaudeSessionBytes {
		t.Errorf("capped size %d still exceeds limit %d", total, maxClaudeSessionBytes)
	}
}
