package tmux

import (
	"errors"
	"os/exec"
	"path/filepath"
)

// StatusOption is the tmux window option holding the state of the agent in a
// window zen opened: "running", "waiting" (it needs the user), or "stopped".
// A tmux format such as window-status-format can read it to highlight the
// windows that need attention.
const StatusOption = "@zen-status"

// HooksOption is the tmux window option an agent's hooks set to "1" when they
// set StatusOption. Hooks see a change of state as it happens, while the
// daemon sees it a scan later, so SetStatuses mostly leaves their "running"
// and "waiting" alone.
const HooksOption = "@zen-hooks"

// Report is the daemon's view of the agent in one worktree.
type Report struct {
	Status string // "running", "waiting", or "stopped"

	// BecameWaiting is set on the scan where the daemon sees the agent go
	// from running to waiting: its session has been idle past the threshold.
	// That is the event the daemon's desktop notification fires on, the one
	// signal every terminal gets. It overrides the hooks' "running", since
	// no hook reports a turn the user interrupted.
	BecameWaiting bool
}

// SetStatuses brings StatusOption up to date on the window zen opened for
// each worktree path in reports. Worktrees without a window are ignored, and
// windows already showing the status are not written to, so the daemon can
// call it on every scan and a write that failed is retried by the next one.
//
// On a window whose status the agent's hooks set (HooksOption), the daemon
// writes only "stopped", which the hooks cannot report once the agent exits,
// and the "waiting" of Report.BecameWaiting. Writing "stopped" also clears
// HooksOption, since whatever runs in the window next may have no hooks.
//
// "stopped" is written only to a window back at its shell prompt. The daemon
// tracks one agent kind, so it may miss an agent of another kind that still
// runs there.
//
// It does not need zen to run inside tmux: like any tmux client, it talks to
// the server named by $TMUX, or the default server when $TMUX is unset. When
// tmux is not installed or no server is running there is nothing to update,
// and SetStatuses returns nil.
func SetStatuses(reports map[string]Report) error {
	if len(reports) == 0 {
		return nil
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	windows, err := listWindows()
	if err != nil {
		return nil // no server: no windows to update
	}
	var errs []error
	for _, w := range statusUpdates(windows, reports) {
		errs = append(errs, run("set-option", "-w", "-t", w.ID, StatusOption, w.Status))
		if w.Status == "stopped" {
			errs = append(errs, run("set-option", "-w", "-u", "-t", w.ID, HooksOption))
		}
	}
	return errors.Join(errs...)
}

// statusUpdates returns the windows whose status must change, each carrying
// its new status.
func statusUpdates(windows []window, reports map[string]Report) []window {
	clean := make(map[string]Report, len(reports))
	for path, r := range reports {
		clean[filepath.Clean(path)] = r
	}
	var updates []window
	for _, w := range windows {
		if w.Worktree == "" {
			continue
		}
		r, ok := clean[filepath.Clean(w.Worktree)]
		if !ok || r.Status == w.Status {
			continue
		}
		hooksOwn := w.Hooks && (w.Status == "running" || w.Status == "waiting")
		switch {
		case r.Status == "stopped":
			if !w.idle() {
				continue // something still runs there
			}
		case hooksOwn && !r.BecameWaiting:
			continue // the hooks know better
		}
		w.Status = r.Status
		updates = append(updates, w)
	}
	return updates
}
