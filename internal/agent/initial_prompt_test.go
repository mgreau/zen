package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartCommandFromFile(t *testing.T) {
	claude := NewWithOptions(Claude, "claude", Options{RemoteControl: true})
	// The prompt goes before --remote-control, which would otherwise take
	// it as the session name.
	want := `claude --model 'opus' "$(cat '/wt/it'\''s/.zen/INITIAL_PROMPT.md')" --remote-control`
	if got := claude.StartCommandFromFile("/wt/it's/.zen/INITIAL_PROMPT.md", "opus"); got != want {
		t.Errorf("Claude StartCommandFromFile = %q, want %q", got, want)
	}

	codex := New(Codex, "codex")
	want = `codex -m 'gpt-5' "$(cat '/wt/p.md')"`
	if got := codex.StartCommandFromFile("/wt/p.md", "gpt-5"); got != want {
		t.Errorf("Codex StartCommandFromFile = %q, want %q", got, want)
	}
}

// TestStartCommandFromFilePassesPromptVerbatim runs the command in a shell,
// with printf standing in for the agent, to check that a long prompt full of
// shell syntax arrives as one argument, unchanged.
func TestStartCommandFromFilePassesPromptVerbatim(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	prompt := "# Hand-off\n\nIt's \"$HOME\", `date`, $(id) and \\n; exit 1\n" +
		strings.Repeat("Longer than tmux's 16 KB command limit. ", 600)
	path := filepath.Join(t.TempDir(), "prompt with space.md")
	if err := os.WriteFile(path, []byte(prompt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := New(Claude, "printf %s").StartCommandFromFile(path, "")
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		t.Fatalf("running %q: %v", cmd, err)
	}
	// Command substitution drops the trailing newline, nothing else.
	if string(out) != prompt {
		t.Errorf("prompt changed on the way through the shell:\ngot  %.120q\nwant %.120q", out, prompt)
	}
}

func TestStageInitialPrompt(t *testing.T) {
	wt := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", wt).CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}

	path, err := StageInitialPrompt(wt, []byte("do the thing\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wt, ".zen", "INITIAL_PROMPT.md"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "do the thing\n" {
		t.Errorf("prompt file = %q, %v", data, err)
	}

	// .zen/ is excluded, so the prompt never shows up as an untracked file.
	out, err := exec.Command("git", "-C", wt, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("git status shows %q, want nothing", out)
	}
}
