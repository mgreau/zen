// Package coordmcp provides an MCP server that exposes zen's internal APIs
// as tools, enabling Claude sessions to query zen directly.
package coordmcp

import (
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mgreau/zen/internal/config"
)

// Server wraps an MCP server with access to zen's configuration.
type Server struct {
	cfg    *config.Config
	server *mcpserver.MCPServer
}

// New creates a new MCP server with all zen tools registered.
func New(cfg *config.Config) *Server {
	s := &Server{
		cfg: cfg,
		server: mcpserver.NewMCPServer(
			"zen",
			"0.1.0",
			mcpserver.WithToolCapabilities(false),
		),
	}
	s.registerTools()
	return s
}

// Run starts the MCP server on stdio.
func (s *Server) Run() error {
	if err := mcpserver.ServeStdio(s.server); err != nil {
		return fmt.Errorf("MCP server error: %w", err)
	}
	return nil
}

// registerTools adds all zen tools to the MCP server.
func (s *Server) registerTools() {
	s.server.AddTool(
		mcpgo.NewTool("zen_inbox",
			mcpgo.WithDescription("Fetch pending PR review requests from GitHub"),
			mcpgo.WithString("repo", mcpgo.Description("Short repo name filter (e.g. 'mono')")),
			mcpgo.WithReadOnlyHintAnnotation(true),
			mcpgo.WithDestructiveHintAnnotation(false),
			mcpgo.WithOpenWorldHintAnnotation(true),
		),
		s.handleInbox,
	)

	s.server.AddTool(
		mcpgo.NewTool("zen_worktree_list",
			mcpgo.WithDescription("List git worktrees across configured repositories"),
			mcpgo.WithString("repo", mcpgo.Description("Short repo name filter (e.g. 'mono')")),
			mcpgo.WithReadOnlyHintAnnotation(true),
			mcpgo.WithDestructiveHintAnnotation(false),
			mcpgo.WithOpenWorldHintAnnotation(false),
		),
		s.handleWorktreeList,
	)

	s.server.AddTool(
		mcpgo.NewTool("zen_pr_details",
			mcpgo.WithDescription("Fetch PR details (title, author, state, branches, body)"),
			mcpgo.WithString("repo", mcpgo.Description("Short repo name (e.g. 'mono')"), mcpgo.Required()),
			mcpgo.WithNumber("pr_number", mcpgo.Description("Pull request number"), mcpgo.Required()),
			mcpgo.WithReadOnlyHintAnnotation(true),
			mcpgo.WithDestructiveHintAnnotation(false),
			mcpgo.WithOpenWorldHintAnnotation(true),
		),
		s.handlePRDetails,
	)

	s.server.AddTool(
		mcpgo.NewTool("zen_pr_files",
			mcpgo.WithDescription("Fetch list of changed files for a PR"),
			mcpgo.WithString("repo", mcpgo.Description("Short repo name (e.g. 'mono')"), mcpgo.Required()),
			mcpgo.WithNumber("pr_number", mcpgo.Description("Pull request number"), mcpgo.Required()),
			mcpgo.WithReadOnlyHintAnnotation(true),
			mcpgo.WithDestructiveHintAnnotation(false),
			mcpgo.WithOpenWorldHintAnnotation(true),
		),
		s.handlePRFiles,
	)

	s.server.AddTool(
		mcpgo.NewTool("zen_agent_status",
			mcpgo.WithDescription("List Claude sessions across worktrees with token usage and running status"),
			mcpgo.WithBoolean("running_only", mcpgo.Description("Only show running sessions")),
			mcpgo.WithReadOnlyHintAnnotation(true),
			mcpgo.WithDestructiveHintAnnotation(false),
			mcpgo.WithOpenWorldHintAnnotation(false),
		),
		s.handleAgentStatus,
	)

	s.server.AddTool(
		mcpgo.NewTool("zen_config_repos",
			mcpgo.WithDescription("List configured repositories with short names, full GitHub names, and base paths"),
			mcpgo.WithReadOnlyHintAnnotation(true),
			mcpgo.WithDestructiveHintAnnotation(false),
			mcpgo.WithOpenWorldHintAnnotation(false),
		),
		s.handleConfigRepos,
	)
}
