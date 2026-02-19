package cmd

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
	fmt.Println("  gh auth login       — authenticate GitHub CLI")
	fmt.Println("  iTerm2 installed    — for tab management")
	fmt.Println("  claude installed    — Claude Code CLI")
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
		ClaudeBin:    "claude",
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

	// Install Claude Code commands
	installedCount, err := installClaudeCommands(scanner)
	if err != nil {
		return err
	}

	fmt.Println("Next steps:")
	fmt.Println("  zen status          — see dashboard")
	fmt.Println("  zen watch start     — start background daemon")
	fmt.Println("  zen inbox           — check pending PR reviews")
	if installedCount > 0 {
		fmt.Println("  claude /review-pr   — review a PR with Claude")
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

// installClaudeCommands prompts the user and installs embedded Claude Code
// command files to ~/.claude/commands/.
func installClaudeCommands(scanner *bufio.Scanner) (int, error) {
	// List available commands from the embedded FS
	entries, err := fs.ReadDir(EmbeddedCommands, "commands")
	if err != nil {
		// No embedded commands (shouldn't happen with a proper build)
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

	fmt.Println("Install Claude Code commands?")
	fmt.Printf("  Commands: %s\n", strings.Join(names, ", "))
	fmt.Println("  Target:   ~/.claude/commands/")
	fmt.Print("Install? [Y/n]: ")
	scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if answer == "n" || answer == "no" {
		fmt.Println()
		return 0, nil
	}

	targetDir := filepath.Join(os.Getenv("HOME"), ".claude", "commands")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return 0, fmt.Errorf("creating %s: %w", targetDir, err)
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

		dst := filepath.Join(targetDir, e.Name())

		// Check if file already exists
		if _, err := os.Stat(dst); err == nil {
			fmt.Printf("  %s already exists. Overwrite? [y/N]: ", dst)
			scanner.Scan()
			if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
				fmt.Printf("  Skipped %s\n", e.Name())
				continue
			}
		}

		if err := os.WriteFile(dst, srcData, 0o644); err != nil {
			return installed, fmt.Errorf("writing %s: %w", dst, err)
		}
		installed++
	}

	fmt.Println(ui.GreenText(fmt.Sprintf("✓ Installed %d command(s) to %s", installed, targetDir)))
	fmt.Println()

	return installed, nil
}
