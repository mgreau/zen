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

// result is the subset of agent-deck's --json output zen reads.
type result struct {
	Success    bool   `json:"success"`
	ID         string `json:"id"`
	Code       string `json:"code"`
	Error      string `json:"error"`
	ExistingID string `json:"existing_id"`
	Status     string `json:"status"`
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

// OpenTab creates and starts an agent-deck session for workDir running
// command. The session is titled after the worktree directory and is not
// linked as a child of whatever agent-deck session zen happens to run in.
//
// agent-deck keeps one session per title and path. When the worktree already
// has one, OpenTab replaces it only if agent-deck reports it stopped or
// errored; a live session is left alone and reported, so resuming never
// starts a second agent in the same worktree.
func OpenTab(workDir, command string) error {
	title := filepath.Base(workDir)
	launch := []string{"launch", "-title", title, "-group", Group, "-no-parent", "-cmd", command, "-json", workDir}

	r, err := call(launch...)
	if err != nil {
		return err
	}
	if r.Success {
		return nil
	}
	if r.Code != "ALREADY_EXISTS" || r.ExistingID == "" {
		return fmt.Errorf("agent-deck launch: %s", r.Error)
	}

	// Only a session agent-deck reports dead may be replaced. The status
	// comes from session show, which reads the live tmux state; remove's own
	// guard reads a stored state that can stay "starting" after the agent has
	// exited, so zen checks liveness itself and then forces the removal.
	// Removal is registry-only: transcripts and the worktree are untouched.
	show, err := call("session", "show", "-json", r.ExistingID)
	if err != nil {
		return err
	}
	if show.Status != "stopped" && show.Status != "error" {
		return fmt.Errorf("agent-deck already has a live session %q for %s; attach with `agent-deck session attach %s`", title, workDir, r.ExistingID)
	}
	rm, err := call("session", "remove", "-force", "-json", r.ExistingID)
	if err != nil {
		return err
	}
	if !rm.Success {
		return fmt.Errorf("agent-deck session remove %s: %s", r.ExistingID, rm.Error)
	}

	r, err = call(launch...)
	if err != nil {
		return err
	}
	if !r.Success {
		return fmt.Errorf("agent-deck launch: %s", r.Error)
	}
	return nil
}
