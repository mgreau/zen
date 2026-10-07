package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mgreau/zen/internal/agent"
)

var (
	// ErrWorktreeDirty means removal would discard local file changes.
	ErrWorktreeDirty = errors.New("refusing to remove worktree with local changes")
	// ErrWorktreeActive means a supported coding agent is running in the worktree.
	ErrWorktreeActive = errors.New("refusing to remove worktree with a running agent")
	// ErrWorktreeLocalCommits means a review worktree's HEAD has commits the
	// PR head never had: work that exists only in this checkout. It is also
	// returned when that cannot be ruled out, so review cleanup fails closed.
	ErrWorktreeLocalCommits = errors.New("refusing to remove worktree that may hold local commits")
	// ErrNotPRReview means a worktree named after a PR is not checked out as
	// that PR's review (see Classify), so review cleanup must leave it alone.
	ErrNotPRReview = errors.New("refusing to remove worktree that is not a PR review checkout")
)

// commitName matches the hex object names GitHub reports for commits.
var commitName = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// RemovalBlocked reports whether err is an expected safety refusal rather than
// a Git or filesystem failure.
func RemovalBlocked(err error) bool {
	return errors.Is(err, ErrWorktreeDirty) ||
		errors.Is(err, ErrWorktreeActive) ||
		errors.Is(err, ErrWorktreeLocalCommits) ||
		errors.Is(err, ErrNotPRReview)
}

// Remove removes a linked worktree only when it is inactive and contains no
// local changes. Zen-generated review context is reproducible and does not make
// a worktree dirty. A missing worktree is already removed and succeeds.
func Remove(originPath, worktreePath string) error {
	return remove(originPath, worktreePath, agent.RunningIn, nil)
}

// RemoveReview removes the review worktree of a merged PR. On top of the
// checks Remove makes, it refuses unless the worktree is still checked out as
// review prNumber (ErrNotPRReview) and HEAD is prHead, GitHub's last head for
// the PR, or one of its ancestors (ErrWorktreeLocalCommits). Review worktrees
// are never pushed to the author's branch, so any commit beyond prHead was made
// here and exists nowhere else. When prHead is not in the local object store,
// pull/<prNumber>/head is fetched first; if the comparison still cannot be
// made, removal is refused.
func RemoveReview(ctx context.Context, originPath, worktreePath string, prNumber int, prHead string) error {
	return removeReview(ctx, originPath, worktreePath, prNumber, prHead, agent.RunningIn)
}

func removeReview(ctx context.Context, originPath, worktreePath string, prNumber int, prHead string, runningIn func(string) bool) error {
	return remove(originPath, worktreePath, runningIn, func() error {
		return verifyReviewHEAD(ctx, originPath, worktreePath, prNumber, prHead)
	})
}

// remove runs verify, when set, after the agent and local-change checks and
// before anything is deleted, while GitMu is held.
func remove(originPath, worktreePath string, runningIn func(string) bool, verify func() error) error {
	GitMu.Lock()
	defer GitMu.Unlock()

	if _, err := os.Stat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect worktree: %w", err)
	}
	if runningIn(worktreePath) {
		return ErrWorktreeActive
	}

	owned := make(map[string]struct{})
	for _, path := range agent.OwnedContextFiles(worktreePath) {
		owned[filepath.ToSlash(path)] = struct{}{}
	}

	dirty, err := removalDirty(worktreePath, owned)
	if err != nil {
		return err
	}
	if dirty {
		return ErrWorktreeDirty
	}

	if verify != nil {
		if err := verify(); err != nil {
			return err
		}
	}

	snapshots, err := removeOwnedContext(worktreePath, owned)
	if err != nil {
		return err
	}

	cmd := exec.Command("git", "worktree", "remove", worktreePath)
	cmd.Dir = originPath
	if out, err := cmd.CombinedOutput(); err != nil {
		if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
			if restoreErr := restoreOwnedContext(snapshots); restoreErr != nil {
				return fmt.Errorf("git worktree remove: %w: %s; restore generated context: %v", err, strings.TrimSpace(string(out)), restoreErr)
			}
		}
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removalDirty(worktreePath string, owned map[string]struct{}) (bool, error) {
	dirty, err := TrackedDirty(worktreePath)
	if err != nil || dirty {
		return dirty, err
	}

	for _, args := range [][]string{
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "-z"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = worktreePath
		out, err := cmd.Output()
		if err != nil {
			return false, fmt.Errorf("inspect untracked files: %w", err)
		}
		for _, raw := range bytes.Split(out, []byte{0}) {
			path := string(raw)
			if path == "" {
				continue
			}
			if _, ok := owned[filepath.ToSlash(path)]; !ok {
				return true, nil
			}
		}
	}
	return false, nil
}

type contextSnapshot struct {
	path string
	data []byte
	mode fs.FileMode
}

func removeOwnedContext(worktreePath string, owned map[string]struct{}) ([]contextSnapshot, error) {
	var snapshots []contextSnapshot
	for path := range owned {
		tracked, err := trackedPath(worktreePath, path)
		if err != nil {
			return nil, err
		}
		if tracked {
			continue
		}
		fullPath := filepath.Join(worktreePath, filepath.FromSlash(path))
		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect generated context %s: %w", path, err)
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("read generated context %s: %w", path, err)
		}
		snapshots = append(snapshots, contextSnapshot{path: fullPath, data: data, mode: info.Mode()})
	}
	for i, snapshot := range snapshots {
		if err := os.Remove(snapshot.path); err != nil {
			removeErr := fmt.Errorf("remove generated context %s: %w", filepath.Base(snapshot.path), err)
			if restoreErr := restoreOwnedContext(snapshots[:i]); restoreErr != nil {
				return nil, fmt.Errorf("%w; restore generated context: %v", removeErr, restoreErr)
			}
			return nil, removeErr
		}
	}
	// Remove the generated directory only when no user-owned files remain.
	_ = os.Remove(filepath.Join(worktreePath, ".zen"))
	return snapshots, nil
}

func restoreOwnedContext(snapshots []contextSnapshot) error {
	for _, snapshot := range snapshots {
		if err := os.MkdirAll(filepath.Dir(snapshot.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(snapshot.path, snapshot.data, snapshot.mode.Perm()); err != nil {
			return err
		}
	}
	return nil
}

func trackedPath(worktreePath, path string) (bool, error) {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", "--", path)
	cmd.Dir = worktreePath
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("inspect context path %s: %w", path, err)
	}
	return true, nil
}

// verifyReviewHEAD checks that worktreePath is still checked out as review
// prNumber and that HEAD holds no commits beyond prHead.
func verifyReviewHEAD(ctx context.Context, originPath, worktreePath string, prNumber int, prHead string) error {
	branch, detached, err := checkedOutBranch(worktreePath)
	if err != nil {
		return err
	}
	if kind, pr := Classify(filepath.Base(worktreePath), branch, detached); kind != TypePRReview || pr != prNumber {
		return fmt.Errorf("%w (branch %q)", ErrNotPRReview, branch)
	}

	if !commitName.MatchString(prHead) {
		return fmt.Errorf("%w: no usable PR head commit from GitHub (%q)", ErrWorktreeLocalCommits, prHead)
	}
	head, err := HEAD(worktreePath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWorktreeLocalCommits, err)
	}
	if SHAEqual(head, prHead) {
		return nil
	}

	// A PR that moved after its worktree was last refreshed, or that was
	// squash-merged, leaves the final head out of the local object store.
	if !hasCommit(worktreePath, prHead) {
		if err := FetchPRHead(ctx, originPath, prNumber); err != nil {
			return fmt.Errorf("%w: PR head %s is not local and fetching it failed: %v", ErrWorktreeLocalCommits, abbrev(prHead), err)
		}
		if !hasCommit(worktreePath, prHead) {
			return fmt.Errorf("%w: PR head %s not found after fetching pull/%d/head", ErrWorktreeLocalCommits, abbrev(prHead), prNumber)
		}
	}

	contained, err := isAncestor(worktreePath, head, prHead)
	if err != nil {
		return fmt.Errorf("%w: compare HEAD with PR head %s: %v", ErrWorktreeLocalCommits, abbrev(prHead), err)
	}
	if contained {
		return nil
	}
	if n, err := countCommits(worktreePath, prHead+"..HEAD"); err == nil {
		return fmt.Errorf("%w: HEAD has %d commit(s) not in PR head %s", ErrWorktreeLocalCommits, n, abbrev(prHead))
	}
	return fmt.Errorf("%w: HEAD is not in PR head %s", ErrWorktreeLocalCommits, abbrev(prHead))
}

// checkedOutBranch returns the branch checked out in worktreePath, or
// detached=true when HEAD is detached.
func checkedOutBranch(worktreePath string) (branch string, detached bool, err error) {
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", true, nil
		}
		return "", false, fmt.Errorf("inspect checked-out branch: %w", err)
	}
	return strings.TrimSpace(string(out)), false, nil
}

// hasCommit reports whether sha names a commit in the local object store.
func hasCommit(worktreePath, sha string) bool {
	cmd := exec.Command("git", "cat-file", "-e", sha+"^{commit}")
	cmd.Dir = worktreePath
	return cmd.Run() == nil
}

// isAncestor reports whether commit is ancestor or equal to descendant.
func isAncestor(worktreePath, commit, descendant string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", commit, descendant)
	cmd.Dir = worktreePath
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
	}
	return true, nil
}

func countCommits(worktreePath, revRange string) (int, error) {
	cmd := exec.Command("git", "rev-list", "--count", revRange)
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func abbrev(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
