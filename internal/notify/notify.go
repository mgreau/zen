package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mgreau/zen/internal/zenbin"
)

// Events zen notifies about. A push command can be limited to some of them
// (see Configure), and receives the event in $ZEN_EVENT.
const (
	EventPRReview      = "pr-review"
	EventPRUpdated     = "pr-updated"
	EventWorktreeReady = "worktree-ready"
	EventPRMerged      = "pr-merged"
	EventStale         = "stale-worktrees"
	EventWaiting       = "session-waiting"
	EventDigest        = "digest"
)

// Events lists every event, in a stable order.
func Events() []string {
	return []string{EventPRReview, EventPRUpdated, EventWorktreeReady, EventPRMerged, EventStale, EventWaiting, EventDigest}
}

// pushTimeout bounds a push command, so a hung network call cannot stall the
// daemon loop that sent the notification, and pushWaitDelay bounds the wait
// for the command's output after that. Variables so tests can shorten them.
var (
	pushTimeout   = 30 * time.Second
	pushWaitDelay = 5 * time.Second
)

var (
	pushMu      sync.Mutex
	pushCommand string
	pushEvents  []string
)

// Configure sets a shell command to run for each notification, besides the
// desktop one: for example a curl to a push service, so notifications reach
// you when zen runs on a remote host with no desktop, or no terminal
// attached. events limits it to those events; empty means all of them.
// An empty command disables it.
//
// The command runs through /bin/sh with the notification in the environment,
// never interpolated into the command line: $ZEN_EVENT, $ZEN_TITLE,
// $ZEN_MESSAGE, $ZEN_SUBTITLE, and $ZEN_RESUME, the zen command that opens
// the worktree, when there is one.
func Configure(command string, events []string) {
	pushMu.Lock()
	defer pushMu.Unlock()
	pushCommand = command
	pushEvents = slices.Clone(events)
}

// note is one notification.
type note struct {
	event, title, message, subtitle string
	resume                          string // zen command that opens the worktree, if any
	clickToResume                   bool   // run resume when the desktop notification is clicked
}

// deliver shows n on the desktop and runs the push command, if any.
func deliver(n note) error {
	var desktop error
	if n.clickToResume {
		desktop = SendWithAction(n.title, n.message, n.subtitle, n.resume)
	} else {
		desktop = Send(n.title, n.message, n.subtitle)
	}
	pushed, err := push(n)
	if pushed && err == nil {
		// The notification arrived. A desktop that can't show it, as on a
		// host you ssh into (no notify-send, or no D-Bus session), is what
		// the push command is for: not an error each time.
		desktop = nil
	}
	return errors.Join(desktop, err)
}

// push runs the configured push command for n, and reports whether it ran:
// not when there is none, or when notify.events leaves out n's event.
func push(n note) (bool, error) {
	pushMu.Lock()
	command, events := pushCommand, pushEvents
	pushMu.Unlock()
	if command == "" || (len(events) > 0 && !slices.Contains(events, n.event)) {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	// On timeout only the shell is killed; a child it started (a curl)
	// could hold the output pipe open, and the wait with it, long after.
	cmd.WaitDelay = pushWaitDelay
	cmd.Env = append(os.Environ(),
		"ZEN_EVENT="+n.event,
		"ZEN_TITLE="+n.title,
		"ZEN_MESSAGE="+n.message,
		"ZEN_SUBTITLE="+n.subtitle,
		"ZEN_RESUME="+n.resume,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return true, fmt.Errorf("notify command: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// zenBin returns how to run zen from a notification: a path that survives
// upgrades (see zenbin.Path).
func zenBin() string {
	if bin, err := zenbin.Path(); err == nil {
		return bin
	}
	return "zen"
}

// Send sends a desktop notification.
// On Linux it uses notify-send (libnotify); elsewhere it uses macOS osascript.
func Send(title, message, subtitle string) error {
	if runtime.GOOS == "linux" {
		return exec.Command("notify-send", linuxNotifyArgs(title, message, subtitle)...).Run()
	}
	script := fmt.Sprintf(`display notification %q with title %q`, message, title)
	if subtitle != "" {
		script = fmt.Sprintf(`display notification %q with title %q subtitle %q`, message, title, subtitle)
	}
	return exec.Command("osascript", "-e", script).Run()
}

// linuxNotifyArgs builds the notify-send arguments. notify-send has no
// subtitle concept, so the subtitle goes on a second line of the body.
func linuxNotifyArgs(title, message, subtitle string) []string {
	body := message
	if subtitle != "" {
		body += "\n" + subtitle
	}
	return []string{"--app-name=zen", title, body}
}

// terminalNotifierPath returns the path to terminal-notifier if installed.
func terminalNotifierPath() string {
	path, _ := exec.LookPath("terminal-notifier")
	return path
}

// SendWithAction sends a notification with an optional click action.
// If terminal-notifier is installed (macOS only), clicking the notification
// runs executeOnClick. Otherwise falls back to Send with the command appended
// to the subtitle so it stays visible.
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
	return deliver(note{
		event:    EventPRReview,
		title:    "New PR Review Request",
		message:  fmt.Sprintf("PR #%d: %s", prNumber, prTitle),
		subtitle: fmt.Sprintf("by %s in %s", author, repo),
	})
}

// PRUpdated notifies that an existing review worktree was fast-forwarded to a
// new GitHub head. Quieter than PRReview; sent once per SHA after a successful
// refresh, not every poll.
func PRUpdated(prNumber int, prTitle, repo string) error {
	return deliver(note{
		event:    EventPRUpdated,
		title:    fmt.Sprintf("PR #%d updated", prNumber),
		message:  prTitle,
		subtitle: repo,
	})
}

// Clicking opens a terminal tab in the worktree (requires terminal-notifier).
func WorktreeReady(prNumber int, worktreePath string) error {
	return deliver(note{
		event:         EventWorktreeReady,
		title:         "Worktree Ready — click to review",
		message:       fmt.Sprintf("PR #%d", prNumber),
		resume:        fmt.Sprintf("%s review resume %d", zenBin(), prNumber),
		clickToResume: true,
	})
}

// PRMerged notifies about a PR merge.
func PRMerged(prNumber int, prTitle string) error {
	return deliver(note{
		event:    EventPRMerged,
		title:    "PR Merged",
		message:  fmt.Sprintf("PR #%d: %s", prNumber, prTitle),
		subtitle: "Worktree can be cleaned up",
	})
}

// StaleWorktrees notifies about stale worktrees found.
func StaleWorktrees(count int) error {
	return deliver(note{
		event:    EventStale,
		title:    "Stale Worktrees Found",
		message:  fmt.Sprintf("%d worktrees can be cleaned up", count),
		subtitle: "Run: zen cleanup",
	})
}

// SessionWaiting notifies that a Claude session is waiting for user input.
func SessionWaiting(worktreeName, model, resumeCmd string) error {
	return deliver(note{
		event:    EventWaiting,
		title:    "Claude is waiting",
		message:  fmt.Sprintf("%s needs your input", worktreeName),
		subtitle: model,
		resume:   resumeCmd,
	})
}

// Digest sends a periodic summary notification. Only sends if there is something actionable.
func Digest(waitingSessions, pendingReviews, featureWork int) error {
	if waitingSessions == 0 && pendingReviews == 0 {
		return nil
	}
	var parts []string
	if waitingSessions > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", waitingSessions))
	}
	if pendingReviews > 0 {
		parts = append(parts, fmt.Sprintf("%d PRs to review", pendingReviews))
	}
	subtitle := ""
	if featureWork > 0 {
		subtitle = fmt.Sprintf("%d feature branch(es) active", featureWork)
	}
	return deliver(note{
		event:    EventDigest,
		title:    "zen digest",
		message:  strings.Join(parts, " • "),
		subtitle: subtitle,
	})
}
