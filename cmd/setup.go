package cmd

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// EmbeddedCommands holds the embedded Claude Code command files.
// Set by main.go before Execute().
var EmbeddedCommands embed.FS

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup to create ~/.zen/config.yaml",
	RunE:  runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	scanner := bufio.NewScanner(os.Stdin)

	configPath := filepath.Join(os.Getenv("HOME"), ".zen", "config.yaml")

	fmt.Println()
	fmt.Println(ui.BoldText("Zen Setup"))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("Prerequisites:")
	fmt.Println("  gh auth login         — authenticate GitHub CLI")
	fmt.Println("  iTerm2 installed      — for tab management")
	fmt.Println("  claude or codex CLI   — the coding agent zen launches")
	fmt.Println()

	// Check for existing config
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Config already exists: %s\n", configPath)
		fmt.Print("Overwrite? [y/N]: ")
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("Setup cancelled.")
			return nil
		}
		fmt.Println()
	}

	// Collect repos
	var repos []repoInput
	for {
		fmt.Println(ui.BoldText(fmt.Sprintf("Repository %d", len(repos)+1)))
		fmt.Println("───────────────────────────────────────────────────────────────")

		shortName := prompt(scanner, "Short name (e.g. apko)", "apko")
		fullName := promptRequired(scanner, "GitHub full name (e.g. chainguard-dev/apko)")
		basePath := promptRequired(scanner, "Base path for worktrees (e.g. ~/git/repo-apko)")

		repos = append(repos, repoInput{
			Short:    shortName,
			FullName: fullName,
			BasePath: basePath,
		})
		fmt.Println()

		fmt.Print("Add another repo? [y/N]: ")
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println()
			break
		}
		fmt.Println()
	}

	// Collect authors
	fmt.Println("───────────────────────────────────────────────────────────────")
	authors := promptRequired(scanner, "GitHub username(s) for PR filtering (comma-separated)")
	fmt.Println()

	// Choose the coding agent
	agentChoice := prompt(scanner, "Coding agent (claude or codex)", "claude")
	agentChoice = strings.ToLower(strings.TrimSpace(agentChoice))
	if agentChoice != "claude" && agentChoice != "codex" {
		fmt.Printf("  Unknown agent %q, defaulting to claude\n", agentChoice)
		agentChoice = "claude"
	}
	fmt.Println()

	// Build config
	repoMap := make(map[string]config.RepoConfig, len(repos))
	for _, r := range repos {
		repoMap[r.Short] = config.RepoConfig{
			FullName: r.FullName,
			BasePath: r.BasePath,
		}
	}

	authorList := strings.Split(authors, ",")
	for i, a := range authorList {
		authorList[i] = strings.TrimSpace(a)
	}

	cfg := config.Config{
		Repos:        repoMap,
		Authors:      authorList,
		PollInterval: "5m",
		Agent:        agentChoice,
		ClaudeBin:    "claude",
		CodexBin:     "codex",
		Watch: config.WatchConfig{
			DispatchInterval: "10s",
			CleanupInterval:  "1h",
			CleanupAfterDays: 5,
			Concurrency:      2,
			MaxRetries:       5,
		},
	}

	// Marshal to YAML
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	// Ensure ~/.zen directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	// Write config file
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Println(ui.GreenText("✓ Config written to " + configPath))
	fmt.Println()

	// Install slash-command prompts for the chosen agent
	ag := agent.New(agent.Kind(agentChoice), "")
	installedCount, err := installAgentPrompts(scanner, ag)
	if err != nil {
		return err
	}

	fmt.Println("Next steps:")
	fmt.Println("  zen status          — see dashboard")
	fmt.Println("  zen watch start     — start background daemon")
	fmt.Println("  zen inbox           — check pending PR reviews")
	if installedCount > 0 {
		if agentChoice == "codex" {
			// Codex expands custom prompts only inside its TUI composer.
			fmt.Println("  codex, then type /review-pr — review a PR")
		} else {
			fmt.Println("  claude /review-pr   — review a PR")
		}
	}
	fmt.Println()

	return nil
}

type repoInput struct {
	Short    string
	FullName string
	BasePath string
}

// prompt asks for input with a default value shown in brackets.
func prompt(scanner *bufio.Scanner, label, defaultVal string) string {
	fmt.Printf("%s [%s]: ", label, defaultVal)
	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultVal
	}
	return val
}

// promptRequired asks for input and repeats until a non-empty value is given.
func promptRequired(scanner *bufio.Scanner, label string) string {
	for {
		fmt.Printf("%s: ", label)
		scanner.Scan()
		val := strings.TrimSpace(scanner.Text())
		if val != "" {
			return val
		}
		fmt.Println("  (required)")
	}
}

// installAgentPrompts prompts the user and installs embedded slash-command
// prompt files into the chosen agent's prompts directory.
func installAgentPrompts(scanner *bufio.Scanner, ag agent.Agent) (int, error) {
	// List available prompts from the embedded FS
	entries, err := fs.ReadDir(EmbeddedCommands, "commands")
	if err != nil {
		// No embedded prompts (shouldn't happen with a proper build)
		return 0, nil
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	if len(names) == 0 {
		return 0, nil
	}

	targetDir := ag.PromptsDir()
	fmt.Printf("Install %s slash-command prompts?\n", ag.Kind())
	fmt.Printf("  Prompts: %s\n", strings.Join(names, ", "))
	fmt.Printf("  Target:  %s\n", targetDir)
	fmt.Print("Install? [Y/n]: ")
	scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if answer == "n" || answer == "no" {
		fmt.Println()
		return 0, nil
	}

	installed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		srcData, err := fs.ReadFile(EmbeddedCommands, filepath.Join("commands", e.Name()))
		if err != nil {
			return installed, fmt.Errorf("reading embedded %s: %w", e.Name(), err)
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		wrote, err := ag.EnsurePrompt(name, srcData)
		if err != nil {
			return installed, err
		}
		if wrote {
			installed++
		}
	}

	fmt.Println(ui.GreenText(fmt.Sprintf("✓ Installed %d prompt(s) to %s", installed, targetDir)))
	fmt.Println()

	return installed, nil
}
