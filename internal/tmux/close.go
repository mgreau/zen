package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Window is a tmux window zen opened for a worktree.
type Window struct {
	ID        string // window id, such as @3
	SessionID string // id of the session holding the window, such as $0
	PanePIDs  []int  // pid of the process each pane started
	// Outside lists the current directories of panes that are not in the
	// worktree, such as a split the user moved elsewhere.
	Outside []string
}

// paneFormat lists, for each pane, the fields WindowsFor needs. Fields are
// separated by a unit separator, which no path contains.
const paneFormat = "#{window_id}\x1f#{session_id}\x1f#{" + WorktreeOption + "}\x1f#{pane_current_path}\x1f#{pane_pid}"

// Running reports whether zen runs inside tmux.
func Running() bool {
	return os.Getenv("TMUX") != ""
}

// WindowsFor returns the windows of the current tmux server that zen tagged
// with worktreePath. Windows zen opened before it tagged them are not found.
func WindowsFor(worktreePath string) ([]Window, error) {
	if !Running() {
		return nil, ErrNotInTmux
	}
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", paneFormat).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	return matchWindows(string(out), canonical(worktreePath)), nil
}

func matchWindows(out, worktree string) []Window {
	byID := map[string]*Window{}
	var order []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 5 {
			continue
		}
		id, session, tag, cwd := f[0], f[1], f[2], f[3]
		if tag == "" || canonical(tag) != worktree {
			continue
		}
		w, ok := byID[id]
		if !ok {
			w = &Window{ID: id, SessionID: session}
			byID[id] = w
			order = append(order, id)
		}
		// An empty path means tmux could not read it; the agent checks
		// still cover what runs in that pane.
		if cwd != "" && !within(canonical(cwd), worktree) {
			w.Outside = append(w.Outside, cwd)
		}
		if pid, err := strconv.Atoi(f[4]); err == nil {
			w.PanePIDs = append(w.PanePIDs, pid)
		}
	}
	wins := make([]Window, 0, len(order))
	for _, id := range order {
		wins = append(wins, *byID[id])
	}
	return wins
}

// CurrentWindow returns the id of the window holding the pane zen runs in,
// or "" when that is unknown.
func CurrentWindow() string {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || !Running() {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{window_id}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// KillWindow closes a window and the processes in its panes. A window that is
// already gone is not an error.
func KillWindow(id string) error {
	out, err := exec.Command("tmux", "kill-window", "-t", id).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "can't find window") {
			return nil
		}
		return fmt.Errorf("tmux kill-window %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunBackground has the tmux server run argv in the background, appending its
// output to outFile. The job belongs to the server, outside the process tree
// of the caller.
func RunBackground(argv []string, outFile string) error {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	line := strings.Join(quoted, " ") + " >>" + shellQuote(outFile) + " 2>&1"
	// run-shell expands formats in its command, even inside quotes, so '#'
	// must be doubled to reach the shell as itself.
	line = strings.ReplaceAll(line, "#", "##")
	out, err := exec.Command("tmux", "run-shell", "-b", line).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux run-shell: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ClientName returns the tmux client showing the pane zen runs in, or ""
// when that is unknown.
func ClientName() string {
	// Without TMUX_PANE, tmux would guess a client, which may be another
	// terminal attached to the server; no message beats a misplaced one.
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || !Running() {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{client_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ShowMessage shows msg on the status line of client, best effort.
func ShowMessage(client, msg string) {
	if client == "" {
		return
	}
	// Doubling '#' keeps tmux from expanding msg as a format; -l would too,
	// but only from tmux 3.4. -d keeps the message up long enough to read
	// and needs tmux 3.2, so older tmux falls back to the default time.
	msg = strings.ReplaceAll(msg, "#", "##")
	if exec.Command("tmux", "display-message", "-c", client, "-d", "8000", msg).Run() != nil {
		_ = exec.Command("tmux", "display-message", "-c", client, msg).Run()
	}
}

func canonical(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if eval, err := filepath.EvalSymlinks(p); err == nil {
		p = eval
	}
	return filepath.Clean(p)
}

// within reports whether dir is root or below it.
func within(dir, root string) bool {
	if dir == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
