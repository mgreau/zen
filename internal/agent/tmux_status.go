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
//
// When zenBin, the path of the zen executable, is not empty, the hooks also
// run "zen hook notify", which sends a desktop notification naming the
// worktree, and Claude's own notifications are turned off for this session
// so you don't get each one twice. Your global Claude settings are untouched.
func WithTmuxStatus(ag Agent, zenBin string) Agent {
	c, ok := ag.(*claudeAgent)
	if !ok {
		return ag
	}
	withHooks := *c
	withHooks.settings = tmuxStatusSettings(zenBin)
	return &withHooks
}

// waitingNotifications matches the Claude notification types that wait on
// the user. Others, such as a background agent finishing (agent_completed)
// or a login succeeding, come while Claude may still be working.
const waitingNotifications = "permission_prompt|idle_prompt|elicitation_dialog|elicitation_url_dialog|agent_needs_input"

// tmuxStatusSettings returns Claude Code settings, for --settings, whose hooks
// mark the window "waiting" when Claude starts, finishes a turn, or asks for
// something (a permission, an answer), and "running" once the user replies.
// Each hook also sets tmux.HooksOption, telling the daemon the hooks own the
// window's status. With zenBin, Notification and Stop also run zen's notify
// hook, and Claude's own notifications are turned off.
//
// Claude adds hooks from --settings to the user's and the project's own, so
// they replace nothing, and nothing is written into the worktree. Each hook
// is a no-op outside tmux and never fails, so it cannot block Claude.
func tmuxStatusSettings(zenBin string) string {
	// Claude runs the notify hook in the background (async), so the time zen
	// takes to start never delays it.
	var notifyHook map[string]any
	if zenBin != "" {
		notifyHook = map[string]any{"type": "command", "command": shellQuote(zenBin) + " hook notify", "async": true}
	}
	hook := func(matcher, status string, notify bool) []map[string]any {
		// "\;" reaches tmux as ";", separating its two commands.
		cmd := `[ -z "$TMUX_PANE" ] || tmux set-option -w -t "$TMUX_PANE" ` + tmux.StatusOption + " " + status +
			` \; set-option -w -t "$TMUX_PANE" ` + tmux.HooksOption + " 1 2>/dev/null || true"
		group := map[string]any{"hooks": []map[string]any{{"type": "command", "command": cmd}}}
		groups := []map[string]any{group}
		if matcher != "" {
			group["matcher"] = matcher
		}
		if notify && notifyHook != nil {
			// zen filters notifications itself (notify.terminal_events), so
			// the notify hook sees every event, whatever the status matches.
			if matcher == "" {
				group["hooks"] = append(group["hooks"].([]map[string]any), notifyHook)
			} else {
				groups = append(groups, map[string]any{"hooks": []map[string]any{notifyHook}})
			}
		}
		return groups
	}
	settings := map[string]any{
		"hooks": map[string]any{
			// Not "compact": Claude compacts mid-turn, while still running.
			"SessionStart": hook("startup|resume|clear", "waiting", false),
			"Notification": hook(waitingNotifications, "waiting", true),
			"Stop":         hook("", "waiting", true),
			// A turn that ends on an error, such as a rate limit, waits on
			// the user too, but fires StopFailure instead of Stop.
			"StopFailure":      hook("", "waiting", true),
			"UserPromptSubmit": hook("", "running", false),
			// A permission prompt is answered without a new prompt, so the
			// tool finishing is what marks the agent running again.
			"PostToolUse": hook("", "running", false),
			// An approved tool that fails fires this instead.
			"PostToolUseFailure": hook("", "running", false),
			// Claude exiting: mark the window stopped, and give up the
			// status, now that no hook will report it, even with no daemon
			// running. /clear ends a session too, but SessionStart follows.
			"SessionEnd": []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": `[ -z "$TMUX_PANE" ] || tmux set-option -w -t "$TMUX_PANE" ` +
				tmux.StatusOption + ` stopped \; set-option -w -u -t "$TMUX_PANE" ` + tmux.HooksOption + " 2>/dev/null || true"}}}},
		},
	}
	if notifyHook != nil {
		settings["preferredNotifChannel"] = "notifications_disabled"
	}
	b, err := json.Marshal(settings)
	if err != nil {
		panic(err) // static input: cannot fail
	}
	return string(b)
}
