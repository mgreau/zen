package cmd

import (
	coordmcp "github.com/mgreau/zen/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server for exposing zen tools",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server on stdio",
	Long: `Starts a Model Context Protocol (MCP) server on stdio,
exposing zen's internal APIs as tools that Claude sessions can call directly.

Available tools:
  zen_inbox          Fetch pending PR review requests
  zen_worktree_list  List git worktrees across repos
  zen_pr_details     Fetch PR details
  zen_pr_files       Fetch changed files for a PR
  zen_agent_status   List Claude sessions with token usage
  zen_config_repos   List configured repositories`,
	RunE: func(cmd *cobra.Command, args []string) error {
		srv := coordmcp.New(cfg)
		return srv.Run()
	},
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}
