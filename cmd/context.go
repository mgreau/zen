package cmd

import (
	"fmt"

	ctxpkg "github.com/mgreau/zen/internal/context"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
)

var (
	contextPR   int
	contextRepo string
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage worktree context files",
}

var contextInjectCmd = &cobra.Command{
	Use:   "inject <worktree-path>",
	Short: "Inject PR context as CLAUDE.local.md into a worktree",
	Long: `Fetches PR metadata from GitHub and writes a CLAUDE.local.md file
in the specified worktree directory so Claude has immediate context.`,
	Args: cobra.ExactArgs(1),
	RunE: runContextInject,
}

func init() {
	contextInjectCmd.Flags().IntVar(&contextPR, "pr", 0, "PR number (required)")
	contextInjectCmd.Flags().StringVar(&contextRepo, "repo", "", "Repository short name (required)")
	contextInjectCmd.MarkFlagRequired("pr")
	contextInjectCmd.MarkFlagRequired("repo")

	contextCmd.AddCommand(contextInjectCmd)
	rootCmd.AddCommand(contextCmd)
}

func runContextInject(cmd *cobra.Command, args []string) error {
	worktreePath := args[0]
	fullRepo := cfg.RepoFullName(contextRepo)

	ui.LogInfo(fmt.Sprintf("Injecting PR #%d context from %s into %s", contextPR, fullRepo, worktreePath))

	if err := ctxpkg.InjectPRContext(cmd.Context(), worktreePath, fullRepo, contextPR); err != nil {
		return fmt.Errorf("injecting context: %w", err)
	}

	ui.LogSuccess(fmt.Sprintf("Wrote CLAUDE.local.md to %s", worktreePath))
	return nil
}
