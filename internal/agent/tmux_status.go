package agent

import (
	"encoding/json"

	"github.com/mgreau/zen/internal/tmux"
)

// WithTmuxStatus returns ag set up to report its state to the tmux window it
// runs in, setting tmux.StatusOption as soon as the state changes rather than
// on the daemon's next session scan. It is for commands zen runs in a tmux
// window it opened; commands printed for the user to run elsewhere should not
// carry it. Agents without hooks (Codex) are returned unchanged and rely on
// the daemon alone.
func WithTmuxStatus(ag Agent) Agent {
	c, ok := ag.(*claudeAgent)
	if !ok {
		return ag
	}
	withHooks := *c
	withHooks.settings = tmuxStatusSettings()
	return &withHooks
}

// tmuxStatusSettings returns Claude Code settings, for --settings, whose hooks
// mark the window "waiting" when Claude starts, finishes a turn, or asks for
// something (a permission, an answer), and "running" once the user replies.
// Each hook also sets tmux.HooksOption, telling the daemon the hooks own the
// window's status.
//
// Claude adds hooks from --settings to the user's and the project's own, so
// they replace nothing, and nothing is written into the worktree. Each hook
// is a no-op outside tmux and never fails, so it cannot block Claude.
func tmuxStatusSettings() string {
	hook := func(matcher, status string) []map[string]any {
		// "\;" reaches tmux as ";", separating its two commands.
		cmd := `[ -z "$TMUX_PANE" ] || tmux set-option -w -t "$TMUX_PANE" ` + tmux.StatusOption + " " + status +
			` \; set-option -w -t "$TMUX_PANE" ` + tmux.HooksOption + " 1 2>/dev/null || true"
		group := map[string]any{
			"hooks": []map[string]any{{"type": "command", "command": cmd}},
		}
		if matcher != "" {
			group["matcher"] = matcher
		}
		return []map[string]any{group}
	}
	settings := map[string]any{
		"hooks": map[string]any{
			// Not "compact": Claude compacts mid-turn, while still running.
			"SessionStart":     hook("startup|resume|clear", "waiting"),
			"Notification":     hook("", "waiting"),
			"Stop":             hook("", "waiting"),
			"UserPromptSubmit": hook("", "running"),
			// A permission prompt is answered without a new prompt, so the
			// tool finishing is what marks the agent running again.
			"PostToolUse": hook("", "running"),
		},
	}
	b, err := json.Marshal(settings)
	if err != nil {
		panic(err) // static input: cannot fail
	}
	return string(b)
}
