package agent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// sampleRollout mirrors the on-disk shape of a Codex rollout file: one
// session_meta line, a turn_context carrying the model, then cumulative
// token_count events (the latest wins).
func sampleRollout(cwd string) string {
	return `{"timestamp":"2025-06-23T10:00:00.000Z","type":"session_meta","payload":{"id":"67e55044-10b1-426f-9247-bb680e5fe0c8","cwd":"` + cwd + `","cli_version":"0.5.0"}}
{"timestamp":"2025-06-23T10:00:01.000Z","type":"turn_context","payload":{"cwd":"` + cwd + `","model":"gpt-5-codex","approval_policy":"on-request"}}
{"timestamp":"2025-06-23T10:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":10,"output_tokens":50,"reasoning_output_tokens":5,"total_tokens":150},"last_token_usage":{"input_tokens":100,"cached_input_tokens":10,"output_tokens":50,"reasoning_output_tokens":5,"total_tokens":150},"model_context_window":272000}}}
{"timestamp":"2025-06-23T10:00:03.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":300,"cached_input_tokens":40,"output_tokens":120,"reasoning_output_tokens":15,"total_tokens":435},"last_token_usage":{"input_tokens":200,"cached_input_tokens":30,"output_tokens":70,"reasoning_output_tokens":10,"total_tokens":285},"model_context_window":272000}}}
`
}

// writeRollout drops a rollout file into a temp CODEX_HOME and returns its path.
func writeRollout(t *testing.T, codexHome, cwd string) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2025", "06", "23")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2025-06-23T10-00-00-67e55044-10b1-426f-9247-bb680e5fe0c8.jsonl")
	if err := os.WriteFile(path, []byte(sampleRollout(cwd)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexFindSessionsMatchesCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	wt := "/Users/dev/git/app-pr-42"
	writeRollout(t, home, wt)
	writeRollout2(t, home, "/Users/dev/git/other") // unrelated cwd

	ag := New(Codex, "codex")

	got, err := ag.FindSessions(wt)
	if err != nil {
		t.Fatalf("FindSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 session for %s, got %d", wt, len(got))
	}
	if got[0].ID != "67e55044-10b1-426f-9247-bb680e5fe0c8" {
		t.Errorf("session ID = %q, want the filename UUID", got[0].ID)
	}

	// A worktree with no matching rollout finds nothing.
	none, _ := ag.FindSessions("/nope")
	if len(none) != 0 {
		t.Errorf("expected no sessions for unmatched cwd, got %d", len(none))
	}
}

// writeRollout2 writes a second rollout under a different filename/cwd.
func writeRollout2(t *testing.T, codexHome, cwd string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2025", "06", "22")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2025-06-22T09-00-00-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	if err := os.WriteFile(path, []byte(sampleRollout(cwd)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodexParseTokensTakesLatestCumulative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := writeRollout(t, home, "/Users/dev/git/app-pr-42")

	ag := New(Codex, "codex")

	model, tokens, err := ag.ParseTokensFull(path)
	if err != nil {
		t.Fatalf("ParseTokensFull: %v", err)
	}
	if model != "gpt-5-codex" {
		t.Errorf("model = %q, want gpt-5-codex", model)
	}
	// total_token_usage is cumulative; the last event wins (not summed).
	if tokens.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", tokens.InputTokens)
	}
	if tokens.OutputTokens != 120 {
		t.Errorf("OutputTokens = %d, want 120", tokens.OutputTokens)
	}
	if tokens.CacheReadInputTokens != 40 {
		t.Errorf("CacheReadInputTokens = %d, want 40", tokens.CacheReadInputTokens)
	}
}

func TestCodexCommands(t *testing.T) {
	ag := New(Codex, "codex")

	if got := ag.StartCommand("/review-pr", "gpt-5-codex"); got != `codex -m 'gpt-5-codex' '/review-pr'` {
		t.Errorf("StartCommand = %q", got)
	}
	if got := ag.StartCommand("", ""); got != "codex" {
		t.Errorf("StartCommand(empty) = %q", got)
	}
	if got := ag.ResumeCommand("abc-123", ""); got != "codex resume 'abc-123'" {
		t.Errorf("ResumeCommand = %q", got)
	}
	// An explicit model override on resume is forwarded.
	if got := ag.ResumeCommand("abc-123", "gpt-5"); got != "codex resume 'abc-123' -m 'gpt-5'" {
		t.Errorf("ResumeCommand(model) = %q", got)
	}
}

func TestCommandQuoting(t *testing.T) {
	ag := New(Claude, "claude")

	if got := ag.StartCommand("review this", "opus"); got != "claude --model 'opus' 'review this'" {
		t.Errorf("StartCommand = %q", got)
	}
	if got := ag.ResumeCommand("abc-123", ""); got != "claude --resume 'abc-123'" {
		t.Errorf("ResumeCommand = %q", got)
	}
	// $, backticks, and single quotes in the prompt must be inert in the shell.
	if got := ag.StartCommand("don't run `id` or $(pwd)", ""); got != `claude 'don'\''t run `+"`id`"+` or $(pwd)'` {
		t.Errorf("StartCommand(hostile prompt) = %q", got)
	}
}

func TestCodexInjectContextFallsBackToSideFile(t *testing.T) {
	ag := New(Codex, "codex")

	// No AGENTS.md present → writes AGENTS.md.
	wt1 := t.TempDir()
	ref, err := ag.InjectContext(wt1, "# ctx")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "AGENTS.md" {
		t.Errorf("ref = %q, want AGENTS.md", ref)
	}
	if !ag.ContextPresent(wt1) {
		t.Error("ContextPresent should be true after injection")
	}

	// Repo ships its own AGENTS.md → context goes to the side file, untouched.
	wt2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt2, "AGENTS.md"), []byte("REPO OWN"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err = ag.InjectContext(wt2, "# ctx")
	if err != nil {
		t.Fatal(err)
	}
	if ref != codexSideContextFile {
		t.Errorf("ref = %q, want %q", ref, codexSideContextFile)
	}
	if data, _ := os.ReadFile(filepath.Join(wt2, "AGENTS.md")); string(data) != "REPO OWN" {
		t.Error("repo's own AGENTS.md was clobbered")
	}
	if !ag.ContextPresent(wt2) {
		t.Error("ContextPresent should be true after side-file injection")
	}

	// Re-inject updates AGENTS.md, not the side file, when that is where we wrote.
	ref, err = ag.InjectContext(wt1, "# ctx-v2")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "AGENTS.md" {
		t.Errorf("refresh ref = %q, want AGENTS.md", ref)
	}
	if data, _ := os.ReadFile(filepath.Join(wt1, "AGENTS.md")); string(data) != "# ctx-v2" {
		t.Errorf("AGENTS.md not rewritten on refresh: %s", data)
	}
}

func TestCodexOwnedContextFiles(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{name: "sentinel records AGENTS.md", files: map[string]string{
			codexContextFile: "generated",
			codexSentinel:    codexContextFile,
		}, want: []string{codexContextFile, codexSentinel}},
		{name: "sentinel records side context", files: map[string]string{
			codexContextFile:     "user owned",
			codexSideContextFile: "generated",
			codexSentinel:        codexSideContextFile,
		}, want: []string{codexSideContextFile, codexSentinel}},
		{name: "legacy sentinel claims side context", files: map[string]string{
			codexSideContextFile: "generated",
			codexSentinel:        "",
		}, want: []string{codexSideContextFile, codexSentinel}},
		{name: "legacy sentinel never claims AGENTS.md", files: map[string]string{
			codexContextFile:     "user owned",
			codexSideContextFile: "generated",
			codexSentinel:        "",
		}, want: []string{codexSideContextFile, codexSentinel}},
		{name: "legacy sentinel without side context", files: map[string]string{
			codexContextFile: "user owned",
			codexSentinel:    "",
		}, want: []string{codexSentinel}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worktreePath := t.TempDir()
			for name, content := range test.files {
				path := filepath.Join(worktreePath, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := OwnedContextFiles(worktreePath); !slices.Equal(got, test.want) {
				t.Errorf("OwnedContextFiles() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCodexShortenModel(t *testing.T) {
	ag := New(Codex, "codex")
	if got := ag.ShortenModel("openai/gpt-5-codex"); got != "gpt-5-codex" {
		t.Errorf("ShortenModel = %q, want gpt-5-codex", got)
	}
}
