package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mgreau/zen/internal/config"
)

// layoutCfg builds a config whose single repo "origin" lives at
// <basePath>/origin, matching the clone initTestRepo produces.
func layoutCfg(basePath, global, perRepo string) *config.Config {
	return &config.Config{
		WorktreeLayout: global,
		Repos: map[string]config.RepoConfig{
			"origin": {
				FullName:       "acme/origin",
				BasePath:       basePath,
				WorktreeLayout: perRepo,
			},
		},
	}
}

// testRepo is initTestRepo with the clone path fully resolved. Git reports
// resolved paths, and on macOS t.TempDir() sits under the /var -> /private/var
// symlink, so an unresolved base would make every assertion here compare two
// spellings of the same directory.
func testRepo(t *testing.T) string {
	t.Helper()
	repoPath, err := filepath.EvalSymlinks(initTestRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	return repoPath
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

func TestNewPath(t *testing.T) {
	base := "/repos"
	tests := []struct {
		name    string
		global  string
		perRepo string
		want    string
	}{
		{"defaults to sibling", "", "", "/repos/origin-pr-42"},
		{"explicit sibling", config.LayoutSibling, "", "/repos/origin-pr-42"},
		{"nested", config.LayoutNested, "", "/repos/origin/_worktrees/origin-pr-42"},
		{"per-repo overrides global nested", config.LayoutNested, config.LayoutSibling, "/repos/origin-pr-42"},
		{"per-repo overrides global sibling", config.LayoutSibling, config.LayoutNested, "/repos/origin/_worktrees/origin-pr-42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewPath(layoutCfg(base, tt.global, tt.perRepo), "origin", "origin-pr-42")
			if got != tt.want {
				t.Errorf("NewPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewPathUnknownRepo(t *testing.T) {
	cfg := layoutCfg("/repos", config.LayoutNested, "")
	if got := NewPath(cfg, "nope", "nope-pr-1"); got != "" {
		t.Errorf("NewPath() for unconfigured repo = %q, want empty", got)
	}
}

// TestResolveFindsSiblingUnderNestedLayout is the regression this whole
// change turns on. Switching worktree_layout must not strand the worktrees
// created under the previous setting: cleanup would compute a path that
// does not exist and report success without removing anything, and setup
// would try a second `git worktree add` for a branch already checked out.
func TestResolveFindsSiblingUnderNestedLayout(t *testing.T) {
	repoPath := testRepo(t)
	base := filepath.Dir(repoPath)

	// A worktree created the old way, beside the clone.
	siblingPath := filepath.Join(base, "origin-pr-42")
	gitIn(t, repoPath, "worktree", "add", "-b", "pr-42", siblingPath)

	cfg := layoutCfg(base, config.LayoutNested, "")

	if got := Resolve(cfg, "origin", "origin-pr-42"); got != siblingPath {
		t.Errorf("Resolve() = %q, want the existing sibling %q", got, siblingPath)
	}
	// The layout still governs anything not yet created.
	wantNew := filepath.Join(repoPath, NestedDir, "origin-pr-99")
	if got := Resolve(cfg, "origin", "origin-pr-99"); got != wantNew {
		t.Errorf("Resolve() for a new worktree = %q, want %q", got, wantNew)
	}
}

// The reverse direction has to hold too, or rolling the setting back would
// strand everything created while it was on.
func TestResolveFindsNestedUnderSiblingLayout(t *testing.T) {
	repoPath := testRepo(t)
	base := filepath.Dir(repoPath)

	nestedPath := filepath.Join(repoPath, NestedDir, "origin-pr-42")
	gitIn(t, repoPath, "worktree", "add", "-b", "pr-42", nestedPath)

	cfg := layoutCfg(base, config.LayoutSibling, "")

	if got := Resolve(cfg, "origin", "origin-pr-42"); got != nestedPath {
		t.Errorf("Resolve() = %q, want the existing nested worktree %q", got, nestedPath)
	}
}

// A registration whose directory is gone must not pin a new worktree to the
// old layout -- git keeps listing it as prunable until someone prunes.
func TestResolveIgnoresPrunableRegistration(t *testing.T) {
	repoPath := testRepo(t)
	base := filepath.Dir(repoPath)

	siblingPath := filepath.Join(base, "origin-pr-42")
	gitIn(t, repoPath, "worktree", "add", "-b", "pr-42", siblingPath)
	if err := os.RemoveAll(siblingPath); err != nil {
		t.Fatal(err)
	}

	cfg := layoutCfg(base, config.LayoutNested, "")
	want := filepath.Join(repoPath, NestedDir, "origin-pr-42")
	if got := Resolve(cfg, "origin", "origin-pr-42"); got != want {
		t.Errorf("Resolve() = %q, want the configured layout %q for a prunable registration", got, want)
	}
}

// The main worktree shares the clone's directory name in some layouts; it
// must never be returned as if it were a linked worktree.
func TestResolveNeverReturnsMainWorktree(t *testing.T) {
	repoPath := testRepo(t)
	base := filepath.Dir(repoPath)

	cfg := layoutCfg(base, config.LayoutSibling, "")
	want := filepath.Join(base, "origin")
	got := Resolve(cfg, "origin", "origin")
	if got != want {
		t.Errorf("Resolve() = %q, want the computed path %q", got, want)
	}
	// Confirm it came from the layout, not from matching the main worktree
	// registration -- both happen to be the same string here, so assert the
	// helper itself skipped it.
	if p := registeredPath(repoPath, "origin", ""); p != "" {
		t.Errorf("registeredPath() matched the main worktree: %q", p)
	}
}

func TestResolveUnconfiguredRepo(t *testing.T) {
	cfg := layoutCfg("/repos", config.LayoutNested, "")
	if got := Resolve(cfg, "nope", "nope-pr-1"); got != "" {
		t.Errorf("Resolve() for unconfigured repo = %q, want empty", got)
	}
}
