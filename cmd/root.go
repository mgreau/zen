package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

var (
	debugFlag bool
	jsonFlag  bool
	cfg       *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "zen",
	Short: "Worktree orchestrator for PR reviews and feature work",
	Long: `zen - Worktree orchestrator for PR reviews and feature work

Manages git worktrees and Claude Code sessions across iTerm tabs.
Silently prepares worktrees, retries failures, and cleans up after itself.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		ui.DebugEnabled = debugFlag
		if debugFlag {
			os.Setenv("ZEN_DEBUG", "1")
		}

		var err error
		cfg, err = config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		return nil
	},
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "Enable debug output")
	rootCmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Output in JSON format")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// printJSON is a helper that marshals v to JSON and prints it.
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
