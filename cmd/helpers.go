package cmd

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/gitrepo"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
)

// homeDir returns the user's home directory.
func homeDir() string {
	return os.Getenv("HOME")
}

// stdinIsTerminal reports whether stdin is an interactive terminal, so
// prompts are only shown to a human and never block scripted invocations.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// offerRegisterRepo interactively offers to add a detected clone to the
// config on first use. On acceptance it persists the entry and updates the
// in-memory cfg so the current command can proceed. Returns true if the
// repo was registered.
func offerRegisterRepo(info gitrepo.Info) bool {
	if !stdinIsTerminal() {
		return false
	}
	// Already registered under some short name — nothing to offer.
	for _, r := range cfg.Repos {
		if r.FullName == info.FullName {
			return false
		}
	}

	home := homeDir()
	fmt.Printf("%s is not registered with zen yet.\n", info.FullName)
	fmt.Printf("Register it as %q (worktrees in %s)? [Y/n]: ", info.Short, ui.ShortenHome(info.BasePath, home))
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if answer != "" && answer != "y" && answer != "yes" {
		return false
	}

	rc := config.RepoConfig{FullName: info.FullName, BasePath: info.BasePath}
	if _, err := config.AddRepo(info.Short, rc); err != nil {
		ui.LogWarn(fmt.Sprintf("could not register repo: %v", err))
		return false
	}
	cfg.Repos[info.Short] = rc
	ui.LogSuccess(fmt.Sprintf("Registered %q → %s", info.Short, info.FullName))
	return true
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
