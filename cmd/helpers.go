package cmd

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/mgreau/zen/internal/agent"
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
func resolveAgent() agent.Agent {
	return cfg.NewAgent(agentFlag)
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
