package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPathToClaudeProject(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/maxime/git/mono-pr-123", "-Users-maxime-git-mono-pr-123"},
		{"/Users/alice/code/repo", "-Users-alice-code-repo"},
		{"/tmp/test", "-tmp-test"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := pathToClaudeProject(tt.path)
			if got != tt.want {
				t.Errorf("pathToClaudeProject(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500B"},
		{1025, "1KB"},
		{1048577, "1MB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatSize(tt.bytes)
			if got != tt.want {
				t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestFindSessionsWithFixture(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create a fake Claude projects directory
	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-tmp-fakeworktree")
	os.MkdirAll(projectDir, 0o755)

	// Write some fake session files
	os.WriteFile(filepath.Join(projectDir, "abc123.jsonl"), []byte(`{"type":"test"}`), 0o644)
	os.WriteFile(filepath.Join(projectDir, "def456.jsonl"), []byte(`{"type":"test2"}`), 0o644)
	os.WriteFile(filepath.Join(projectDir, "readme.md"), []byte("not a session"), 0o644)

	sessions, err := FindSessions("/tmp/fakeworktree")
	if err != nil {
		t.Fatalf("FindSessions() error: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("FindSessions() returned %d sessions, want 2", len(sessions))
	}

	// Verify sessions have IDs (without .jsonl)
	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.ID] = true
	}
	if !ids["abc123"] || !ids["def456"] {
		t.Errorf("sessions IDs = %v, want abc123 and def456", ids)
	}
}

func TestHasActiveSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// No sessions exist
	if HasActiveSession("/tmp/nonexistent") {
		t.Error("HasActiveSession should be false when no sessions exist")
	}

	// Create a session
	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-tmp-testworkdir")
	os.MkdirAll(projectDir, 0o755)
	os.WriteFile(filepath.Join(projectDir, "session1.jsonl"), []byte("{}"), 0o644)

	if !HasActiveSession("/tmp/testworkdir") {
		t.Error("HasActiveSession should be true when sessions exist")
	}
}

func TestParseSessionDetailFull(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "test-session.jsonl")

	content := `{"type":"system"}
{"message":{"model":"claude-opus-4-6","usage":{"input_tokens":1000,"output_tokens":200,"cache_creation_input_tokens":500,"cache_read_input_tokens":300}}}
{"message":{"model":"claude-opus-4-6","usage":{"input_tokens":2000,"output_tokens":400,"cache_creation_input_tokens":0,"cache_read_input_tokens":800}}}
{"type":"result"}
`
	os.WriteFile(sessionFile, []byte(content), 0o644)

	model, tokens, err := ParseSessionDetailFull(sessionFile)
	if err != nil {
		t.Fatalf("ParseSessionDetailFull() error: %v", err)
	}

	if model != "claude-opus-4-6" {
		t.Errorf("model = %q, want %q", model, "claude-opus-4-6")
	}
	if tokens.InputTokens != 3000 {
		t.Errorf("InputTokens = %d, want 3000", tokens.InputTokens)
	}
	if tokens.OutputTokens != 600 {
		t.Errorf("OutputTokens = %d, want 600", tokens.OutputTokens)
	}
	if tokens.CacheCreationInputTokens != 500 {
		t.Errorf("CacheCreationInputTokens = %d, want 500", tokens.CacheCreationInputTokens)
	}
	if tokens.CacheReadInputTokens != 1100 {
		t.Errorf("CacheReadInputTokens = %d, want 1100", tokens.CacheReadInputTokens)
	}
}

func TestParseSessionDetailTail(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "test-session.jsonl")

	content := `{"message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":500,"output_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	os.WriteFile(sessionFile, []byte(content), 0o644)

	model, tokens, err := ParseSessionDetailTail(sessionFile)
	if err != nil {
		t.Fatalf("ParseSessionDetailTail() error: %v", err)
	}

	if model != "claude-sonnet-4-5-20250929" {
		t.Errorf("model = %q, want %q", model, "claude-sonnet-4-5-20250929")
	}
	if tokens.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", tokens.InputTokens)
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1.0K"},
		{12500, "12.5K"},
		{1000000, "1.0M"},
		{3500000, "3.5M"},
	}
	for _, tt := range tests {
		got := FormatTokenCount(tt.n)
		if got != tt.want {
			t.Errorf("FormatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestShortenModel(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"claude-opus-4-6", "opus-4-6"},
		{"claude-sonnet-4-5-20250929", "sonnet-4-5"},
		{"claude-haiku-4-5-20251001", "haiku-4-5"},
		{"unknown-model", "unknown-model"},
	}
	for _, tt := range tests {
		got := ShortenModel(tt.model)
		if got != tt.want {
			t.Errorf("ShortenModel(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestFormatAge(t *testing.T) {
	now := time.Now()

	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-48 * time.Hour), "2d ago"},
	}
	for _, tt := range tests {
		got := FormatAge(tt.t)
		if got != tt.want {
			t.Errorf("FormatAge(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestSessionFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	got := SessionFilePath("/tmp/my-worktree", "abc-123")
	want := filepath.Join(tmpDir, ".claude", "projects", "-tmp-my-worktree", "abc-123.jsonl")
	if got != want {
		t.Errorf("SessionFilePath() = %q, want %q", got, want)
	}
}
