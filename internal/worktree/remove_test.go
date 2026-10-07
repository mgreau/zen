package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/agent"
)

func removalFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "repo")
	path := filepath.Join(root, "repo-feature")
	runRemovalGit(t, root, "init", "-b", "main", origin)
	runRemovalGit(t, origin, "config", "user.email", "test@example.com")
	runRemovalGit(t, origin, "config", "user.name", "Test")
	runRemovalGit(t, origin, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(origin, "tracked"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRemovalGit(t, origin, "add", "tracked")
	runRemovalGit(t, origin, "commit", "-m", "initial")
	runRemovalGit(t, origin, "worktree", "add", "-b", "feature", path)
	return origin, path
}

func TestRemove(t *testing.T) {
	tests := []struct {
		name           string
		prepare        func(*testing.T, string)
		wantErr        error
		preserveAgents bool
	}{
		{name: "clean"},
		{name: "tracked change", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "tracked", "changed")
		}, wantErr: ErrWorktreeDirty},
		{name: "staged change", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "staged", "new")
			runRemovalGit(t, path, "add", "staged")
		}, wantErr: ErrWorktreeDirty},
		{name: "untracked file", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "notes.txt", "keep me")
		}, wantErr: ErrWorktreeDirty},
		{name: "ignored file", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, ".gitignore", "ignored.log\n")
			runRemovalGit(t, path, "add", ".gitignore")
			runRemovalGit(t, path, "commit", "-m", "ignore generated log")
			writeRemovalFile(t, path, "ignored.log", "keep me")
		}, wantErr: ErrWorktreeDirty},
		{name: "claude context", prepare: func(t *testing.T, path string) {
			if _, err := agent.New(agent.Claude, "").InjectContext(path, "generated"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "codex context", prepare: func(t *testing.T, path string) {
			if _, err := agent.New(agent.Codex, "").InjectContext(path, "generated"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "codex side context preserves user agents file", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "AGENTS.md", "user owned")
			if _, err := agent.New(agent.Codex, "").InjectContext(path, "generated"); err != nil {
				t.Fatal(err)
			}
		}, wantErr: ErrWorktreeDirty, preserveAgents: true},
		{name: "legacy codex sentinel preserves user agents and side context", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "AGENTS.md", "user owned")
			writeRemovalFile(t, path, ".zen/PR_CONTEXT.md", "generated")
			writeRemovalFile(t, path, ".zen/.pr_context_injected", "")
		}, wantErr: ErrWorktreeDirty, preserveAgents: true},
		{name: "legacy codex sentinel preserves user agents after side context deletion", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "AGENTS.md", "user owned")
			writeRemovalFile(t, path, ".zen/.pr_context_injected", "")
		}, wantErr: ErrWorktreeDirty, preserveAgents: true},
		{name: "claude file without sentinel", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "CLAUDE.local.md", "user owned")
		}, wantErr: ErrWorktreeDirty},
		{name: "agents file without sentinel", prepare: func(t *testing.T, path string) {
			writeRemovalFile(t, path, "AGENTS.md", "user owned")
		}, wantErr: ErrWorktreeDirty},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			origin, path := removalFixture(t)
			if test.prepare != nil {
				test.prepare(t, path)
			}
			err := remove(origin, path, func(string) bool { return false }, nil)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Remove() error = %v, want %v", err, test.wantErr)
			}
			_, statErr := os.Stat(path)
			if test.wantErr != nil && statErr != nil {
				t.Fatal("refused removal removed the worktree")
			}
			if test.preserveAgents {
				data, err := os.ReadFile(filepath.Join(path, "AGENTS.md"))
				if err != nil || string(data) != "user owned" {
					t.Fatalf("user-owned AGENTS.md was not preserved: data=%q err=%v", data, err)
				}
			}
			if test.wantErr == nil && !os.IsNotExist(statErr) {
				t.Fatal("successful removal left the worktree")
			}
		})
	}
}

func TestRemoveGitFailurePreservesWorktree(t *testing.T) {
	_, path := removalFixture(t)
	if _, err := agent.New(agent.Claude, "").InjectContext(path, "generated"); err != nil {
		t.Fatal(err)
	}
	err := remove(filepath.Join(t.TempDir(), "not-a-repository"), path, func(string) bool { return false }, nil)
	if err == nil || RemovalBlocked(err) {
		t.Fatalf("Remove() error = %v, want Git failure", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("Git failure removed the worktree")
	}
	for _, name := range []string{"CLAUDE.local.md", ".zen/.claude_context_injected"} {
		if _, err := os.Stat(filepath.Join(path, filepath.FromSlash(name))); err != nil {
			t.Errorf("Git failure did not restore %s: %v", name, err)
		}
	}
}

func TestRemoveMissingIsIdempotent(t *testing.T) {
	if err := Remove(t.TempDir(), filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveRefusesRunningAgent(t *testing.T) {
	origin, path := removalFixture(t)
	err := remove(origin, path, func(string) bool { return true }, nil)
	if !errors.Is(err, ErrWorktreeActive) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrWorktreeActive)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("active-agent refusal removed the worktree")
	}
}

// reviewFixture lays out a PR review the way CreateFromPR does: an upstream
// with refs/pull/123/head, a clone that fetched it into pr-123, and the
// review worktree repo-pr-123 checked out on that branch. Returns the
// upstream, the clone, the worktree path, and the PR head commit.
func reviewFixture(t *testing.T) (upstream, origin, path, prHead string) {
	t.Helper()
	root := t.TempDir()
	upstream = filepath.Join(root, "upstream")
	origin = filepath.Join(root, "repo")
	path = filepath.Join(root, "repo-pr-123")
	runRemovalGit(t, root, "init", "-b", "main", upstream)
	runRemovalGit(t, upstream, "config", "user.email", "test@example.com")
	runRemovalGit(t, upstream, "config", "user.name", "Test")
	runRemovalGit(t, upstream, "config", "commit.gpgsign", "false")
	writeRemovalFile(t, upstream, "tracked", "base")
	runRemovalGit(t, upstream, "add", "tracked")
	runRemovalGit(t, upstream, "commit", "-m", "initial")
	runRemovalGit(t, upstream, "checkout", "-b", "author")
	prHead = commitRemovalFile(t, upstream, "tracked", "author change")
	runRemovalGit(t, upstream, "update-ref", "refs/pull/123/head", prHead)
	runRemovalGit(t, upstream, "checkout", "main")

	runRemovalGit(t, root, "clone", upstream, origin)
	runRemovalGit(t, origin, "config", "user.email", "test@example.com")
	runRemovalGit(t, origin, "config", "user.name", "Test")
	runRemovalGit(t, origin, "config", "commit.gpgsign", "false")
	runRemovalGit(t, origin, "fetch", "origin", "+pull/123/head:pr-123")
	runRemovalGit(t, origin, "worktree", "add", path, "pr-123")
	return upstream, origin, path, prHead
}

// pushPRHead adds a commit to the PR on upstream only, so the clone does not
// have it until something fetches pull/123/head.
func pushPRHead(t *testing.T, upstream string) string {
	t.Helper()
	runRemovalGit(t, upstream, "checkout", "author")
	sha := commitRemovalFile(t, upstream, "tracked", "author follow-up")
	runRemovalGit(t, upstream, "update-ref", "refs/pull/123/head", sha)
	runRemovalGit(t, upstream, "checkout", "main")
	return sha
}

func TestRemoveReview(t *testing.T) {
	const missingSHA = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name string
		// prepare returns the PR head GitHub reports; empty keeps the fixture's.
		prepare  func(t *testing.T, upstream, origin, path string) string
		noPRHead bool
		wantErr  error
	}{
		{name: "at PR head with only zen context", prepare: func(t *testing.T, _, _, path string) string {
			if _, err := agent.New(agent.Claude, "").InjectContext(path, "generated"); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
		{name: "behind a PR head that is not fetched yet", prepare: func(t *testing.T, upstream, _, _ string) string {
			return pushPRHead(t, upstream)
		}},
		{name: "detached at PR head", prepare: func(t *testing.T, _, _, path string) string {
			runRemovalGit(t, path, "checkout", "--detach")
			return ""
		}},
		{name: "local commit beyond PR head", prepare: func(t *testing.T, _, _, path string) string {
			commitRemovalFile(t, path, "tracked", "reviewer fix")
			return ""
		}, wantErr: ErrWorktreeLocalCommits},
		{name: "local commit after PR head moved", prepare: func(t *testing.T, upstream, _, path string) string {
			commitRemovalFile(t, path, "local", "reviewer fix")
			return pushPRHead(t, upstream)
		}, wantErr: ErrWorktreeLocalCommits},
		{name: "PR head missing after fetch", prepare: func(t *testing.T, _, _, _ string) string {
			return missingSHA
		}, wantErr: ErrWorktreeLocalCommits},
		{name: "PR head not local and fetch fails", prepare: func(t *testing.T, upstream, origin, _ string) string {
			sha := pushPRHead(t, upstream)
			runRemovalGit(t, origin, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone"))
			return sha
		}, wantErr: ErrWorktreeLocalCommits},
		{name: "no PR head from GitHub", noPRHead: true, wantErr: ErrWorktreeLocalCommits},
		{name: "work branch at review path", prepare: func(t *testing.T, _, _, path string) string {
			runRemovalGit(t, path, "checkout", "-b", "mgreau/pr-123")
			return ""
		}, wantErr: ErrNotPRReview},
		{name: "untracked file still blocks", prepare: func(t *testing.T, _, _, path string) string {
			writeRemovalFile(t, path, "notes.txt", "keep me")
			return ""
		}, wantErr: ErrWorktreeDirty},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream, origin, path, prHead := reviewFixture(t)
			if test.prepare != nil {
				if head := test.prepare(t, upstream, origin, path); head != "" {
					prHead = head
				}
			}
			if test.noPRHead {
				prHead = ""
			}
			err := removeReview(context.Background(), origin, path, 123, prHead, func(string) bool { return false })
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("RemoveReview() error = %v, want %v", err, test.wantErr)
			}
			_, statErr := os.Stat(path)
			if test.wantErr != nil {
				if !RemovalBlocked(err) {
					t.Errorf("RemovalBlocked(%v) = false, want a safety refusal", err)
				}
				if statErr != nil {
					t.Fatal("refused removal removed the worktree")
				}
				return
			}
			if !os.IsNotExist(statErr) {
				t.Fatal("successful removal left the worktree")
			}
		})
	}
}

func commitRemovalFile(t *testing.T, dir, name, message string) string {
	t.Helper()
	writeRemovalFile(t, dir, name, message)
	runRemovalGit(t, dir, "add", name)
	runRemovalGit(t, dir, "commit", "-m", message)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func writeRemovalFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runRemovalGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
