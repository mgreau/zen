package reconciler

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/notify"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/tmux"
	"github.com/mgreau/zen/internal/worktree"
)

// prevSessionStatus and lastNotifiedAt track session state across scans
// to detect running → waiting transitions and debounce notifications.
var (
	prevSessionStatus sync.Map // SessionID → string status
	lastNotifiedAt    sync.Map // SessionID → time.Time
)

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
// (see agent.WithTmuxStatus), except on the running → waiting transition that
// also fires the desktop notification.
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

	for _, wt := range wts {
		sessions, _ := ag.FindSessions(wt.Path)
		if len(sessions) == 0 {
			continue
		}

		// Only track the most recent session per worktree
		s := sessions[0]
		filePath := s.Path

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
		becameWaiting := false
		if status == "waiting" {
			if prev, ok := prevSessionStatus.Load(s.ID); ok && prev.(string) == "running" {
				becameWaiting = true
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
		windowReports[wt.Path] = tmux.Report{Status: status, BecameWaiting: becameWaiting}

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
	zenBin, err := os.Executable()
	if err != nil {
		zenBin = "zen"
	}
	if wt.Type == worktree.TypePRReview && wt.PRNumber > 0 {
		return fmt.Sprintf("%s review resume %d", zenBin, wt.PRNumber)
	}
	return fmt.Sprintf("%s work resume %s", zenBin, wt.Name)
}
