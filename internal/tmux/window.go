// Package tmux opens windows in the tmux session zen runs in. It suits hosts
// reached over ssh, such as a cloud workstation, where there is no terminal
// application to drive.
//
// Each window zen opens carries the window option @zen-worktree, set to the
// worktree path, so zen can find the window again: opening a worktree that
// already has a window reuses it instead of stacking a duplicate.
package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// ErrNotInTmux is returned when zen is not running inside a tmux session.
var ErrNotInTmux = errors.New("not running inside tmux: start zen from a tmux session, or pass --no-terminal")

// WorktreeOption is the tmux window option holding the worktree path of a
// window zen opened.
const WorktreeOption = "@zen-worktree"

// Outcome says what OpenWindow did.
type Outcome int

const (
	// NewWindow means a new window was opened and runs the command.
	NewWindow Outcome = iota
	// Reused means the worktree's window was at its shell prompt and now
	// runs the command.
	Reused
	// AlreadyRunning means something still runs in the worktree's window,
	// so the command was not run. OpenWindow switched to the window;
	// OpenWindowDetached left it alone.
	AlreadyRunning
)

// OpenWindow opens command for workDir in a tmux window and switches to it.
//
// A new window is named after the base name of workDir and runs command
// through the user's login shell. When command exits, the window keeps an
// interactive shell in workDir, so a finished or failed agent leaves a prompt
// instead of closing the window.
//
// If a window zen opened for workDir already exists, it is reused: when it
// sits at its shell prompt, command is respawned in it; when something else,
// such as the agent, is still running there, zen only switches to it and
// command is not run.
func OpenWindow(workDir, command string) (Outcome, error) {
	return open(workDir, command, false)
}

// OpenWindowDetached is OpenWindow without switching to the window. It is for
// windows opened in the background, such as by the daemon, so they do not
// take focus from the window the user is typing in.
func OpenWindowDetached(workDir, command string) (Outcome, error) {
	return open(workDir, command, true)
}

func open(workDir, command string, detached bool) (Outcome, error) {
	if os.Getenv("TMUX") == "" {
		return 0, ErrNotInTmux
	}
	workDir = filepath.Clean(workDir)
	shell := userShell()

	windows, err := listWindows()
	if err != nil {
		return 0, err
	}
	if w, ok := findWindow(windows, workDir); ok {
		allowPassthrough(w.ID) // for windows opened before zen set it
		if !w.idle() {
			if !detached {
				if err := run("switch-client", "-t", w.ID); err != nil {
					return 0, err
				}
			}
			return AlreadyRunning, nil
		}
		// The status belonged to the agent that exited. Clear it, and the
		// hooks flag, before the new agent starts, so its hooks or the
		// daemon set it afresh and nothing wipes what they set. Best effort:
		// a stale status is not worth failing for.
		_ = run("set-option", "-w", "-u", "-t", w.ID, StatusOption)
		_ = run("set-option", "-w", "-u", "-t", w.ID, HooksOption)
		if err := run(respawnArgs(w.ID, workDir, command, shell)...); err != nil {
			return 0, err
		}
		if !detached {
			// The agent is running by now, so failing to show it is not a
			// failure to open it.
			_ = run("switch-client", "-t", w.ID)
		}
		return Reused, nil
	}

	// Only stdout carries the window ID: a warning on stderr must not end up
	// in the -t target below.
	var stderr strings.Builder
	cmd := exec.Command("tmux", windowArgs(workDir, command, shell, detached)...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("tmux new-window: %w: %s", err, stderr.String())
	}
	id := strings.TrimSpace(string(out))
	if err := run("set-option", "-w", "-t", id, WorktreeOption, workDir); err != nil {
		// An untagged window cannot be found again, so the next open would
		// start a second agent in the same worktree. Close it instead: the
		// agent in it has only just started.
		_ = run("kill-window", "-t", id)
		return 0, err
	}
	allowPassthrough(id)
	return NewWindow, nil
}

// allowPassthrough lets the panes of window id send escape sequences
// straight to the terminal tmux runs in, so an agent's desktop notifications
// (OSC 9, 99 or 777, which Claude Code sends) reach the computer you ssh from.
// "all" passes them from windows you are not looking at too, which is the
// point with many agents; tmux 3.3 knows only "on", visible panes only, and
// older tmux has no passthrough at all. Best effort: a missing notification
// is not worth failing to open the window for.
//
// Passthrough gives programs in these windows the same access to your
// terminal they would have without tmux.
func allowPassthrough(id string) {
	if run("set-option", "-w", "-t", id, "allow-passthrough", "all") != nil {
		_ = run("set-option", "-w", "-t", id, "allow-passthrough", "on")
	}
}

func userShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// windowArgs builds the tmux new-window arguments. tmux runs the shell and its
// arguments directly, so command reaches the shell as one argument and tmux
// never parses it. A login shell loads the same profile as an ssh session.
// tmux prints the new window's ID so zen can tag it.
func windowArgs(workDir, command, shell string, detached bool) []string {
	args := []string{"new-window"}
	if detached {
		args = append(args, "-d")
	}
	args = append(args, "-P", "-F", "#{window_id}", "-n", filepath.Base(workDir), "-c", workDir)
	return append(args, shellArgs(command, shell)...)
}

// respawnArgs builds the arguments that replace the idle shell in window id
// with command, run the same way as in a new window.
func respawnArgs(id, workDir, command, shell string) []string {
	args := []string{"respawn-window", "-k", "-t", id, "-c", workDir}
	return append(args, shellArgs(command, shell)...)
}

func shellArgs(command, shell string) []string {
	return []string{"--", shell, "-l", "-c", command + `; exec "${SHELL:-/bin/sh}"`}
}

// window is a tmux window as reported by list-windows.
type window struct {
	ID       string // window ID, such as @3
	Worktree string // @zen-worktree, empty for windows zen did not open
	Command  string // foreground command in the active pane
	PID      string // PID of the process the active pane started
	Panes    string // number of panes
	Status   string // @zen-status
	Hooks    bool   // @zen-hooks: the agent's hooks set Status
}

// idle reports whether the window sits at the user's shell prompt, so a new
// command can replace it without killing anything the user cares about.
//
// A window split into several panes is never idle: respawn-window would kill
// all of them, and the agent may run in one that is not active.
//
// The foreground command alone cannot tell: the shell zen starts runs the
// agent with "-c" and, not being interactive, does not give it a process
// group of its own, so tmux reports the shell for as long as the agent runs.
// Only once the agent exits does that shell exec an interactive one, whose
// command line no longer carries "-c". An interactive shell running a job,
// such as an editor, shows that job as the foreground command.
//
// The shell is the one in the pane's command line, not zen's own $SHELL: the
// interactive shell comes from the pane's environment, which may name another.
func (w window) idle() bool {
	if w.Panes != "1" {
		return false
	}
	args, err := processArgs(w.PID)
	if err != nil {
		return false // cannot tell: treat as busy, the safe side
	}
	fields := strings.Fields(args)
	if len(fields) == 0 || slices.Contains(fields, "-c") {
		return false
	}
	// A login shell's name may start with "-".
	shell := strings.TrimPrefix(filepath.Base(fields[0]), "-")
	return strings.TrimPrefix(w.Command, "-") == shell
}

// processArgs returns the command line of process pid. A variable so tests
// can stub it.
var processArgs = func(pid string) (string, error) {
	out, err := exec.Command("ps", "-o", "args=", "-p", pid).Output()
	return string(out), err
}

// listFormat is the list-windows format parseWindows reads. Fields are tab
// separated: tmux never puts a tab in a window ID or a command name, and a
// worktree path with a tab in it is not worth supporting.
const listFormat = "#{window_id}\t#{" + WorktreeOption + "}\t#{pane_current_command}\t#{pane_pid}\t#{window_panes}" +
	"\t#{" + StatusOption + "}\t#{" + HooksOption + "}"

// listWindows lists every window on the tmux server, across sessions.
func listWindows() ([]window, error) {
	out, err := exec.Command("tmux", "list-windows", "-a", "-F", listFormat).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux list-windows: %w: %s", err, out)
	}
	return parseWindows(string(out)), nil
}

func parseWindows(out string) []window {
	var windows []window
	for line := range strings.Lines(out) {
		f := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(f) != 7 {
			continue
		}
		windows = append(windows, window{
			ID: f[0], Worktree: f[1], Command: f[2], PID: f[3], Panes: f[4],
			Status: f[5], Hooks: f[6] == "1",
		})
	}
	return windows
}

// findWindow returns the first window zen opened for workDir.
func findWindow(windows []window, workDir string) (window, bool) {
	for _, w := range windows {
		if w.Worktree != "" && filepath.Clean(w.Worktree) == workDir {
			return w, true
		}
	}
	return window{}, false
}

func run(args ...string) error {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %s: %w: %s", args[0], err, out)
	}
	return nil
}
