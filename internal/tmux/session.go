package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// AgentsWindowName is the name of the window SetUpSession opens for the
// agent's view of its sessions.
const AgentsWindowName = "Agents"

// SetUpSession prepares a session tmux has just created, from zen's
// session-created hook:
//
//   - With first, a command such as "zen inbox", it types it into the first
//     window, so the window stays an ordinary shell prompt afterwards. Only
//     when the session is a lone shell: a session created to run a program
//     (tmux new 'vim') must not get keys typed into it.
//   - With agents, a command such as "claude agents", it opens an Agents
//     window in the background, in agentsDir, through the user's login
//     shell like the windows zen opens for worktrees, so you stay on the
//     first window. A missing agentsDir falls back to the session's
//     directory rather than failing.
//
// Only a session a terminal attached to when it was made (tmux new, tmux
// new -A) is set up. A session a script or tool creates in the background
// (tmux new -d) is left alone, since the script may type into it too; so
// is one grouped with another (tmux new -t other), which shares that
// session's windows, where someone may be typing.
func SetUpSession(session, first, agents, agentsDir string) error {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", session+":",
		"#{session_attached}\t#{session_grouped}").Output()
	if err != nil {
		return fmt.Errorf("tmux display-message: %w", err)
	}
	if !interactive(strings.TrimSpace(string(out))) {
		return nil
	}
	return setUp(session, first, agents, agentsDir)
}

// setUp does SetUpSession's work, for a session it decided to set up.
func setUp(session, first, agents, agentsDir string) error {
	if first != "" {
		out, err := exec.Command("tmux", "display-message", "-p", "-t", session+":",
			"#{session_windows}\t#{pane_id}\t#{pane_start_command}").Output()
		if err != nil {
			return fmt.Errorf("tmux display-message: %w", err)
		}
		if p, ok := freshShell(strings.TrimRight(string(out), "\n")); ok {
			if err := run("send-keys", "-t", p, "-l", first); err != nil {
				return err
			}
			if err := run("send-keys", "-t", p, "Enter"); err != nil {
				return err
			}
		}
	}
	if agents != "" {
		args := []string{"new-window", "-d", "-t", session + ":", "-n", AgentsWindowName}
		if info, err := os.Stat(agentsDir); agentsDir != "" && err == nil && info.IsDir() {
			args = append(args, "-c", agentsDir)
		}
		args = append(args, shellArgs(agents, userShell())...)
		if err := run(args...); err != nil {
			return err
		}
	}
	return nil
}

// interactive reads "attached clients, grouped" for a new session and
// reports whether to set it up.
func interactive(info string) bool {
	f := strings.Split(info, "\t")
	return len(f) == 2 && f[0] != "0" && f[0] != "" && f[1] != "1"
}

// freshShell reads "windows, pane ID, start command" for a session's current
// pane and returns the pane if the session is one window running the
// default shell.
func freshShell(info string) (string, bool) {
	f := strings.Split(info, "\t")
	if len(f) != 3 || f[0] != "1" || f[2] != "" {
		return "", false
	}
	return f[1], true
}
