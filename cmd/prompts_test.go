package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
)

var testPrompts = fstest.MapFS{
	"commands/review-pr.md":  {Data: []byte("review")},
	"commands/zen-import.md": {Data: []byte("import")},
}

// usePromptTestConfig points HOME and CODEX_HOME at temporary directories
// and gives the commands a default config, restoring cfg afterwards.
func usePromptTestConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	old := cfg
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = old })
	return home
}

func TestEnsurePromptsInstallsMissingOnly(t *testing.T) {
	home := usePromptTestConfig(t)
	dir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A prompt the user already has, maybe edited, is kept.
	if err := os.WriteFile(filepath.Join(dir, "review-pr.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	ag := cfg.NewAgent(string(agent.Claude))
	if got := ensurePromptsFrom(testPrompts, ag); !slices.Equal(got, []string{"zen-import"}) {
		t.Errorf("first run installed %q, want [zen-import]", got)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "review-pr.md")); string(data) != "mine" {
		t.Errorf("existing review-pr.md overwritten: %q", data)
	}
	if got := ensurePromptsFrom(testPrompts, ag); len(got) != 0 {
		t.Errorf("second run installed %q, want nothing", got)
	}
}

func TestEnsurePromptsSkipsClaudeOnlyForCodex(t *testing.T) {
	usePromptTestConfig(t)
	got := ensurePromptsFrom(testPrompts, cfg.NewAgent(string(agent.Codex)))
	if !slices.Equal(got, []string{"review-pr"}) {
		t.Errorf("Codex got %q, want only review-pr", got)
	}
}

func TestEnsureImportPrompt(t *testing.T) {
	home := usePromptTestConfig(t)

	// Without ~/.claude, zen doesn't create it.
	if got := ensureImportPromptFrom(testPrompts); len(got) != 0 {
		t.Errorf("installed %q without ~/.claude", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("~/.claude created: %v", err)
	}

	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ensureImportPromptFrom(testPrompts)
	if !slices.Contains(got, "zen-import") {
		t.Errorf("installed %q, want zen-import", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "commands", "zen-import.md")); err != nil {
		t.Errorf("zen-import.md not installed: %v", err)
	}
}
