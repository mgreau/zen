package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates an "upstream" repo with a "main" branch and one
// commit, then clones it into "origin" so the clone has a real `origin`
// remote pointing at upstream — mirroring a real checkout, where
// CreateFromMain's `git fetch origin main` fetches from GitHub.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	upstreamPath := filepath.Join(dir, "upstream")
	if err := os.MkdirAll(upstreamPath, 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(repoDir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run(upstreamPath, "init", "-b", "main")
	run(upstreamPath, "config", "user.email", "test@example.com")
	run(upstreamPath, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(upstreamPath, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(upstreamPath, "add", "README.md")
	run(upstreamPath, "commit", "-m", "initial")

	repoPath := filepath.Join(dir, "origin")
	run(dir, "clone", upstreamPath, repoPath)
	run(repoPath, "config", "user.email", "test@example.com")
	run(repoPath, "config", "user.name", "Test")

	return repoPath
}

func TestCreateFromMain(t *testing.T) {
	repoPath := initTestRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-feature")

	if err := CreateFromMain(repoPath, worktreePath, "repo-feature", "mgreau/feature"); err != nil {
		t.Fatalf("CreateFromMain() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "README.md")); err != nil {
		t.Errorf("expected checked-out worktree, README.md missing: %v", err)
	}

	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current: %v", err)
	}
	if got := string(out); got != "mgreau/feature\n" {
		t.Errorf("branch = %q, want %q", got, "mgreau/feature\n")
	}
}

func TestCreateFromMain_FetchFailure(t *testing.T) {
	dir := t.TempDir()
	// Not a git repo at all — "git fetch" should fail cleanly.
	err := CreateFromMain(dir, filepath.Join(dir, "wt"), "wt", "mgreau/feature")
	if err == nil {
		t.Fatal("expected error for non-repo originPath")
	}
}
