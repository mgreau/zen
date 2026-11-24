package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRemoteControlCommands(t *testing.T) {
	claude, err := NewWithOptions(Claude, "claude", Options{RemoteControl: true}).PrepareLaunch("/wt")
	if err != nil {
		t.Fatal(err)
	}
	if got := claude.StartCommand("/review-pr", "opus"); got != "claude --model 'opus' '/review-pr' --remote-control" {
		t.Errorf("Claude StartCommand = %q", got)
	}
	if got := claude.ResumeCommand("abc-123", ""); got != "claude --resume 'abc-123' --remote-control" {
		t.Errorf("Claude ResumeCommand = %q", got)
	}

	bin := fakeCodexOnPath(t)
	var started string
	stubCodexDaemonFunc(t, func(b, _ string) error { started = b; return nil })
	unprepared := NewWithOptions(Codex, testCodexBin, Options{RemoteControl: true})
	// Print-only commands are built without PrepareLaunch and stay local.
	if got := unprepared.StartCommand("hi", ""); got != testCodexBin+" 'hi'" {
		t.Errorf("unprepared Codex StartCommand = %q, want local launch", got)
	}

	codex, err := unprepared.PrepareLaunch("/wt/it's here")
	if err != nil {
		t.Fatal(err)
	}
	// The daemon and the TUI run the same resolved binary; both it and the
	// worktree are quoted (the fake binary's directory contains a space).
	if started != bin {
		t.Errorf("daemon started with %q, want %q", started, bin)
	}
	remote := shellQuote(bin) + ` --remote unix:// -C '/wt/it'\''s here'`
	if got := codex.StartCommand("/review-pr", "gpt-5-codex"); got != remote+" -m 'gpt-5-codex' '/review-pr'" {
		t.Errorf("Codex StartCommand = %q", got)
	}
	if got := codex.ResumeCommand("abc-123", "gpt-5"); got != shellQuote(bin)+" resume 'abc-123' --remote unix:// -C '/wt/it'\\''s here' -m 'gpt-5'" {
		t.Errorf("Codex ResumeCommand = %q", got)
	}
}

func TestCodexRemoteControlFallsBackWhenDaemonFails(t *testing.T) {
	fakeCodexOnPath(t)
	stubCodexDaemonFunc(t, func(string, string) error { return errors.New("no daemon") })
	codex, err := NewWithOptions(Codex, testCodexBin, Options{RemoteControl: true}).PrepareLaunch("/wt")
	if err == nil || !strings.Contains(err.Error(), "no daemon") {
		t.Errorf("err = %v, want the daemon failure reported", err)
	}
	if got := codex.StartCommand("hi", ""); got != testCodexBin+" 'hi'" {
		t.Errorf("StartCommand = %q, want plain codex launch", got)
	}
	if got := codex.ResumeCommand("abc-123", ""); got != testCodexBin+" resume 'abc-123'" {
		t.Errorf("ResumeCommand = %q, want plain codex resume", got)
	}
}

func TestCodexRemoteControlFallsBackWhenBinNotExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	stubCodexDaemonFunc(t, func(string, string) error { t.Error("daemon started"); return nil })
	codex, err := NewWithOptions(Codex, "npx codex", Options{RemoteControl: true}).PrepareLaunch("/wt")
	if err == nil || !strings.Contains(err.Error(), "not an executable path") {
		t.Errorf("err = %v, want a codex_bin error", err)
	}
	if got := codex.StartCommand("", ""); got != "npx codex" {
		t.Errorf("StartCommand = %q, want the configured bin verbatim", got)
	}
}

func TestCodexPrepareLaunchExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var started string
	stubCodexDaemonFunc(t, func(b, _ string) error { started = b; return nil })
	if _, err := NewWithOptions(Codex, "~/bin/codex", Options{RemoteControl: true}).PrepareLaunch("/wt"); err != nil {
		t.Fatal(err)
	}
	if started != bin {
		t.Errorf("daemon started with %q, want %q", started, bin)
	}
}

func TestCodexRemoteControlOffNeverTouchesDaemon(t *testing.T) {
	stubCodexDaemonFunc(t, func(string, string) error { t.Error("daemon started with remote control off"); return nil })
	ag, err := New(Codex, "codex").PrepareLaunch("/wt")
	if err != nil {
		t.Fatal(err)
	}
	if got := ag.StartCommand("hi", ""); got != "codex 'hi'" {
		t.Errorf("StartCommand = %q", got)
	}
}

// testCodexBin is the configured codex_bin in tests; fakeCodexOnPath puts an
// executable of that name on PATH.
const testCodexBin = "zen-test-codex"

// fakeCodexOnPath installs a no-op testCodexBin in a directory whose name
// contains a space (to exercise quoting), makes it the whole PATH, and
// returns its absolute path.
func fakeCodexOnPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "has space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, testCodexBin)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return bin
}

// stubCodexDaemonFunc replaces the daemon starter so tests never launch Codex.
func stubCodexDaemonFunc(t *testing.T, fn func(bin, logPath string) error) {
	t.Helper()
	orig := ensureCodexRemoteDaemon
	ensureCodexRemoteDaemon = fn
	t.Cleanup(func() { ensureCodexRemoteDaemon = orig })
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

func TestCodexShortenModel(t *testing.T) {
	ag := New(Codex, "codex")
	if got := ag.ShortenModel("openai/gpt-5-codex"); got != "gpt-5-codex" {
		t.Errorf("ShortenModel = %q, want gpt-5-codex", got)
	}
}
