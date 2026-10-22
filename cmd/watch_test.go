package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/config"
	wt "github.com/mgreau/zen/internal/worktree"
)

func watchGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The poll decides between "set up" and "refresh" from whether the review
// worktree exists and where its HEAD is. It has to find the worktree
// wherever git registered it: with the nested layout configured, a lookup
// that only builds the sibling path reports an existing review as missing,
// and for a PR author outside the allowlist that means no refresh is ever
// queued when the head moves.
func TestReviewWorktreeHEADFollowsLayout(t *testing.T) {
	tests := []struct {
		name   string
		layout string
		place  func(base, origin string) string
	}{
		{"nested worktree, nested layout", config.LayoutNested, func(_, origin string) string {
			return filepath.Join(origin, wt.NestedDir, "mono-pr-42")
		}},
		{"sibling worktree, nested layout", config.LayoutNested, func(base, _ string) string {
			return filepath.Join(base, "mono-pr-42")
		}},
		{"nested worktree, sibling layout", config.LayoutSibling, func(_, origin string) string {
			return filepath.Join(origin, wt.NestedDir, "mono-pr-42")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			origin := filepath.Join(base, "mono")
			watchGit(t, base, "init", "-b", "main", origin)
			watchGit(t, origin, "config", "user.email", "test@example.com")
			watchGit(t, origin, "config", "user.name", "Test")
			watchGit(t, origin, "config", "commit.gpgsign", "false")
			if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("hi"), 0o644); err != nil {
				t.Fatal(err)
			}
			watchGit(t, origin, "add", "README.md")
			watchGit(t, origin, "commit", "-m", "initial")
			worktreePath := tt.place(base, origin)
			watchGit(t, origin, "worktree", "add", "-b", "pr-42", worktreePath)
			wantHead := watchGit(t, worktreePath, "rev-parse", "HEAD")

			c := &config.Config{
				WorktreeLayout: tt.layout,
				Repos: map[string]config.RepoConfig{
					"mono": {FullName: "example/mono", BasePath: base},
				},
			}
			exists, head := reviewWorktreeHEAD(c, "mono", 42)
			if !exists {
				t.Fatalf("review worktree at %s reported missing", worktreePath)
			}
			if head != wantHead {
				t.Errorf("head = %q, want %q", head, wantHead)
			}

			if exists, head := reviewWorktreeHEAD(c, "mono", 7); exists || head != "" {
				t.Errorf("absent review: exists=%v head=%q, want false and empty", exists, head)
			}
		})
	}
}

// A repo with no base_path has no worktree to find; the lookup must say so
// rather than stat a relative path.
func TestReviewWorktreeHEADUnconfiguredRepo(t *testing.T) {
	c := &config.Config{Repos: map[string]config.RepoConfig{}}
	if exists, head := reviewWorktreeHEAD(c, "mono", 42); exists || head != "" {
		t.Errorf("unconfigured repo: exists=%v head=%q, want false and empty", exists, head)
	}
}
