package coordmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	ghpkg "github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/reconciler"
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
// Uses cached session snapshot when available, falls back to real-time scanning.
func (s *Server) handleAgentStatus(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	runningOnly := req.GetBool("running_only", false)

	var entries []agentStatusEntry

	// Try cached snapshot first — only use if it contains paths matching our config
	snapshot, _ := reconciler.ReadSessionSnapshot()
	basePaths := s.cfg.AllBasePaths()
	if reconciler.IsSnapshotFresh(snapshot, 60*time.Second) && reconciler.SnapshotMatchesConfig(snapshot, basePaths) {
		for _, ss := range snapshot.Sessions {
			if runningOnly && ss.Status == "stopped" {
				continue
			}
			entries = append(entries, agentStatusEntry{
				Worktree:     ss.WorktreePath,
				SessionID:    ss.SessionID,
				Status:       ss.Status,
				Size:         ss.Size,
				Model:        ss.Model,
				InputTokens:  ss.InputTokens,
				OutputTokens: ss.OutputTokens,
				LastActive:   session.FormatAge(time.Unix(ss.LastModified, 0)),
			})
		}
	} else {
		// Fall back to real-time scanning
		wts, err := worktree.ListAll(s.cfg)
		if err != nil {
			return mcpgo.NewToolResultError("failed to list worktrees: " + err.Error()), nil
		}

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

// whoAmIMergedEntry represents a commit merged to origin/main.
type whoAmIMergedEntry struct {
	Repo     string `json:"repo"`
	Hash     string `json:"hash"`
	Subject  string `json:"subject"`
	Body     string `json:"body,omitempty"`
	PRNumber string `json:"pr_number,omitempty"`
	Date     string `json:"date"`
}

// whoAmIWorktreeEntry holds per-worktree activity data.
type whoAmIWorktreeEntry struct {
	Name       string `json:"name"`
	Repo       string `json:"repo"`
	Type       string `json:"type"`
	Branch     string `json:"branch"`
	Commits    int    `json:"commits"`
	LastCommit string `json:"last_commit,omitempty"`
	HasSession bool   `json:"has_session"`
	PRNumber   int    `json:"pr_number,omitempty"`
}

// whoAmISummary holds the complete who-am-i response.
type whoAmISummary struct {
	Period      string                `json:"period"`
	Since       string                `json:"since"`
	Repos       []string              `json:"repos"`
	Merged      []whoAmIMergedEntry   `json:"merged"`
	InProgress  []whoAmIWorktreeEntry `json:"in_progress"`
	PRReviews   []whoAmIWorktreeEntry `json:"pr_reviews"`
}

// handleWhoAmI returns a summary of work done across repos.
func (s *Server) handleWhoAmI(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	repoFilter := req.GetString("repo", "")
	period := req.GetString("period", "7d")
	mergedOnly := req.GetBool("merged_only", false)

	since, err := whoamiParsePeriod(period)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// Determine repos
	repos := s.cfg.RepoNames()
	if repoFilter != "" {
		if s.cfg.RepoBasePath(repoFilter) == "" {
			return mcpgo.NewToolResultError(fmt.Sprintf("unknown repo %q", repoFilter)), nil
		}
		repos = []string{repoFilter}
	}

	// Merged commits
	var merged []whoAmIMergedEntry
	for _, repo := range repos {
		basePath := s.cfg.RepoBasePath(repo)
		originPath := filepath.Join(basePath, repo)
		entries := whoamiMergedCommits(originPath, since, mergedOnly)
		for i := range entries {
			entries[i].Repo = repo
		}
		merged = append(merged, entries...)
	}

	if mergedOnly {
		if merged == nil {
			merged = []whoAmIMergedEntry{}
		}
		return jsonResult(whoAmISummary{
			Period:     period,
			Since:      since.Format("2006-01-02"),
			Repos:      repos,
			Merged:     merged,
			InProgress: []whoAmIWorktreeEntry{},
			PRReviews:  []whoAmIWorktreeEntry{},
		})
	}

	// In-progress worktrees
	wts, err := worktree.ListAll(s.cfg)
	if err != nil {
		return mcpgo.NewToolResultError("failed to list worktrees: " + err.Error()), nil
	}

	var inProgress, prReviews []whoAmIWorktreeEntry
	for _, wt := range wts {
		if repoFilter != "" && wt.Repo != repoFilter {
			continue
		}

		commits := whoamiCountCommits(wt.Path, since)
		hasSession := session.HasActiveSession(wt.Path)
		if commits == 0 && !whoamiHasRecentSession(wt.Path, since) {
			continue
		}

		entry := whoAmIWorktreeEntry{
			Name:       wt.Name,
			Repo:       wt.Repo,
			Type:       string(wt.Type),
			Branch:     wt.Branch,
			Commits:    commits,
			HasSession: hasSession,
			PRNumber:   wt.PRNumber,
		}
		if commits > 0 {
			entry.LastCommit = whoamiLastCommit(wt.Path)
		}

		if wt.Type == worktree.TypePRReview {
			prReviews = append(prReviews, entry)
		} else {
			inProgress = append(inProgress, entry)
		}
	}

	if merged == nil {
		merged = []whoAmIMergedEntry{}
	}
	if inProgress == nil {
		inProgress = []whoAmIWorktreeEntry{}
	}
	if prReviews == nil {
		prReviews = []whoAmIWorktreeEntry{}
	}

	return jsonResult(whoAmISummary{
		Period:     period,
		Since:      since.Format("2006-01-02"),
		Repos:      repos,
		Merged:     merged,
		InProgress: inProgress,
		PRReviews:  prReviews,
	})
}

// --- who-am-i git helpers (MCP-local, no dependency on cmd package) ---

var whoamiPRNumberRe = regexp.MustCompile(`\(#(\d+)\)\s*$`)

func whoamiParsePeriod(period string) (time.Time, error) {
	now := time.Now()
	if len(period) < 2 {
		return time.Time{}, fmt.Errorf("invalid period %q", period)
	}
	unit := period[len(period)-1]
	var val int
	if _, err := fmt.Sscanf(period[:len(period)-1], "%d", &val); err != nil || val <= 0 {
		return time.Time{}, fmt.Errorf("invalid period %q", period)
	}
	switch unit {
	case 'd':
		return now.AddDate(0, 0, -val), nil
	case 'w':
		return now.AddDate(0, 0, -val*7), nil
	case 'm':
		return now.AddDate(0, -val, 0), nil
	default:
		return time.Time{}, fmt.Errorf("invalid period unit %q", string(unit))
	}
}

func whoamiMergedCommits(originPath string, since time.Time, withBody bool) []whoAmIMergedEntry {
	authorCmd := exec.Command("git", "config", "user.name")
	authorCmd.Dir = originPath
	authorOut, err := authorCmd.Output()
	if err != nil {
		return nil
	}
	author := strings.TrimSpace(string(authorOut))

	cmd := exec.Command("git", "log",
		"--format=%h\t%s\t%ad",
		"--date=short",
		"--since="+since.Format("2006-01-02"),
		"--author="+author,
		"origin/main",
	)
	cmd.Dir = originPath
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var entries []whoAmIMergedEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		e := whoAmIMergedEntry{Hash: parts[0], Subject: parts[1]}
		if len(parts) == 3 {
			e.Date = parts[2]
		}
		if m := whoamiPRNumberRe.FindStringSubmatch(e.Subject); len(m) == 2 {
			e.PRNumber = m[1]
			e.Subject = strings.TrimSpace(whoamiPRNumberRe.ReplaceAllString(e.Subject, ""))
		}
		if withBody {
			bodyCmd := exec.Command("git", "log", "-1", "--format=%b", e.Hash)
			bodyCmd.Dir = originPath
			if bodyOut, err := bodyCmd.Output(); err == nil {
				e.Body = strings.TrimSpace(string(bodyOut))
			}
		}
		entries = append(entries, e)
	}
	return entries
}

func whoamiCountCommits(wtPath string, since time.Time) int {
	cmd := exec.Command("git", "rev-list", "--count", "--since="+since.Format("2006-01-02"), "origin/main..HEAD")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var count int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count)
	return count
}

func whoamiLastCommit(wtPath string) string {
	cmd := exec.Command("git", "log", "-1", "--format=%s", "origin/main..HEAD")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func whoamiHasRecentSession(wtPath string, since time.Time) bool {
	sessions, _ := session.FindSessions(wtPath)
	if len(sessions) == 0 {
		return false
	}
	return time.Unix(sessions[0].Modified, 0).After(since)
}
