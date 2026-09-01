package worktree

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ui"
)

// NestedDir holds worktrees inside the clone under the "nested" layout.
//
// The leading underscore is load-bearing, not decoration: the Go tool skips
// directories whose names begin with "_" or ".", so `go build ./...`,
// `go vet ./...`, and `go list ./...` do not descend into checked-out
// worktrees. Renaming this to something without the prefix would break
// ./... for every Go repo zen manages.
const NestedDir = "_worktrees"

// OriginPath returns the path to a repo's main clone. Empty when the repo
// is not configured. The clone never moves between layouts -- only the
// worktrees do.
func OriginPath(cfg *config.Config, repo string) string {
	basePath := cfg.RepoBasePath(repo)
	if basePath == "" {
		return ""
	}
	return filepath.Join(basePath, repo)
}

// NewPath returns where a worktree named name would be created for repo
// under the configured layout. It says nothing about what exists on disk;
// use Resolve to find a worktree that may already have been created.
func NewPath(cfg *config.Config, repo, name string) string {
	basePath := cfg.RepoBasePath(repo)
	if basePath == "" {
		return ""
	}
	if cfg.WorktreeLayoutFor(repo) == config.LayoutNested {
		return filepath.Join(basePath, repo, NestedDir, name)
	}
	return filepath.Join(basePath, name)
}

// Resolve returns the on-disk path of the worktree named name in repo.
//
// A worktree git has already registered is located through git, wherever it
// lives; the configured layout decides only where a *new* one goes. That
// asymmetry is the point. Computing the path from the layout alone would
// strand every worktree created under the previous setting, and it would do
// so silently: cleanup's os.Stat would miss the old path and take its
// "already removed" branch, reporting success without removing anything,
// while setup would find nothing and call `git worktree add` for a branch
// git already has checked out elsewhere -- rejected on every poll, through
// the full retry backoff, forever.
//
// Resolving through git instead makes both layouts work at once and makes
// switching reversible in both directions, which is what lets existing
// worktrees drain on their own rather than needing to be migrated.
func Resolve(cfg *config.Config, repo, name string) string {
	newPath := NewPath(cfg, repo, name)
	if p := registeredPath(OriginPath(cfg, repo), name, newPath); p != "" {
		return p
	}
	return newPath
}

// samePath reports whether two paths name the same location, following
// symlinks. Git reports fully resolved paths while zen builds its own from
// base_path as the user wrote it, so a symlinked component anywhere above
// the repo (a symlinked home, /tmp on macOS) makes plain string comparison
// disagree about paths that are in fact identical.
//
// EvalSymlinks fails on a path that does not exist; a cleaned comparison is
// the best available answer then.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

// registeredPath finds a worktree by directory name among those git has
// registered for the clone at originPath, returning "" when there is none.
//
// Prunable registrations -- git still lists the worktree but its directory
// is gone -- are skipped, so a stale entry from the old layout cannot pin a
// new worktree to it.
//
// When a registration is the same location as prefer, prefer is returned
// rather than git's spelling of it. Callers use this path as a map key for
// agent sessions (Claude encodes the worktree path into a directory name),
// so handing back a different string for an unchanged worktree would orphan
// its history.
func registeredPath(originPath, name, prefer string) string {
	if originPath == "" {
		return ""
	}

	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = originPath
	out, err := cmd.Output()
	if err != nil {
		ui.LogDebug(fmt.Sprintf("git worktree list failed in %s: %v", originPath, err))
		return ""
	}

	var (
		matches  []string
		path     string
		prunable bool
	)
	// Porcelain output is one blank-line-separated record per worktree,
	// each opening with "worktree <path>"; flush the previous record when
	// the next one starts, and once more at EOF.
	flush := func() {
		if path != "" && !prunable && filepath.Base(path) == name && !samePath(path, originPath) {
			matches = append(matches, path)
		}
		path, prunable = "", false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			path = strings.TrimPrefix(line, "worktree ")
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			prunable = true
		}
	}
	flush()

	for _, m := range matches {
		if samePath(m, prefer) {
			return prefer
		}
	}
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}
