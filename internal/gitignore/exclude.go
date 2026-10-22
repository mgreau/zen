// Package gitignore manages per-clone exclusions through git's
// .git/info/exclude, for paths zen creates inside a repository it may not
// own.
//
// The committed .gitignore is deliberately never touched: excluding a
// zen-owned path there would require a commit to someone else's project.
// info/exclude is per-clone and needs no commit — but it is shared by every
// worktree of the repo (git has no per-worktree exclude file), so only
// zen-owned names belong in it. A name a contributor might legitimately
// create would be hidden from git status everywhere and would outlive
// whatever added it.
package gitignore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsIgnored reports whether git ignores ref in the repository containing
// dir. It asks git rather than reading .gitignore, because a negated
// pattern (`!_worktrees`) or a core.excludesFile entry both change the
// answer in ways that parsing a single file cannot see. ref need not exist
// on disk — check-ignore matches pathnames, not directory entries.
func IsIgnored(dir, ref string) bool {
	cmd := exec.Command("git", "check-ignore", "-q", ref)
	cmd.Dir = dir
	// Exit 0 = ignored, 1 = not ignored, 128 = error. Only 0 is a yes.
	return cmd.Run() == nil
}

// Result reports what EnsureExcluded had to do.
type Result int

const (
	// AlreadyIgnored means git ignored the path before zen touched
	// anything, so no repository was modified. A user-level ignore
	// (~/.config/git/ignore, which git reads with no configuration) puts
	// every repo in this state at once, and is the preferred arrangement
	// for a path that reflects how the user works rather than anything
	// about a particular project.
	AlreadyIgnored Result = iota
	// Written means the entry was added to this clone's info/exclude.
	Written
	// Failed means the path is still not ignored afterwards.
	Failed
)

func (r Result) String() string {
	switch r {
	case AlreadyIgnored:
		return "already-ignored"
	case Written:
		return "written"
	default:
		return "failed"
	}
}

// Ignored reports whether git ignores the path now, however that came about.
func (r Result) Ignored() bool { return r != Failed }

// EnsureExcluded makes git ignore ref within dir's repository, preferring to
// do nothing.
//
// If ref is already ignored — by a user-level ignore, a committed
// .gitignore, or an earlier call — no repository is modified and the result
// is AlreadyIgnored. Only otherwise is an entry appended to this clone's
// info/exclude. Failed is the one outcome a user has to resolve by hand: an
// unwritable exclude file, or a negation pattern overriding the entry.
func EnsureExcluded(dir, ref string) Result {
	if IsIgnored(dir, ref) {
		return AlreadyIgnored
	}
	if err := appendExclude(dir, ref); err != nil {
		return Failed
	}
	if IsIgnored(dir, ref) {
		return Written
	}
	return Failed
}

// appendExclude writes ref as a new line in the repo's info/exclude,
// creating the file if needed and skipping a ref that is already listed.
//
// The exclude path comes from `git rev-parse --git-path`, which resolves to
// the common git directory even when dir is a linked worktree — a worktree
// does not get its own info/exclude.
func appendExclude(dir, ref string) error {
	cmd := exec.Command("git", "rev-parse", "--git-path", "info/exclude")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git rev-parse --git-path info/exclude: %w", err)
	}
	excludePath := strings.TrimSpace(string(out))
	if excludePath == "" {
		return fmt.Errorf("git reported no info/exclude path")
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(dir, excludePath)
	}

	needsNewline := false
	if data, err := os.ReadFile(excludePath); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(l) == ref {
				return nil // already listed; a duplicate line would not help
			}
		}
		needsNewline = len(data) > 0 && data[len(data)-1] != '\n'
	}

	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if needsNewline {
		if _, err := fmt.Fprintln(f); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(f, "%s\n", ref)
	return err
}
