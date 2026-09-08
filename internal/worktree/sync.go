package worktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// RemotePRRef is the remote-tracking ref used to store pull/N/head.
// Fetching into the local pr-N branch is refused when that branch is checked
// out in a worktree; this ref is never checked out.
func RemotePRRef(prNumber int) string {
	return fmt.Sprintf("refs/remotes/origin/pr-%d", prNumber)
}

// SHAEqual reports whether two git object names refer to the same commit.
// Empty strings never match. Short prefixes of at least 7 hex chars match a
// longer SHA.
func SHAEqual(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 7 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// HEAD returns the full SHA of HEAD in the given working tree.
func HEAD(worktreePath string) (string, error) {
	return RevParse(worktreePath, "HEAD")
}

// RevParse resolves a revision in worktreePath.
func RevParse(worktreePath, rev string) (string, error) {
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s: %w", rev, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// TrackedDirty reports whether worktreePath has staged or unstaged changes to
// tracked files. Untracked files (CLAUDE.local.md, AGENTS.md, .zen/) are ignored
// so injected context does not block a fast-forward.
func TrackedDirty(worktreePath string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain", "-uno")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// FetchRefspec runs `git fetch origin <refspec>` in originPath.
func FetchRefspec(ctx context.Context, originPath, refspec string) error {
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin", refspec)
	cmd.Dir = originPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("git fetch timed out: %w", ctx.Err())
		}
		return fmt.Errorf("git fetch origin %s: %w: %s", refspec, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// FetchPRHead fetches GitHub's pull/N/head into refs/remotes/origin/pr-N.
func FetchPRHead(ctx context.Context, originPath string, prNumber int) error {
	refspec := fmt.Sprintf("+refs/pull/%d/head:%s", prNumber, RemotePRRef(prNumber))
	return FetchRefspec(ctx, originPath, refspec)
}

// ErrNonFastForward is returned when a worktree cannot be fast-forwarded
// (diverged history, typically a force-push). Callers should ResetToRemotePR
// when the worktree is clean and idle, not skip forever.
type ErrNonFastForward struct {
	Detail string
}

func (e *ErrNonFastForward) Error() string {
	return "cannot fast-forward: " + e.Detail
}

// IsNonFastForward reports whether err is a rejected fast-forward.
func IsNonFastForward(err error) bool {
	if err == nil {
		return false
	}
	var nff *ErrNonFastForward
	if errors.As(err, &nff) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not possible to fast-forward") ||
		strings.Contains(s, "refusing to merge unrelated")
}

// IsMissingPRRef reports whether git fetch failed because pull/N/head is gone
// (PR deleted, or GitHub no longer advertises the ref). Callers should skip,
// not retry as a git flake.
func IsMissingPRRef(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "couldn't find remote ref") ||
		(strings.Contains(s, "unable to find") && strings.Contains(s, "ref")) ||
		(strings.Contains(s, "does not exist") && strings.Contains(s, "pull"))
}

// FastForward fast-forwards the worktree onto refs/remotes/origin/pr-N.
// A non-ff merge returns *ErrNonFastForward. Callers that want the worktree
// to match GitHub anyway should ResetToRemotePR when the tree is clean.
func FastForward(ctx context.Context, worktreePath string, prNumber int) error {
	ref := RemotePRRef(prNumber)
	cmd := exec.CommandContext(ctx, "git", "merge", "--ff-only", ref)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		low := strings.ToLower(detail + " " + err.Error())
		if strings.Contains(low, "not possible to fast-forward") ||
			strings.Contains(low, "refusing to merge unrelated") ||
			strings.Contains(low, "diverging") {
			return &ErrNonFastForward{Detail: detail}
		}
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("git merge timed out: %w", ctx.Err())
		}
		return fmt.Errorf("git merge --ff-only %s: %w: %s", ref, err, detail)
	}
	return nil
}

// UniqueCommitCount is how many commits are on HEAD that are not in
// refs/remotes/origin/pr-N. Used to warn before reset --hard: a linear GitHub
// update with no local commits is 0; a local review commit or a rewritten
// remote is typically ≥1.
func UniqueCommitCount(worktreePath string, prNumber int) (int, error) {
	ref := RemotePRRef(prNumber)
	cmd := exec.Command("git", "rev-list", "--count", ref+"..HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s..HEAD: %w", ref, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count: %w", err)
	}
	return n, nil
}

// ResetToRemotePR points the worktree at refs/remotes/origin/pr-N with
// reset --hard. Used after a non-fast-forward fetch (force-push) when the
// tree has no tracked local changes and the user confirmed. Untracked files
// (CLAUDE.local.md, .zen/) are left in place. The previous tip remains in the
// reflog as pr-N@{1}.
func ResetToRemotePR(ctx context.Context, worktreePath string, prNumber int) error {
	ref := RemotePRRef(prNumber)
	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", ref)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("git reset timed out: %w", ctx.Err())
		}
		return fmt.Errorf("git reset --hard %s: %w: %s", ref, err, detail)
	}
	return nil
}
