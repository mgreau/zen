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

// terminalNotifierPath returns the path to terminal-notifier if installed.
func terminalNotifierPath() string {
	path, _ := exec.LookPath("terminal-notifier")
	return path
}

// SendWithAction sends a notification with an optional click action.
// If terminal-notifier is installed, clicking the notification runs executeOnClick.
// Otherwise falls back to osascript with the command appended to the subtitle.
func SendWithAction(title, message, subtitle, executeOnClick string) error {
	tn := terminalNotifierPath()
	if tn != "" && executeOnClick != "" {
		args := []string{"-title", title, "-message", message}
		if subtitle != "" {
			args = append(args, "-subtitle", subtitle)
		}
		args = append(args, "-execute", executeOnClick)
		return exec.Command(tn, args...).Run()
	}
	// Fallback: append resume hint to subtitle so command is visible
	if executeOnClick != "" {
		if subtitle != "" {
			subtitle = subtitle + " | " + executeOnClick
		} else {
			subtitle = executeOnClick
		}
	}
	return Send(title, message, subtitle)
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

// SessionWaiting notifies that a Claude session is waiting for user input.
// resumeCmd is executed on notification click when terminal-notifier is available.
func SessionWaiting(worktreeName, model, resumeCmd string) error {
	return SendWithAction(
		"Claude is waiting",
		fmt.Sprintf("%s needs your input", worktreeName),
		model,
		resumeCmd,
	)
}
