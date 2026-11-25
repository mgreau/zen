// Package agentdeck opens zen sessions in agent-deck
// (https://github.com/asheshgoplani/agent-deck), a tmux-based session manager
// for AI coding agents, instead of in a terminal emulator tab.
//
// zen hands over the same shell command it would type into a tab. agent-deck
// recognises a leading claude or codex binary, registers the session as that
// tool (so its status, conversation ID and send/output commands work), and
// forwards the remaining arguments through a wrapper. Any other command, such
// as an agent stack launcher with an env prefix, runs as a shell session.
package agentdeck

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Group is the agent-deck group zen's sessions are filed under. Without it
// agent-deck groups by the worktree's parent directory, which for zen is a
// shared directory such as ~/src or _worktrees.
const Group = "zen"

// Bin is the agent-deck executable. A variable so tests can point it at a
// fake.
var Bin = "agent-deck"

// run executes agent-deck and returns its stdout. agent-deck exits non-zero
// on failure but still prints its JSON result, so callers parse stdout before
// looking at the error.
var run = func(args ...string) ([]byte, error) {
	cmd := exec.Command(Bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("agent-deck not found on PATH: install it from https://github.com/asheshgoplani/agent-deck or pick another terminal")
		}
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

// result is the subset of agent-deck's --json output zen reads. Success is a
// pointer because session show omits it when the session exists and prints
// "success": false (with a code) when it does not.
type result struct {
	Success    *bool  `json:"success"`
	ID         string `json:"id"`
	Code       string `json:"code"`
	Error      string `json:"error"`
	ExistingID string `json:"existing_id"`
	Status     string `json:"status"`
	Substate   string `json:"substate"`
}

// ok reports whether agent-deck said the command succeeded.
func (r result) ok() bool { return r.Success != nil && *r.Success }

// failed reports whether agent-deck said the command failed.
func (r result) failed() bool { return r.Success != nil && !*r.Success }

// notFound is the code agent-deck reports for an unknown session.
const notFound = "NOT_FOUND"

// exited reports whether a session's process is gone, so its registry entry
// can be removed without killing an agent. agent-deck reports a claude or
// codex session that exited as "error", and one it stopped as "stopped". A
// shell session (an env-prefixed command, such as an agent stack launch)
// whose command exited 0 stays "idle", the same status a running shell
// reports; only its substate, "process-exited", tells the two apart.
func (r result) exited() bool {
	return r.Status == "stopped" || r.Status == "error" || r.Substate == "process-exited"
}

func call(args ...string) (result, error) {
	out, runErr := run(args...)
	var r result
	if len(out) > 0 {
		if err := json.Unmarshal(out, &r); err != nil {
			return r, fmt.Errorf("agent-deck %s: unreadable output %q: %w", args[0], strings.TrimSpace(string(out)), err)
		}
		return r, nil
	}
	if runErr != nil {
		return r, fmt.Errorf("agent-deck %s: %w", args[0], runErr)
	}
	return r, fmt.Errorf("agent-deck %s: no output", args[0])
}

// show returns a session's live state. found is false when agent-deck no
// longer knows the session.
func show(id string) (r result, found bool, err error) {
	r, err = call("session", "show", "-json", id)
	if err != nil {
		return r, false, err
	}
	if r.failed() {
		if r.Code == notFound {
			return r, false, nil
		}
		return r, false, fmt.Errorf("agent-deck session show %s: %s", id, r.Error)
	}
	return r, true, nil
}

// remove deletes a session's registry entry. Callers check liveness first:
// remove's own guard reads a stored state that can stay "starting" after the
// agent has exited, so the removal is forced. Removal is registry-only:
// transcripts and the worktree are untouched. A session that is already gone
// counts as removed.
func remove(id string) error {
	rm, err := call("session", "remove", "-force", "-json", id)
	if err != nil {
		return err
	}
	if !rm.ok() && rm.Code != notFound {
		return fmt.Errorf("agent-deck session remove %s: %s", id, rm.Error)
	}
	return nil
}

// OpenTab creates and starts an agent-deck session for workDir running
// command. The session is titled after the worktree directory and is not
// linked as a child of whatever agent-deck session zen happens to run in.
//
// agent-deck keeps one session per title and path. When the worktree already
// has one, OpenTab replaces it only if its process has exited (see exited); a
// live session is left alone and reported, so resuming never starts a second
// agent in the same worktree. A session that vanishes between the launch and
// the check is treated as absent.
func OpenTab(workDir, command string) error {
	title := filepath.Base(workDir)
	launch := []string{"launch", "-title", title, "-group", Group, "-no-parent", "-cmd", command, "-json", workDir}

	r, err := call(launch...)
	if err != nil {
		return err
	}
	if r.ok() {
		return nil
	}
	if r.Code != "ALREADY_EXISTS" || r.ExistingID == "" {
		return fmt.Errorf("agent-deck launch: %s", r.Error)
	}

	// The status comes from session show, which reads the live tmux state.
	existing, found, err := show(r.ExistingID)
	if err != nil {
		return err
	}
	if found {
		if !existing.exited() {
			return fmt.Errorf("agent-deck already has a live session %q for %s; attach with `agent-deck session attach %s`", title, workDir, r.ExistingID)
		}
		if err := remove(r.ExistingID); err != nil {
			return err
		}
	}

	r, err = call(launch...)
	if err != nil {
		return err
	}
	if !r.ok() {
		return fmt.Errorf("agent-deck launch: %s", r.Error)
	}
	return nil
}

// entry is the subset of an agent-deck list --json row zen reads.
type entry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
	Group string `json:"group"`
}

// Forget removes the agent-deck session zen opened for workDir, for use once
// the worktree itself is gone, so dead sessions do not pile up in agent-deck's
// TUI. It matches only what OpenTab creates: group zen, titled after the
// worktree directory, at workDir. A session whose process is still running is
// left alone. It reports whether a session was removed.
func Forget(workDir string) (bool, error) {
	out, runErr := run("list", "-json")
	if runErr != nil && len(out) == 0 {
		return false, fmt.Errorf("agent-deck list: %w", runErr)
	}
	var entries []entry
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(out, &entries); err != nil {
			return false, fmt.Errorf("agent-deck list: unreadable output %q: %w", trimmed, err)
		}
	}

	workDir = filepath.Clean(workDir)
	title := filepath.Base(workDir)
	removed := false
	for _, e := range entries {
		if e.Group != Group || e.Title != title || filepath.Clean(e.Path) != workDir {
			continue
		}
		s, found, err := show(e.ID)
		if err != nil {
			return removed, err
		}
		if !found {
			continue
		}
		if !s.exited() {
			return removed, fmt.Errorf("agent-deck session %q for %s is still running; left in place", title, workDir)
		}
		if err := remove(e.ID); err != nil {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}
