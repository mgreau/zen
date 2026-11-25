package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgreau/zen/internal/session"
)

func TestClaudeCleanSessionsKeepsSharedDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mine := filepath.Join(home, "app-my_feature")
	other := filepath.Join(home, "app-my-feature")
	for _, d := range []string{mine, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	proj := session.ProjectDirPath(mine)
	for _, d := range []string{filepath.Join(proj, "mine", "subagents"), filepath.Join(proj, "memory")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, cwd string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(`{"type":"user","cwd":"`+cwd+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("mine.jsonl", mine)
	write("theirs.jsonl", other)

	n, err := New(Claude, "").CleanSessions(mine)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("removed %d, want 1", n)
	}
	for _, gone := range []string{"mine.jsonl", "mine"} {
		if _, err := os.Stat(filepath.Join(proj, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still present", gone)
		}
	}
	for _, kept := range []string{"theirs.jsonl", "memory"} {
		if _, err := os.Stat(filepath.Join(proj, kept)); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}
