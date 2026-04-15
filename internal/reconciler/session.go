package reconciler

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/notify"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/worktree"
)

// prevSessionStatus and lastNotifiedAt track session state across scans
// to detect running → waiting transitions and debounce notifications.
var (
	prevSessionStatus sync.Map // SessionID → string status
	lastNotifiedAt    sync.Map // SessionID → time.Time
)

const sessionNotifyDebounce = 5 * time.Minute

// ScanSessions scans all worktrees for Claude sessions and writes
// a cached snapshot to ~/.zen/state/sessions.json.
//
// A session is classified as:
//   - "stopped"  — process not alive
//   - "running"  — process alive, file recently modified
//   - "waiting"  — process alive, file idle ≥ idleThreshold (needs user input)
func ScanSessions(cfg *config.Config, idleThreshold time.Duration) {
	wts, err := worktree.ListAll(cfg)
	if err != nil {
		fmt.Printf("[%s] Session scan: error listing worktrees: %v\n",
			time.Now().Format(time.RFC3339), err)
		return
	}

	now := time.Now()
	var states []SessionState

	for _, wt := range wts {
		sessions, _ := session.FindSessions(wt.Path)
		if len(sessions) == 0 {
			continue
		}

		// Only track the most recent session per worktree
		s := sessions[0]
		filePath := session.SessionFilePath(wt.Path, s.ID)

		running := session.IsProcessRunning(s.ID)

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
		model, tokens, _ := session.ParseSessionDetailTail(filePath)
		shortenedModel := session.ShortenModel(model)

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
					lastNotifiedAt.Store(s.ID, now)
				}
			}
		}
		prevSessionStatus.Store(s.ID, status)

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
