package notify

import (
	"fmt"
	"os/exec"
)

// Send sends a macOS notification using osascript.
func Send(title, message, subtitle string) error {
	script := fmt.Sprintf(`display notification %q with title %q`, message, title)
	if subtitle != "" {
		script = fmt.Sprintf(`display notification %q with title %q subtitle %q`, message, title, subtitle)
	}
	return exec.Command("osascript", "-e", script).Run()
}


// PRReview notifies about a new PR review request.
func PRReview(prNumber int, prTitle, author, repo string) error {
	return Send(
		"New PR Review Request",
		fmt.Sprintf("PR #%d: %s", prNumber, prTitle),
		fmt.Sprintf("by %s in %s", author, repo),
	)
}

// WorktreeReady notifies that a worktree is ready.
func WorktreeReady(prNumber int, worktreePath string) error {
	return Send(
		"Worktree Ready",
		fmt.Sprintf("PR #%d worktree created", prNumber),
		worktreePath,
	)
}

// PRMerged notifies about a PR merge.
func PRMerged(prNumber int, prTitle string) error {
	return Send(
		"PR Merged",
		fmt.Sprintf("PR #%d: %s", prNumber, prTitle),
		"Worktree can be cleaned up",
	)
}

// StaleWorktrees notifies about stale worktrees found.
func StaleWorktrees(count int) error {
	return Send(
		"Stale Worktrees Found",
		fmt.Sprintf("%d worktrees can be cleaned up", count),
		"Run: zen cleanup",
	)
}
