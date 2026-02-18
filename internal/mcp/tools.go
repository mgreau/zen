package coordmcp

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	ghpkg "github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/worktree"
)

// jsonResult marshals v to JSON and returns it as a text tool result.
func jsonResult(v any) (*mcpgo.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcpgo.NewToolResultText(string(data)), nil
}

// handleInbox fetches pending PR review requests from GitHub.
func (s *Server) handleInbox(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	repoShort := req.GetString("repo", "")
	repoFilter := ""
	if repoShort != "" {
		repoFilter = s.cfg.RepoFullName(repoShort)
	}

	reviews, err := ghpkg.GetReviewRequests(ctx, repoFilter)
	if err != nil {
		return mcpgo.NewToolResultError("failed to fetch review requests: " + err.Error()), nil
	}
	if reviews == nil {
		reviews = []ghpkg.ReviewRequest{}
	}
	return jsonResult(reviews)
}

// handleWorktreeList lists git worktrees across configured repositories.
func (s *Server) handleWorktreeList(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	repoShort := req.GetString("repo", "")

	var wts []worktree.Worktree
	var err error
	if repoShort != "" {
		wts, err = worktree.ListForRepo(s.cfg, repoShort)
	} else {
		wts, err = worktree.ListAll(s.cfg)
	}
	if err != nil {
		return mcpgo.NewToolResultError("failed to list worktrees: " + err.Error()), nil
	}
	if wts == nil {
		wts = []worktree.Worktree{}
	}
	return jsonResult(wts)
}

// handlePRDetails fetches PR details from GitHub.
func (s *Server) handlePRDetails(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	repoShort, err := req.RequireString("repo")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	prNumber, err := req.RequireInt("pr_number")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	fullRepo := s.cfg.RepoFullName(repoShort)
	client, err := ghpkg.NewClient(ctx)
	if err != nil {
		return mcpgo.NewToolResultError("failed to create GitHub client: " + err.Error()), nil
	}

	details, err := client.GetPRDetails(ctx, fullRepo, prNumber)
	if err != nil {
		return mcpgo.NewToolResultError("failed to fetch PR details: " + err.Error()), nil
	}
	return jsonResult(details)
}

// handlePRFiles fetches the list of changed files for a PR.
func (s *Server) handlePRFiles(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	repoShort, err := req.RequireString("repo")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	prNumber, err := req.RequireInt("pr_number")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	fullRepo := s.cfg.RepoFullName(repoShort)
	client, err := ghpkg.NewClient(ctx)
	if err != nil {
		return mcpgo.NewToolResultError("failed to create GitHub client: " + err.Error()), nil
	}

	files, err := client.GetPRFiles(ctx, fullRepo, prNumber)
	if err != nil {
		return mcpgo.NewToolResultError("failed to fetch PR files: " + err.Error()), nil
	}
	if files == nil {
		files = []string{}
	}
	return jsonResult(files)
}

// agentStatusEntry holds one row of agent status output for MCP.
type agentStatusEntry struct {
	Worktree     string `json:"worktree"`
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	Size         string `json:"size"`
	Model        string `json:"model"`
	InputTokens  string `json:"input_tokens"`
	OutputTokens string `json:"output_tokens"`
	LastActive   string `json:"last_active"`
}

// handleAgentStatus lists Claude sessions across worktrees.
func (s *Server) handleAgentStatus(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	runningOnly := req.GetBool("running_only", false)

	wts, err := worktree.ListAll(s.cfg)
	if err != nil {
		return mcpgo.NewToolResultError("failed to list worktrees: " + err.Error()), nil
	}

	var entries []agentStatusEntry
	for _, wt := range wts {
		sessions, _ := session.FindSessions(wt.Path)
		if len(sessions) == 0 {
			continue
		}

		sess := sessions[0]
		filePath := session.SessionFilePath(wt.Path, sess.ID)
		model, tokens, _ := session.ParseSessionDetailTail(filePath)
		running := session.IsProcessRunning(sess.ID)

		if runningOnly && !running {
			continue
		}

		status := "stopped"
		if running {
			status = "running"
		}

		lastActive := time.Unix(sess.Modified, 0)

		entries = append(entries, agentStatusEntry{
			Worktree:     wt.Path,
			SessionID:    sess.ID,
			Status:       status,
			Size:         sess.SizeStr,
			Model:        session.ShortenModel(model),
			InputTokens:  session.FormatTokenCount(tokens.InputTokens),
			OutputTokens: session.FormatTokenCount(tokens.OutputTokens),
			LastActive:   session.FormatAge(lastActive),
		})
	}
	if entries == nil {
		entries = []agentStatusEntry{}
	}
	return jsonResult(entries)
}

// repoEntry holds one configured repository for JSON output.
type repoEntry struct {
	ShortName string `json:"short_name"`
	FullName  string `json:"full_name"`
	BasePath  string `json:"base_path"`
}

// handleConfigRepos lists configured repositories.
func (s *Server) handleConfigRepos(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	var repos []repoEntry
	for name, rc := range s.cfg.Repos {
		repos = append(repos, repoEntry{
			ShortName: name,
			FullName:  rc.FullName,
			BasePath:  rc.BasePath,
		})
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].ShortName < repos[j].ShortName
	})
	if repos == nil {
		repos = []repoEntry{}
	}
	return jsonResult(repos)
}
