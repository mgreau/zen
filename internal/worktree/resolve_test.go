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

// TestResolveMatchesLegacyPathUnderDefaultConfig is the "nothing breaks"
// guarantee. Before Resolve existed, every call site computed
// filepath.Join(basePath, name). With the default config -- which is what
// every existing user has until they opt in -- Resolve must return that
// exact string, in each of the states a real repo can be in.
//
// The string identity matters beyond mere correctness: worktree paths are
// agent-session keys (Claude encodes the path into a ~/.claude/projects
// directory name), so a differently-spelled path for the same worktree
// would orphan resumable history.
func TestResolveMatchesLegacyPathUnderDefaultConfig(t *testing.T) {
	repoPath := testRepo(t)
	base := filepath.Dir(repoPath)

	// Default config: worktree_layout unset entirely.
	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{
			"origin": {FullName: "acme/origin", BasePath: base},
		},
	}
	legacy := func(name string) string { return filepath.Join(base, name) }

	t.Run("no worktree registered", func(t *testing.T) {
		if got := Resolve(cfg, "origin", "origin-pr-42"); got != legacy("origin-pr-42") {
			t.Errorf("Resolve() = %q, want the legacy path %q", got, legacy("origin-pr-42"))
		}
	})

	t.Run("worktree registered where the old code put it", func(t *testing.T) {
		gitIn(t, repoPath, "worktree", "add", "-b", "pr-7", legacy("origin-pr-7"))
		if got := Resolve(cfg, "origin", "origin-pr-7"); got != legacy("origin-pr-7") {
			t.Errorf("Resolve() = %q, want the legacy path %q", got, legacy("origin-pr-7"))
		}
	})

	t.Run("feature and slack worktree names", func(t *testing.T) {
		for _, name := range []string{"origin-add-oidc-claims", "origin-slack-1712345678"} {
			if got := Resolve(cfg, "origin", name); got != legacy(name) {
				t.Errorf("Resolve(%q) = %q, want %q", name, got, legacy(name))
			}
		}
	})

	t.Run("clone missing entirely", func(t *testing.T) {
		gone := &config.Config{Repos: map[string]config.RepoConfig{
			"origin": {FullName: "acme/origin", BasePath: filepath.Join(base, "nonexistent")},
		}}
		want := filepath.Join(base, "nonexistent", "origin-pr-42")
		if got := Resolve(gone, "origin", "origin-pr-42"); got != want {
			t.Errorf("Resolve() = %q, want %q -- a failed git call must fall back, not blank out", got, want)
		}
	})
}

// The one default-config behaviour that does change, stated deliberately:
// a worktree registered to this clone but living outside base_path used to
// be invisible, so zen would try to create a duplicate and git would refuse
// because the branch was already checked out. Resolve finds it instead.
func TestResolveFindsWorktreeOutsideBasePath(t *testing.T) {
	repoPath := testRepo(t)
	base := filepath.Dir(repoPath)

	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	strayPath := filepath.Join(elsewhere, "origin-pr-42")
	gitIn(t, repoPath, "worktree", "add", "-b", "pr-42", strayPath)

	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"origin": {FullName: "acme/origin", BasePath: base},
	}}
	if got := Resolve(cfg, "origin", "origin-pr-42"); got != strayPath {
		t.Errorf("Resolve() = %q, want the registered worktree %q", got, strayPath)
	}
}

// samePath falls back to a cleaned comparison when EvalSymlinks cannot
// resolve a path, which is the normal case for a worktree that has not been
// created yet.
func TestSamePath(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical strings", "/a/b", "/a/b", true},
		{"both nonexistent, equal after cleaning", "/a/b/../b", "/a/b", true},
		{"both nonexistent, different", "/a/b", "/a/c", false},
		{"empty left", "", "/a/b", false},
		{"empty right", "/a/b", "", false},
		{"both empty", "", "", true},
		{"existing vs nonexistent", dir, filepath.Join(dir, "nope"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := samePath(tt.a, tt.b); got != tt.want {
				t.Errorf("samePath(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// A symlinked path and its target are the same location.
func TestSamePathFollowsSymlinks(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(real, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !samePath(link, target) {
		t.Errorf("samePath(%q, %q) = false, want true", link, target)
	}
}

// registeredPath must not blow up when the directory is not a git repo.
func TestRegisteredPathNonRepo(t *testing.T) {
	if got := registeredPath(t.TempDir(), "anything", ""); got != "" {
		t.Errorf("registeredPath() in a non-repo = %q, want empty", got)
	}
}
