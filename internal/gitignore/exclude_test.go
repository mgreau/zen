package gitignore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

// initRepo makes a repository with one commit, so check-ignore has an index
// to work against.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "README.md")
	run(t, dir, "commit", "-m", "initial")
	return dir
}

func excludeContents(t *testing.T, repo string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func TestEnsureExcluded(t *testing.T) {
	repo := initRepo(t)

	if IsIgnored(repo, "_worktrees/") {
		t.Fatal("_worktrees/ ignored before anything was written")
	}
	if !EnsureExcluded(repo, "_worktrees/") {
		t.Fatal("EnsureExcluded() = false, want the path ignored afterwards")
	}
	if !IsIgnored(repo, "_worktrees/") {
		t.Error("git still does not ignore _worktrees/ after EnsureExcluded()")
	}
}

// The ref must be matched before it exists on disk -- zen excludes the
// directory before creating the first worktree in it.
func TestEnsureExcludedBeforeDirectoryExists(t *testing.T) {
	repo := initRepo(t)

	if !EnsureExcluded(repo, "_worktrees/") {
		t.Fatal("EnsureExcluded() = false for a path that does not exist yet")
	}
	if _, err := os.Stat(filepath.Join(repo, "_worktrees")); !os.IsNotExist(err) {
		t.Fatal("test precondition: _worktrees should not exist")
	}
	if !IsIgnored(repo, "_worktrees/") {
		t.Error("a not-yet-created directory should still match the exclude")
	}
}

func TestEnsureExcludedIsIdempotent(t *testing.T) {
	repo := initRepo(t)

	for i := 0; i < 3; i++ {
		if !EnsureExcluded(repo, "_worktrees/") {
			t.Fatalf("EnsureExcluded() = false on call %d", i+1)
		}
	}
	if got := strings.Count(excludeContents(t, repo), "_worktrees/"); got != 1 {
		t.Errorf("_worktrees/ appears %d times in info/exclude, want 1", got)
	}
}

// A repo that already ignores the path through its committed .gitignore
// needs no zen-owned entry in info/exclude.
func TestEnsureExcludedSkipsWhenGitignoreAlreadyCovers(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("_worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !EnsureExcluded(repo, "_worktrees/") {
		t.Fatal("EnsureExcluded() = false when .gitignore already covers the path")
	}
	if got := excludeContents(t, repo); strings.Contains(got, "_worktrees/") {
		t.Errorf("info/exclude was written despite .gitignore already covering the path: %q", got)
	}
}

// A negated pattern beats info/exclude, and reading .gitignore for the
// literal string would miss it. EnsureExcluded must report the failure so
// the caller can tell the user to fix it by hand.
func TestEnsureExcludedReportsNegationOverride(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("!_worktrees\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if EnsureExcluded(repo, "_worktrees/") {
		t.Error("EnsureExcluded() = true, want false when .gitignore negates the pattern")
	}
}

// info/exclude lives in the common git directory, so writing it from inside
// a linked worktree must reach the same file the main checkout reads.
func TestEnsureExcludedFromLinkedWorktree(t *testing.T) {
	repo := initRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "wt")
	run(t, repo, "worktree", "add", "-b", "feature", worktreePath)

	if !EnsureExcluded(worktreePath, ".zen/") {
		t.Fatal("EnsureExcluded() = false from a linked worktree")
	}
	if got := excludeContents(t, repo); !strings.Contains(got, ".zen/") {
		t.Errorf("main clone's info/exclude = %q, want it to contain .zen/", got)
	}
}

// A file with no trailing newline must not have the new ref appended onto
// its last line.
func TestEnsureExcludedPreservesExistingEntries(t *testing.T) {
	repo := initRepo(t)
	excludePath := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.WriteFile(excludePath, []byte("*.log"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !EnsureExcluded(repo, "_worktrees/") {
		t.Fatal("EnsureExcluded() = false")
	}
	if !IsIgnored(repo, "debug.log") {
		t.Error("the pre-existing *.log entry stopped working")
	}
	if !IsIgnored(repo, "_worktrees/") {
		t.Error("_worktrees/ was not added")
	}
}

// Outside a git repository there is no info/exclude to write, and no way to
// make git ignore anything. EnsureExcluded must report that rather than
// panicking or claiming success.
func TestEnsureExcludedOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	if IsIgnored(dir, "_worktrees/") {
		t.Error("IsIgnored() = true outside a repo")
	}
	if EnsureExcluded(dir, "_worktrees/") {
		t.Error("EnsureExcluded() = true outside a repo, want false")
	}
}
