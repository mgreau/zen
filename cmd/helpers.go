package cmd

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
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

// stackFlag is the optional --stack override shared by commands that launch an
// agent. It beats every stack key in config.
var stackFlag string

// addStackFlag registers the shared --stack flag on a command.
func addStackFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&stackFlag, "stack", "", "Agent stack to launch the session through: a path or a name on PATH (overrides config)")
}

// resolveStack picks and validates the agent stack for a session in repo, or
// returns nil when none is configured. Call it before creating a terminal tab
// so a bad value fails the command first.
func resolveStack(mode config.StackMode, repo string) (*config.StackRef, error) {
	return cfg.ResolveStack(mode, repo, stackFlag)
}

// stackModeFor maps a worktree kind to the stack keys that apply to it.
func stackModeFor(t worktree.Type) config.StackMode {
	if t == worktree.TypePRReview {
		return config.ReviewStack
	}
	return config.WorkStack
}

// printStack reports the stack a session launches through, if any.
func printStack(ref *config.StackRef) {
	if ref != nil {
		fmt.Printf("Stack: %s\n", ui.CyanText(ref.Value))
	}
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
