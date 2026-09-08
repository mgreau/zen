package review

import (
	"context"
	"fmt"
	"os"

	"github.com/mgreau/zen/internal/agent"
	wt "github.com/mgreau/zen/internal/worktree"
)

// SyncOutcome is the result of trying to fast-forward an existing PR worktree.
type SyncOutcome int

const (
	// SyncMissing means the worktree directory does not exist.
	SyncMissing SyncOutcome = iota
	// SyncUpToDate means HEAD already matches the fetched PR head (or wantSHA).
	SyncUpToDate
	// SyncUpdated means the worktree was fast-forwarded to a new commit.
	SyncUpdated
	// SyncSkippedDirty means tracked files have local changes; nothing was moved.
	SyncSkippedDirty
	// SyncSkippedAgent means an agent process is running in the worktree.
	SyncSkippedAgent
	// SyncSkippedReset means the worktree cannot fast-forward onto the fetched
	// head (rewritten history, or a head behind the worktree) and reset --hard
	// was not confirmed: the daemon and MCP never reset, and the CLI declined,
	// was given --json, or had no terminal to ask on.
	SyncSkippedReset
)

func (o SyncOutcome) String() string {
	switch o {
	case SyncMissing:
		return "missing"
	case SyncUpToDate:
		return "up-to-date"
	case SyncUpdated:
		return "updated"
	case SyncSkippedDirty:
		return "skipped-dirty"
	case SyncSkippedAgent:
		return "skipped-agent"
	case SyncSkippedReset:
		return "skipped-reset"
	default:
		return fmt.Sprintf("SyncOutcome(%d)", o)
	}
}

// ResetKind says why a clean idle worktree could not be moved onto the
// fetched GitHub head by fast-forward alone.
type ResetKind int

const (
	// ResetDiverged: git refused the fast-forward. HEAD and the fetched head
	// have both moved since they last agreed — a force-push over local commits.
	ResetDiverged ResetKind = iota
	// ResetBehind: the fetched head is an ancestor of HEAD, so `git merge
	// --ff-only` reported "Already up to date" without moving anything. Either
	// GitHub was force-pushed backward, or the worktree has local commits.
	ResetBehind
)

func (k ResetKind) String() string {
	switch k {
	case ResetDiverged:
		return "diverged"
	case ResetBehind:
		return "behind"
	default:
		return fmt.Sprintf("ResetKind(%d)", k)
	}
}

// ResetRequest is passed to ConfirmReset when a clean idle worktree cannot be
// fast-forwarded onto GitHub's head (typically a force-push). Kind says which
// way it failed.
type ResetRequest struct {
	PRNumber      int
	UniqueCommits int // commits on HEAD that are not in origin/pr-N
	Kind          ResetKind
}

// ConfirmReset asks whether to git reset --hard onto the fetched GitHub head.
// Nil means never reset (daemon, MCP, non-interactive --json).
type ConfirmReset func(ResetRequest) bool

// runningIn is swapped in tests.
var runningIn = agent.RunningIn

// SyncExisting fetches pull/N/head into a remote-tracking ref and moves the
// worktree to that commit. A linear update is git merge --ff-only. If the
// author rewrote history, reset --hard runs only when confirmReset returns
// true (CLI prompt). Nil confirmReset never resets — the daemon and MCP skip.
// Dirty worktrees and live agent sessions are left alone.
//
// wantSHA is the GitHub head OID. When it matches worktree HEAD, fetch is skipped.
// An empty wantSHA always fetches.
func SyncExisting(ctx context.Context, originPath, worktreePath string, prNumber int, wantSHA string, log Logger, confirmReset ConfirmReset) (SyncOutcome, error) {
	if log == nil {
		log = noop
	}
	if _, err := os.Stat(worktreePath); err != nil {
		return SyncMissing, nil
	}

	if runningIn(worktreePath) {
		log(fmt.Sprintf("skipping fetch for PR #%d: agent session is running", prNumber))
		return SyncSkippedAgent, nil
	}

	dirty, err := wt.TrackedDirty(worktreePath)
	if err != nil {
		return SyncUpToDate, err
	}
	if dirty {
		log(fmt.Sprintf("skipping fetch for PR #%d: worktree has local changes", prNumber))
		return SyncSkippedDirty, nil
	}

	if wantSHA != "" {
		head, herr := wt.HEAD(worktreePath)
		if herr == nil && wt.SHAEqual(head, wantSHA) {
			return SyncUpToDate, nil
		}
	}

	wt.GitMu.Lock()
	defer wt.GitMu.Unlock()

	// Re-check after lock: another poll may have updated, or the user dirtied it.
	if runningIn(worktreePath) {
		log(fmt.Sprintf("skipping fetch for PR #%d: agent session is running", prNumber))
		return SyncSkippedAgent, nil
	}
	dirty, err = wt.TrackedDirty(worktreePath)
	if err != nil {
		return SyncUpToDate, err
	}
	if dirty {
		log(fmt.Sprintf("skipping fetch for PR #%d: worktree has local changes", prNumber))
		return SyncSkippedDirty, nil
	}

	gitCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	if err := wt.FetchPRHead(gitCtx, originPath, prNumber); err != nil {
		return SyncUpToDate, err
	}

	head, herr := wt.HEAD(worktreePath)
	if herr != nil {
		return SyncUpToDate, herr
	}
	remote, rerr := wt.RevParse(worktreePath, wt.RemotePRRef(prNumber))
	if rerr != nil {
		return SyncUpToDate, rerr
	}
	if wt.SHAEqual(head, remote) {
		return SyncUpToDate, nil
	}

	if runningIn(worktreePath) {
		log(fmt.Sprintf("skipping merge for PR #%d: agent session is running", prNumber))
		return SyncSkippedAgent, nil
	}

	if err := wt.FastForward(gitCtx, worktreePath, prNumber); err != nil {
		if !wt.IsNonFastForward(err) {
			return SyncUpToDate, err
		}
		return maybeReset(gitCtx, worktreePath, prNumber, ResetDiverged, log, confirmReset)
	}

	// `git merge --ff-only <ref>` exits 0 with "Already up to date" when ref is
	// an ancestor of HEAD, leaving the worktree exactly where it was. Without
	// this check a backward force-push reports SyncUpdated on every poll:
	// context is rewritten and an update notification fires forever, while HEAD
	// never reaches the fetched head. Only a HEAD that actually landed on the
	// target is an update; anything else goes through the same reset-or-skip
	// policy as diverged history.
	moved, merr := wt.HEAD(worktreePath)
	if merr != nil {
		return SyncUpToDate, merr
	}
	if !wt.SHAEqual(moved, remote) {
		return maybeReset(gitCtx, worktreePath, prNumber, ResetBehind, log, confirmReset)
	}

	log(fmt.Sprintf("fast-forwarded PR #%d worktree", prNumber))
	return SyncUpdated, nil
}

func maybeReset(ctx context.Context, worktreePath string, prNumber int, kind ResetKind, log Logger, confirmReset ConfirmReset) (SyncOutcome, error) {
	if runningIn(worktreePath) {
		log(fmt.Sprintf("skipping reset for PR #%d: agent session is running", prNumber))
		return SyncSkippedAgent, nil
	}
	dirty, err := wt.TrackedDirty(worktreePath)
	if err != nil {
		return SyncUpToDate, err
	}
	if dirty {
		log(fmt.Sprintf("skipping reset for PR #%d: worktree has local changes", prNumber))
		return SyncSkippedDirty, nil
	}

	unique, _ := wt.UniqueCommitCount(worktreePath, prNumber)
	req := ResetRequest{PRNumber: prNumber, UniqueCommits: unique, Kind: kind}
	if confirmReset == nil || !confirmReset(req) {
		reason := "rewritten GitHub head"
		if kind == ResetBehind {
			reason = "GitHub head is behind this worktree"
		}
		log(fmt.Sprintf("skipping reset for PR #%d: %s (run zen review %d to reset)", prNumber, reason, prNumber))
		return SyncSkippedReset, nil
	}
	if err := wt.ResetToRemotePR(ctx, worktreePath, prNumber); err != nil {
		return SyncUpToDate, err
	}
	log(fmt.Sprintf("reset PR #%d worktree onto the GitHub head", prNumber))
	return SyncUpdated, nil
}
