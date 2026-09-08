package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	wt "github.com/mgreau/zen/internal/worktree"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// setupPRWorktree builds origin + clone + worktree at commit A with
// refs/pull/1/head on origin. Returns clone path, worktree path, shaA.
func setupPRWorktree(t *testing.T) (clone, wtDir, shaA string) {
	t.Helper()
	orig := t.TempDir()
	if err := os.MkdirAll(orig, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, orig, "init", "-b", "main")
	git(t, orig, "config", "user.email", "test@example.com")
	git(t, orig, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(orig, "file.txt"), []byte("A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, orig, "add", "file.txt")
	git(t, orig, "commit", "-m", "A")
	shaA = git(t, orig, "rev-parse", "HEAD")
	git(t, orig, "branch", "pr-1")
	git(t, orig, "update-ref", "refs/pull/1/head", shaA)

	clone = t.TempDir()
	git(t, orig, "clone", orig, clone)
	git(t, clone, "config", "user.email", "test@example.com")
	git(t, clone, "config", "user.name", "test")
	git(t, clone, "fetch", "origin", "pr-1:pr-1")

	wtDir = filepath.Join(t.TempDir(), "repo-pr-1")
	git(t, clone, "worktree", "add", wtDir, "pr-1")
	return clone, wtDir, shaA
}

func pushCommitB(t *testing.T, clone string) string {
	t.Helper()
	// origin is clone's origin remote URL
	url := git(t, clone, "remote", "get-url", "origin")
	if err := os.WriteFile(filepath.Join(url, "file.txt"), []byte("B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, url, "add", "file.txt")
	git(t, url, "commit", "-m", "B")
	shaB := git(t, url, "rev-parse", "HEAD")
	git(t, url, "branch", "-f", "pr-1", shaB)
	git(t, url, "update-ref", "refs/pull/1/head", shaB)
	return shaB
}

func TestSyncExisting_fastForwardOnNewHead(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaB := pushCommitB(t, clone)

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncUpdated {
		t.Fatalf("outcome=%s want updated", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaB {
		t.Fatalf("HEAD=%s want %s (started at %s)", head, shaB, shaA)
	}
}

func TestSyncExisting_upToDate(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaA, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncUpToDate {
		t.Fatalf("outcome=%s want up-to-date", outcome)
	}
}

func TestSyncExisting_skipDirty(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaB := pushCommitB(t, clone)
	if err := os.WriteFile(filepath.Join(wtDir, "file.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncSkippedDirty {
		t.Fatalf("outcome=%s want skipped-dirty", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaA {
		t.Fatalf("HEAD moved to %s, want still %s", head, shaA)
	}
	got, err := os.ReadFile(filepath.Join(wtDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dirty\n" {
		t.Fatalf("local edits discarded: %q", got)
	}
}

func TestSyncExisting_untrackedContextDoesNotBlock(t *testing.T) {
	clone, wtDir, _ := setupPRWorktree(t)
	shaB := pushCommitB(t, clone)
	if err := os.WriteFile(filepath.Join(wtDir, "CLAUDE.local.md"), []byte("ctx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncUpdated {
		t.Fatalf("outcome=%s want updated", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaB {
		t.Fatalf("HEAD=%s want %s", head, shaB)
	}
}

func TestSyncExisting_skipAgent(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaB := pushCommitB(t, clone)
	old := runningIn
	runningIn = func(string) bool { return true }
	t.Cleanup(func() { runningIn = old })

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncSkippedAgent {
		t.Fatalf("outcome=%s want skipped-agent", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaA {
		t.Fatalf("HEAD moved to %s, want still %s", head, shaA)
	}
}

func rewriteCommit(t *testing.T, clone, shaA string) string {
	t.Helper()
	url := git(t, clone, "remote", "get-url", "origin")
	git(t, url, "checkout", "pr-1")
	if err := os.WriteFile(filepath.Join(url, "file.txt"), []byte("rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, url, "add", "file.txt")
	git(t, url, "commit", "--amend", "-m", "rewritten")
	sha := git(t, url, "rev-parse", "HEAD")
	if sha == shaA {
		t.Fatal("amend did not rewrite SHA")
	}
	git(t, url, "update-ref", "refs/pull/1/head", sha)
	return sha
}

func TestSyncExisting_rewrittenHead(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaNew := rewriteCommit(t, clone, shaA)

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaNew, nil, func(ResetRequest) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncUpdated {
		t.Fatalf("outcome=%s want updated", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaNew {
		t.Fatalf("HEAD=%s want rewritten %s (was %s)", head, shaNew, shaA)
	}
	got, err := os.ReadFile(filepath.Join(wtDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "rewritten\n" {
		t.Fatalf("worktree file = %q", got)
	}
}

func TestSyncExisting_rewrittenUnattended(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaNew := rewriteCommit(t, clone, shaA)

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaNew, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncSkippedReset {
		t.Fatalf("outcome=%s want skipped-reset (daemon/MCP must not reset --hard)", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaA {
		t.Fatalf("HEAD moved to %s, want still %s", head, shaA)
	}
}

func TestSyncExisting_rewrittenDeclined(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaNew := rewriteCommit(t, clone, shaA)
	var got ResetRequest
	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaNew, nil, func(req ResetRequest) bool {
		got = req
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncSkippedReset {
		t.Fatalf("outcome=%s want skipped-reset", outcome)
	}
	if got.PRNumber != 1 {
		t.Fatalf("ResetRequest.PRNumber=%d", got.PRNumber)
	}
	if got.UniqueCommits < 1 {
		t.Fatalf("UniqueCommits=%d, want at least 1 (diverged history)", got.UniqueCommits)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaA {
		t.Fatalf("HEAD moved to %s, want still %s", head, shaA)
	}
}

func TestSyncExisting_rewrittenDirty(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaNew := rewriteCommit(t, clone, shaA)
	if err := os.WriteFile(filepath.Join(wtDir, "file.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaNew, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncSkippedDirty {
		t.Fatalf("outcome=%s want skipped-dirty", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaA {
		t.Fatalf("HEAD moved to %s, want still %s", head, shaA)
	}
	got, err := os.ReadFile(filepath.Join(wtDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dirty\n" {
		t.Fatalf("local edits discarded: %q", got)
	}
}

func TestSyncExisting_rewrittenAgent(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaNew := rewriteCommit(t, clone, shaA)
	old := runningIn
	runningIn = func(string) bool { return true }
	t.Cleanup(func() { runningIn = old })

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaNew, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncSkippedAgent {
		t.Fatalf("outcome=%s want skipped-agent", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaA {
		t.Fatalf("HEAD moved to %s, want still %s", head, shaA)
	}
}

func TestSyncExisting_missingPullRef(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	url := git(t, clone, "remote", "get-url", "origin")
	git(t, url, "update-ref", "-d", "refs/pull/1/head")

	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nil, nil)
	if err == nil {
		t.Fatal("expected fetch error when pull ref is gone")
	}
	if !wt.IsMissingPRRef(err) {
		t.Fatalf("IsMissingPRRef(%v) = false", err)
	}
	if outcome != SyncUpToDate {
		t.Fatalf("outcome=%s", outcome)
	}
	head := git(t, wtDir, "rev-parse", "HEAD")
	if head != shaA {
		t.Fatalf("HEAD moved to %s after failed fetch", head)
	}
}

func TestSyncExisting_missing(t *testing.T) {
	outcome, err := SyncExisting(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "nope"), 1, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncMissing {
		t.Fatalf("outcome=%s want missing", outcome)
	}
}

// rewindPullRef points GitHub's pull/1/head back at an earlier commit, the
// shape left behind by a force-push that undoes work.
func rewindPullRef(t *testing.T, clone, sha string) {
	t.Helper()
	url := git(t, clone, "remote", "get-url", "origin")
	git(t, url, "branch", "-f", "pr-1", sha)
	git(t, url, "update-ref", "refs/pull/1/head", sha)
}

// advanceToB fast-forwards the worktree onto a pushed commit B, so the
// backward-force-push tests start from a worktree that is genuinely ahead.
func advanceToB(t *testing.T, clone, wtDir string) string {
	t.Helper()
	shaB := pushCommitB(t, clone)
	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncUpdated {
		t.Fatalf("setup: outcome=%s want updated", outcome)
	}
	return shaB
}

func TestSyncExisting_backwardForcePushUnattended(t *testing.T) {
	// `git merge --ff-only A` succeeds with "Already up to date" when A is an
	// ancestor of HEAD. Reporting that as an update rewrote context and fired a
	// notification on every poll while HEAD never moved.
	clone, wtDir, shaA := setupPRWorktree(t)
	shaB := advanceToB(t, clone, wtDir)
	rewindPullRef(t, clone, shaA)

	for i := range 2 { // the false update repeated on every poll
		outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaA, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if outcome != SyncSkippedReset {
			t.Fatalf("poll %d: outcome=%s want skipped-reset", i, outcome)
		}
		head := git(t, wtDir, "rev-parse", "HEAD")
		if head != shaB {
			t.Fatalf("poll %d: HEAD=%s want still %s", i, head, shaB)
		}
	}
}

func TestSyncExisting_backwardForcePushRequestsReset(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	advanceToB(t, clone, wtDir)
	rewindPullRef(t, clone, shaA)

	var got ResetRequest
	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaA, nil, func(req ResetRequest) bool {
		got = req
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncUpdated {
		t.Fatalf("outcome=%s want updated after a confirmed reset", outcome)
	}
	if head := git(t, wtDir, "rev-parse", "HEAD"); head != shaA {
		t.Fatalf("HEAD=%s want %s", head, shaA)
	}
	if got.Kind != ResetBehind {
		t.Fatalf("ResetRequest.Kind=%s want behind", got.Kind)
	}
	if got.UniqueCommits != 1 {
		t.Fatalf("UniqueCommits=%d want 1 (commit B leaves the branch)", got.UniqueCommits)
	}
}

func TestSyncExisting_localCommitAheadIsNotAnUpdate(t *testing.T) {
	// A review commit made in the worktree also leaves the fetched head an
	// ancestor of HEAD. The daemon must not claim an update or discard it.
	clone, wtDir, shaA := setupPRWorktree(t)
	if err := os.WriteFile(filepath.Join(wtDir, "notes.txt"), []byte("review notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wtDir, "add", "notes.txt")
	git(t, wtDir, "commit", "-m", "local review commit")
	local := git(t, wtDir, "rev-parse", "HEAD")

	var got ResetRequest
	outcome, err := SyncExisting(context.Background(), clone, wtDir, 1, shaA, nil, func(req ResetRequest) bool {
		got = req
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SyncSkippedReset {
		t.Fatalf("outcome=%s want skipped-reset", outcome)
	}
	if head := git(t, wtDir, "rev-parse", "HEAD"); head != local {
		t.Fatalf("HEAD=%s want local commit %s kept", head, local)
	}
	if got.Kind != ResetBehind {
		t.Fatalf("ResetRequest.Kind=%s want behind", got.Kind)
	}
	if got.UniqueCommits != 1 {
		t.Fatalf("UniqueCommits=%d want 1", got.UniqueCommits)
	}
	if _, err := os.Stat(filepath.Join(wtDir, "notes.txt")); err != nil {
		t.Fatalf("local commit contents lost: %v", err)
	}
}

func TestSyncExisting_divergedReportsDivergedKind(t *testing.T) {
	clone, wtDir, shaA := setupPRWorktree(t)
	shaNew := rewriteCommit(t, clone, shaA)

	var got ResetRequest
	if _, err := SyncExisting(context.Background(), clone, wtDir, 1, shaNew, nil, func(req ResetRequest) bool {
		got = req
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if got.Kind != ResetDiverged {
		t.Fatalf("ResetRequest.Kind=%s want diverged", got.Kind)
	}
}
