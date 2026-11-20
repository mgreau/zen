package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLastTurnInterrupted(t *testing.T) {
	const (
		prompt      = `{"type":"user","message":{"role":"user","content":"run the tests"}}`
		toolUse     = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash"}]}}`
		toolResult  = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":"ok"}]}}`
		interrupted = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
		forTool     = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
		bookkeeping = `{"type":"file-history-snapshot","messageId":"m"}` + "\n" + `{"type":"queue-operation","operation":"dequeue"}`
	)
	tests := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"interrupted", []string{prompt, interrupted}, true},
		{"interrupted during a tool", []string{prompt, toolUse, forTool}, true},
		{"bookkeeping after the marker", []string{prompt, interrupted, bookkeeping}, true},
		{"a tool still running", []string{prompt, toolUse, bookkeeping}, false},
		{"a tool result", []string{prompt, toolUse, toolResult}, false},
		{"replied after an interrupt", []string{interrupted, prompt}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(tt.lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := LastTurnInterrupted(path); got != tt.want {
			t.Errorf("%s: LastTurnInterrupted = %v, want %v", tt.name, got, tt.want)
		}
	}
	// A snapshot after the marker bigger than the first read.
	big := `{"type":"file-history-snapshot","snapshot":"` + strings.Repeat("x", 3*interruptTail) + `"}`
	path := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join([]string{prompt, interrupted, big}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !LastTurnInterrupted(path) {
		t.Error("missed an interrupt behind a large snapshot")
	}

	if LastTurnInterrupted(filepath.Join(t.TempDir(), "missing")) {
		t.Error("missing file reported interrupted")
	}
}

// A slash command run after an interrupt records user entries that are not
// a message from the user; the interrupt still counts.
func TestLastTurnInterruptedAfterCommand(t *testing.T) {
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"run the tests"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"Caveat: local command"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/status</command-name>"}}`,
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>ok</local-command-stdout>"}}`,
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !LastTurnInterrupted(path) {
		t.Error("a slash command after the interrupt hid it")
	}
}

func TestToolRunning(t *testing.T) {
	for _, tt := range []struct {
		name  string
		lines []string
		want  bool
	}{
		{"subagent running", []string{
			`{"type":"user","message":{"content":"go"}}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"on it"},{"type":"tool_use","id":"t","name":"Task"}]}}`,
			`{"type":"file-history-snapshot"}`,
		}, true},
		{"tool finished", []string{
			`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"Bash"}]}}`,
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t"}]}}`,
		}, false},
		{"reply", []string{
			`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`,
		}, false},
	} {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(tt.lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ToolRunning(path); got != tt.want {
			t.Errorf("%s: ToolRunning = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTailEdgeCases(t *testing.T) {
	write := func(lines ...string) string {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// Two subagents in parallel: one finished, the other still running.
	parallel := write(
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Task"},{"type":"tool_use","id":"b","name":"Task"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a"}]}}`,
	)
	if !ToolRunning(parallel) {
		t.Error("a parallel tool call still running was missed")
	}
	both := write(
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Task"},{"type":"tool_use","id":"b","name":"Task"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b"}]}}`,
	)
	if ToolRunning(both) {
		t.Error("tool reported running after both results came in")
	}

	// A prompt command after an interrupt sets Claude working again.
	prompted := write(
		`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
		`{"type":"user","message":{"content":"<command-name>/review-pr</command-name>"}}`,
		`{"type":"user","isMeta":true,"message":{"content":"Review the PR..."}}`,
	)
	if LastTurnInterrupted(prompted) {
		t.Error("a prompt command after the interrupt still counted as interrupted")
	}
}
