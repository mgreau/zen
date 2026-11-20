package cmd

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/terminal"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
)

// homeDir returns the user's home directory.
func homeDir() string {
	return os.Getenv("HOME")
}

// agentFlag is the optional --agent override shared by commands that launch an
// agent (review, work, resume). Empty means "use the configured default".
var agentFlag string

// addAgentFlag registers the shared --agent flag on a command.
func addAgentFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&agentFlag, "agent", "", "Agent to use: claude or codex (defaults to config)")
}

// resolveAgent builds the agent for the current invocation, honouring --agent.
// An unrecognised --agent value is an error rather than a silent fallback.
func resolveAgent() (agent.Agent, error) {
	if kind := agent.Kind(cfg.AgentKind(agentFlag)); !kind.Valid() {
		return nil, fmt.Errorf("invalid agent %q: must be \"claude\" or \"codex\"", kind)
	}
	return cfg.NewAgent(agentFlag), nil
}

// hasAgentSession reports whether the agent has any session for the worktree.
func hasAgentSession(ag agent.Agent, worktreePath string) bool {
	sessions, _ := ag.FindSessions(worktreePath)
	return len(sessions) > 0
}

// ensureReviewPrompt installs the embedded /review-pr slash-command prompt into
// the agent's prompts directory if it is not already present.
func ensureReviewPrompt(ag agent.Agent) {
	data, err := fs.ReadFile(EmbeddedCommands, "commands/review-pr.md")
	if err != nil {
		return // no embedded prompt (shouldn't happen in a proper build)
	}
	installed, err := ag.EnsurePrompt("review-pr", data)
	if err != nil {
		ui.LogInfo(fmt.Sprintf("Warning: could not install /review-pr prompt: %v", err))
		return
	}
	if installed {
		ui.LogInfo(fmt.Sprintf("Installed /review-pr prompt for %s", ag.Kind()))
	}
}

// openTab runs the agent command for workDir in terminal t and reports what
// happened: a new tab, the worktree's existing tab reused, or a switch to the
// tab where the agent already runs, in which case the command did not run.
//
// build makes the command from an agent. openTab passes it ag set up for t
// (see terminal.AgentFor); printing build(ag) instead gives the plain command
// for the user to run themselves.
func openTab(t terminal.Terminal, workDir string, ag agent.Agent, build func(agent.Agent) string) error {
	outcome, err := t.OpenTab(workDir, build(terminal.AgentFor(t, ag, cfg.Notify)))
	if err != nil {
		return fmt.Errorf("opening %s %s: %w", t.Name(), t.Unit(), err)
	}
	msg := terminal.Describe(t, outcome)
	if outcome == terminal.AlreadyRunning {
		ui.LogWarn(msg)
	} else {
		ui.LogSuccess(msg)
	}
	return nil
}
