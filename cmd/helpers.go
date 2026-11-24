package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
// An unrecognised --agent value is an error rather than a silent fallback.
func resolveAgent() (agent.Agent, error) {
	if kind := agent.Kind(cfg.AgentKind(agentFlag)); !kind.Valid() {
		return nil, fmt.Errorf("invalid agent %q: must be \"claude\" or \"codex\"", kind)
	}
	return cfg.NewAgent(agentFlag), nil
}

// prepareLaunch readies the agent to launch in workDir (starting Codex's
// remote-control daemon when needed), reporting progress and any fallback.
func prepareLaunch(ag agent.Agent, workDir string) agent.Agent {
	if ag.NeedsLaunchPrep() {
		ui.LogInfo(fmt.Sprintf("Preparing %s remote control...", ag.Kind()))
	}
	prepared, err := ag.PrepareLaunch(workDir)
	if err != nil {
		ui.LogWarn(fmt.Sprintf("%s remote control unavailable, launching without it: %v", ag.Kind(), err))
	}
	return prepared
}

// hasAgentSession reports whether the agent has any session for the worktree.
func hasAgentSession(ag agent.Agent, worktreePath string) bool {
	sessions, _ := ag.FindSessions(worktreePath)
	return len(sessions) > 0
}

// ensurePrompts installs each embedded slash-command prompt that works with
// ag (see promptSupported) and is not in its prompts directory yet, so a zen
// upgrade brings new prompts without running zen setup again. Prompts already
// there are left alone.
func ensurePrompts(ag agent.Agent) {
	ensurePromptsFrom(EmbeddedCommands, ag)
}

// ensurePromptsFrom is ensurePrompts reading the prompts from fsys's
// commands directory, and returns the names it installed.
func ensurePromptsFrom(fsys fs.FS, ag agent.Agent) []string {
	entries, err := fs.ReadDir(fsys, "commands")
	if err != nil {
		return nil // no embedded prompts (shouldn't happen in a proper build)
	}
	var installed []string
	for _, e := range entries {
		if e.IsDir() || !promptSupported(e.Name(), ag) {
			continue
		}
		data, err := fs.ReadFile(fsys, "commands/"+e.Name())
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		wrote, err := ag.EnsurePrompt(name, data)
		if err != nil {
			ui.LogInfo(fmt.Sprintf("Warning: could not install /%s prompt: %v", name, err))
			continue
		}
		if wrote {
			ui.LogInfo(fmt.Sprintf("Installed /%s prompt for %s", name, ag.Kind()))
			installed = append(installed, name)
		}
	}
	return installed
}

// ensureImportPrompt installs Claude Code's missing zen prompts, /zen-import
// among them, whatever the configured agent. /zen-import is typed in a
// Claude session zen didn't start, so it can't be installed on first use
// like /review-pr; zen installs it from commands you run anyway. It is
// skipped when ~/.claude doesn't exist, so zen doesn't create Claude's
// directory for someone who only uses Codex.
func ensureImportPrompt() {
	ensureImportPromptFrom(EmbeddedCommands)
}

func ensureImportPromptFrom(fsys fs.FS) []string {
	ag := cfg.NewAgent(string(agent.Claude))
	if _, err := os.Stat(filepath.Dir(ag.PromptsDir())); err != nil {
		return nil
	}
	return ensurePromptsFrom(fsys, ag)
}

// humanSize formats a byte count for display.
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
