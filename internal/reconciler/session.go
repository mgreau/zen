package reconciler

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/notify"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/tmux"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/mgreau/zen/internal/zenbin"
)

// prevSessionStatus and lastNotifiedAt track session state across scans
// to detect running → waiting transitions and debounce notifications.
var (
	prevSessionStatus sync.Map // SessionID → string status
	lastNotifiedAt    sync.Map // SessionID → time.Time
	interruptChecks   sync.Map // session file path → sessionCheck
)

// sessionCheck caches what the end of a Claude session file says, as of
// its exact modification time and size, so an idle session is read once.
type sessionCheck struct {
	modNanos, size int64
	interrupted    bool // waiting after the user interrupted (Esc)
	toolRunning    bool // a tool call is still running
}

// checkSession reports what the end of the Claude session at path says.
func checkSession(path string) sessionCheck {
	info, err := os.Stat(path)
	if err != nil {
		return sessionCheck{}
	}
	if v, ok := interruptChecks.Load(path); ok {
		if c := v.(sessionCheck); c.modNanos == info.ModTime().UnixNano() && c.size == info.Size() {
			return c
		}
	}
	tail := session.ReadTail(path)
	c := sessionCheck{
		modNanos:    info.ModTime().UnixNano(),
		size:        info.Size(),
		interrupted: tail.Interrupted,
		toolRunning: tail.ToolRunning,
	}
	interruptChecks.Store(path, c)
	return c
}

const sessionNotifyDebounce = 5 * time.Minute

// ScanSessions scans all worktrees for sessions of the configured agent and
// writes a cached snapshot to ~/.zen/state/sessions.json.
//
// A session is classified as:
//   - "stopped"  — process not alive
//   - "running"  — process alive, file recently modified
//   - "waiting"  — process alive, file idle ≥ idleThreshold (needs user input)
//
// Each status is also passed to the tmux window zen opened for the worktree,
// if any (see tmux.SetStatuses). That happens on every scan, not only on a
// change, so a write that failed is retried; tmux.SetStatuses skips windows
// already up to date, and those whose agent hooks report a fresher status
// (see agent.WithTmuxStatus). On those, the scan sets only "stopped", once
// the agent exits, and "waiting" after a turn the user interrupted, which no
// hook reports (see tmux.Report).
func ScanSessions(cfg *config.Config, idleThreshold time.Duration) {
	wts, err := worktree.ListAll(cfg)
	if err != nil {
		fmt.Printf("[%s] Session scan: error listing worktrees: %v\n",
			time.Now().Format(time.RFC3339), err)
		return
	}

	ag := cfg.NewAgent("")
	now := time.Now()
	var states []SessionState
	windowReports := map[string]tmux.Report{} // worktree path → report
	scanned := map[string]bool{}              // session file paths seen

	for _, wt := range wts {
		sessions, _ := ag.FindSessions(wt.Path)
		if len(sessions) == 0 {
			continue
		}

		// Only track the most recent session per worktree
		s := sessions[0]
		filePath := s.Path
		scanned[filePath] = true

		running := ag.IsProcessRunning(s.ID, wt.Path)

		var status string
		switch {
		case !running:
			status = "stopped"
		case now.Sub(time.Unix(s.Modified, 0)) >= idleThreshold:
			status = "waiting"
		default:
			status = "running"
		}

		// Parse model and tokens from the tail of the session file
		model, tokens, _ := ag.ParseTokensTail(filePath)
		shortenedModel := ag.ShortenModel(model)

		// Notify on running → waiting transition (debounced)
		if status == "waiting" {
			if prev, ok := prevSessionStatus.Load(s.ID); ok && prev.(string) == "running" {
				var lastTime time.Time
				if last, ok := lastNotifiedAt.Load(s.ID); ok {
					lastTime = last.(time.Time)
				}
				if time.Since(lastTime) >= sessionNotifyDebounce {
					resumeCmd := sessionResumeCmd(wt)
					if err := notify.SessionWaiting(wt.Name, shortenedModel, resumeCmd); err != nil {
						fmt.Printf("[%s] Session notify error for %s: %v\n",
							time.Now().Format(time.RFC3339), wt.Name, err)
					}
					if SlackReadyHook != nil {
						SlackReadyHook(wt.Path, wt.Name, resumeCmd)
					}
					lastNotifiedAt.Store(s.ID, now)
				}
			}
		}
		prevSessionStatus.Store(s.ID, status)
		claudeWaiting := status == "waiting" && ag.Kind() == agent.Claude
		var check sessionCheck
		if claudeWaiting {
			check = checkSession(filePath)
		}
		windowReports[wt.Path] = tmux.Report{
			Status: status,
			// Checked on every waiting scan, not only the first: after a
			// daemon restart there is no previous status to compare with.
			Interrupted: claudeWaiting && check.interrupted,
			ToolRunning: claudeWaiting && check.toolRunning,
			Idle:        now.Sub(time.Unix(s.Modified, 0)),
		}

		states = append(states, SessionState{
			WorktreePath: wt.Path,
			WorktreeName: wt.Name,
			SessionID:    s.ID,
			Status:       status,
			Model:        shortenedModel,
			Size:         s.SizeStr,
			InputTokens:  session.FormatTokenCount(tokens.InputTokens),
			OutputTokens: session.FormatTokenCount(tokens.OutputTokens),
			LastModified: s.Modified,
			UpdatedAt:    now.Unix(),
		})
	}

	// Forget sessions that are gone, so the cache doesn't grow with every
	// session the daemon has ever seen.
	interruptChecks.Range(func(path, _ any) bool {
		if !scanned[path.(string)] {
			interruptChecks.Delete(path)
		}
		return true
	})

	if err := tmux.SetStatuses(windowReports); err != nil {
		fmt.Printf("[%s] Session scan: error updating tmux windows: %v\n",
			time.Now().Format(time.RFC3339), err)
	}

	if err := WriteSessionSnapshot(states); err != nil {
		fmt.Printf("[%s] Session scan: error writing snapshot: %v\n",
			time.Now().Format(time.RFC3339), err)
	}
}

// sessionResumeCmd returns the zen command to resume a session in a new terminal tab.
func sessionResumeCmd(wt worktree.Worktree) string {
	zenBin, err := zenbin.Path()
	if err != nil {
		zenBin = "zen"
	}
	if wt.Type == worktree.TypePRReview && wt.PRNumber > 0 {
		return fmt.Sprintf("%s review resume %d", zenBin, wt.PRNumber)
	}
	return fmt.Sprintf("%s work resume %s", zenBin, wt.Name)
}
