package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// Neutralize any user-level ignore. Without this the fixture inherits
	// the developer's ~/.config/git/ignore, so a machine that follows zen's
	// own advice ("put _worktrees/ in your global ignore") would see these
	// tests pass or fail for reasons unrelated to the code.
	empty := filepath.Join(dir, ".empty-excludes")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	run(repoPath, "config", "core.excludesFile", empty)

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

// A worktree created inside the clone must not show up as untracked in the
// main checkout -- that is the one drawback of the nested layout, and the
// whole reason EnsureNestedExcluded runs during creation.
func TestCreateFromMainNestedIsExcluded(t *testing.T) {
	repoPath := initTestRepo(t)
	worktreePath := filepath.Join(repoPath, NestedDir, "repo-feature")

	if err := CreateFromMain(repoPath, worktreePath, "repo-feature", "mgreau/feature"); err != nil {
		t.Fatalf("CreateFromMain() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "README.md")); err != nil {
		t.Fatalf("expected checked-out worktree, README.md missing: %v", err)
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("git status in the clone = %q, want clean (the worktree should be excluded)", got)
	}
}

// The sibling layout has nothing inside the clone to exclude, so creation
// must not write an entry to info/exclude on its behalf.
func TestCreateFromMainSiblingWritesNoExclude(t *testing.T) {
	repoPath := initTestRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-feature")

	if err := CreateFromMain(repoPath, worktreePath, "repo-feature", "mgreau/feature"); err != nil {
		t.Fatalf("CreateFromMain() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoPath, ".git", "info", "exclude"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "repo-feature") {
		t.Errorf("info/exclude mentions a sibling worktree: %q", data)
	}
}

// seedPRRef publishes a commit as refs/pull/<n>/head on the clone's origin,
// which is the ref CreateFromPR fetches. GitHub creates these; a local
// upstream needs one pushed by hand.
func seedPRRef(t *testing.T, repoPath string, prNumber int, content string) {
	t.Helper()
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(repoPath, "checkout", "-q", "-b", "prwork")
	if err := os.WriteFile(filepath.Join(repoPath, "pr.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repoPath, "add", "pr.txt")
	run(repoPath, "commit", "-m", "pr change")
	run(repoPath, "push", "-q", "origin", fmt.Sprintf("HEAD:refs/pull/%d/head", prNumber))
	run(repoPath, "checkout", "-q", "main")
	run(repoPath, "branch", "-q", "-D", "prwork")
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestCreateFromPR(t *testing.T) {
	repoPath := initTestRepo(t)
	seedPRRef(t, repoPath, 42, "from the PR")
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-pr-42")

	if err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-42", 42, 0, nil); err != nil {
		t.Fatalf("CreateFromPR() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(worktreePath, "pr.txt"))
	if err != nil {
		t.Fatalf("PR content missing from worktree: %v", err)
	}
	if string(data) != "from the PR" {
		t.Errorf("pr.txt = %q, want the PR head content", data)
	}
	if got := currentBranch(t, worktreePath); got != "pr-42" {
		t.Errorf("branch = %q, want pr-42", got)
	}
}

// The review path is where a nested worktree would otherwise show as
// untracked in the main checkout -- it is the most-used creation path.
func TestCreateFromPRNestedIsExcluded(t *testing.T) {
	repoPath := initTestRepo(t)
	seedPRRef(t, repoPath, 42, "from the PR")
	worktreePath := filepath.Join(repoPath, NestedDir, "repo-pr-42")

	if err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-42", 42, 0, nil); err != nil {
		t.Fatalf("CreateFromPR() error: %v", err)
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("git status in the clone = %q, want clean", got)
	}
}

func TestCreateFromPRSiblingWritesNoExclude(t *testing.T) {
	repoPath := initTestRepo(t)
	seedPRRef(t, repoPath, 42, "from the PR")
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-pr-42")

	if err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-42", 42, 0, nil); err != nil {
		t.Fatalf("CreateFromPR() error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repoPath, ".git", "info", "exclude"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "repo-pr-42") {
		t.Errorf("info/exclude mentions a sibling worktree: %q", data)
	}
}

// A concurrent caller may have created it first; the second call must not
// fail, and must not disturb what is already checked out.
func TestCreateFromPRAlreadyExistsIsNoOp(t *testing.T) {
	repoPath := initTestRepo(t)
	seedPRRef(t, repoPath, 42, "from the PR")
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-pr-42")

	if err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-42", 42, 0, nil); err != nil {
		t.Fatalf("first CreateFromPR() error: %v", err)
	}
	marker := filepath.Join(worktreePath, "local-edit.txt")
	if err := os.WriteFile(marker, []byte("in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-42", 42, 0, nil); err != nil {
		t.Fatalf("second CreateFromPR() error: %v, want no-op", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("second call disturbed the existing worktree: %v", err)
	}
}

// A missing pull/N/head must surface as an error, leave no partial worktree,
// and leave no orphaned pr-N branch behind.
func TestCreateFromPRMissingRef(t *testing.T) {
	repoPath := initTestRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-pr-99")

	err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-99", 99, 0, nil)
	if err == nil {
		t.Fatal("expected an error for a PR ref that does not exist")
	}
	if !strings.Contains(err.Error(), "git fetch") {
		t.Errorf("error = %v, want it to name the failing step", err)
	}
	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		t.Error("a partial worktree was left behind")
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "pr-99")
	cmd.Dir = repoPath
	if cmd.Run() == nil {
		t.Error("an orphaned pr-99 branch was left behind")
	}
}

// The timeout has to bound a single git command, not just ctx, so a hung
// fetch cannot hold GitMu open indefinitely.
func TestCreateFromPRTimeout(t *testing.T) {
	repoPath := initTestRepo(t)
	seedPRRef(t, repoPath, 42, "from the PR")
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-pr-42")

	err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-42", 42, time.Nanosecond, nil)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want a timeout error", err)
	}
}

func TestCreateFromPRLogger(t *testing.T) {
	repoPath := initTestRepo(t)
	seedPRRef(t, repoPath, 42, "from the PR")
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-pr-42")

	var msgs []string
	log := Logger(func(m string) { msgs = append(msgs, m) })
	if err := CreateFromPR(context.Background(), repoPath, worktreePath, "repo-pr-42", 42, 0, log); err != nil {
		t.Fatalf("CreateFromPR() error: %v", err)
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "pull/42/head") || !strings.Contains(joined, "repo-pr-42") {
		t.Errorf("progress messages = %q, want the fetch and create steps reported", joined)
	}
}

// A nil Logger is the daemon and MCP case; it must not panic.
func TestLoggerNilIsSafe(t *testing.T) {
	var log Logger
	log.logf("no receiver for %s", "this")
}

// When the exclusion cannot be made to stick, EnsureNestedExcluded warns
// rather than failing the create -- the worktree is still usable, it just
// shows as untracked. Exercised here with a directory that is not a repo.
func TestEnsureNestedExcludedWarnsWhenItCannotExclude(t *testing.T) {
	dir := t.TempDir()
	EnsureNestedExcluded(dir, filepath.Join(dir, NestedDir, "wt")) // must not panic
}

// A worktree beside the clone is not nested, so nothing is written.
func TestEnsureNestedExcludedIgnoresSiblings(t *testing.T) {
	repoPath := initTestRepo(t)
	EnsureNestedExcluded(repoPath, filepath.Join(filepath.Dir(repoPath), "repo-feature"))

	data, err := os.ReadFile(filepath.Join(repoPath, ".git", "info", "exclude"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// git ships a default info/exclude full of comments, so the check is
	// that zen added nothing -- not that the file is empty.
	if strings.Contains(string(data), "repo-feature") {
		t.Errorf("info/exclude was written for a sibling worktree: %q", data)
	}
}

// End to end: with a user-level ignore covering _worktrees/, creating a
// nested worktree leaves the clone's git status clean *and* writes nothing
// into the repository -- which is the whole argument for setting one.
func TestCreateFromMainNestedWritesNothingWhenUserIgnoreCovers(t *testing.T) {
	repoPath := initTestRepo(t)
	userIgnore := filepath.Join(t.TempDir(), "ignore")
	if err := os.WriteFile(userIgnore, []byte("_worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := exec.Command("git", "config", "core.excludesFile", userIgnore)
	cfg.Dir = repoPath
	if out, err := cfg.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	before, _ := os.ReadFile(filepath.Join(repoPath, ".git", "info", "exclude"))

	worktreePath := filepath.Join(repoPath, NestedDir, "repo-feature")
	if err := CreateFromMain(repoPath, worktreePath, "repo-feature", "mgreau/feature"); err != nil {
		t.Fatalf("CreateFromMain() error: %v", err)
	}

	after, _ := os.ReadFile(filepath.Join(repoPath, ".git", "info", "exclude"))
	if string(before) != string(after) {
		t.Errorf("info/exclude changed despite a user-level ignore:\nbefore %q\nafter  %q", before, after)
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("git status = %q, want clean", got)
	}
}
