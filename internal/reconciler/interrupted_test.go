package reconciler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckSessionCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	t.Cleanup(func() { interruptChecks.Delete(path) })
	write := func(content string, mod time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	prompt := `{"type":"user","message":{"content":"go"}}` + "\n"
	marker := `{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}` + "\n"
	tool := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"Task"}]}}` + "\n"

	sec := time.Unix(1_700_000_000, 0)
	write(prompt, sec)
	if c := checkSession(path); c.interrupted || c.toolRunning {
		t.Fatalf("plain prompt: %+v", c)
	}
	// Appended within the same second: whole-second keys would miss it.
	write(prompt+marker, sec.Add(300*time.Millisecond))
	if c := checkSession(path); !c.interrupted {
		t.Error("interrupt written within the same second was missed")
	}
	write(prompt+tool, sec.Add(600*time.Millisecond))
	if c := checkSession(path); !c.toolRunning || c.interrupted {
		t.Errorf("running tool call: %+v", c)
	}
}
