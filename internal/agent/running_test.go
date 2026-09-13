package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsAgentProcess(t *testing.T) {
	tests := []struct {
		comm, cmdline string
		want          bool
	}{
		{"claude", "/opt/homebrew/bin/claude /review-pr", true},
		{"/opt/homebrew/bin/claude", "/opt/homebrew/bin/claude /review-pr", true},
		{"/tmp/claude", "/tmp/claude", true},
		{"codex", "codex resume abc", true},
		{"aider", "aider --chat-mode ask", true},
		{"node", "/usr/bin/node /Users/x/.nvm/versions/node/v22/lib/node_modules/@anthropic-ai/claude-code/cli.js", true},
		{"node", "/usr/bin/node /opt/homebrew/lib/node_modules/claude-code/cli.js", true},
		{"node", "/usr/bin/node /Users/x/.local/share/npm/bin/claude /review-pr", true},
		{"nodejs", "/usr/bin/node /usr/lib/node_modules/@openai/codex/bin.js", true},
		{"python3", "/usr/bin/python3 /Users/x/.local/bin/aider --no-auto-commits", true},
		{"python3.12", "python3.12 -m aider", true},
		{"uv", "uv run aider --chat-mode ask", true},
		{"node", "/Applications/Cursor.app/Contents/Resources/app/out/main.js", false},
		{"node", "next-server", false},
		{"python3", "pytest -q", false},
		{"Google Chrome", "https://claude.ai", false},
		{"", "claude", false},
		{"zsh", "cd /tmp && claude", false},
	}
	for _, tt := range tests {
		got := isAgentProcess(tt.comm, tt.cmdline)
		if got != tt.want {
			t.Errorf("isAgentProcess(%q, %q) = %v, want %v", tt.comm, tt.cmdline, got, tt.want)
		}
	}
}

func TestCwdInWorktree(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "mono-pr-42")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(wt, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	sib := filepath.Join(root, "mono-pr-43")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}

	want, err := canonicalDir(wt)
	if err != nil {
		t.Fatal(err)
	}

	if !cwdInWorktree(wt, want) {
		t.Fatal("worktree root should match itself")
	}
	if !cwdInWorktree(sub, want) {
		t.Fatal("subdirectory should match")
	}
	if cwdInWorktree(sib, want) {
		t.Fatal("sibling worktree must not match")
	}
	if cwdInWorktree(root, want) {
		t.Fatal("parent directory must not match")
	}
	if cwdInWorktree("", want) || cwdInWorktree(wt, "") {
		t.Fatal("empty paths must not match")
	}
}

func TestCwdInWorktree_symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root := t.TempDir()
	realWT := filepath.Join(root, "real")
	if err := os.MkdirAll(realWT, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realWT, link); err != nil {
		t.Fatal(err)
	}
	want, err := canonicalDir(realWT)
	if err != nil {
		t.Fatal(err)
	}
	if !cwdInWorktree(link, want) {
		t.Fatal("symlink cwd should match the real worktree")
	}
}
