package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	agentRunning bool
	agentFull    bool
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage Claude agent sessions",
}

var agentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all Claude sessions across worktrees",
	Long: `Displays a table of Claude Code sessions found across all worktrees,
including token usage, running status, and last activity time.`,
	RunE: runAgentStatus,
}

func init() {
	agentStatusCmd.Flags().BoolVar(&agentRunning, "running", false, "Only show running sessions")
	agentStatusCmd.Flags().BoolVar(&agentFull, "full", false, "Scan full session files for accurate token totals (slower)")

	agentCmd.AddCommand(agentStatusCmd)
	rootCmd.AddCommand(agentCmd)
}

// agentStatusEntry holds one row of the agent status output.
type agentStatusEntry struct {
	Worktree        string `json:"worktree"`
	SessionID       string `json:"session_id"`
	Status          string `json:"status"`
	Size            string `json:"size"`
	Model           string `json:"model"`
	InputTokens     string `json:"input_tokens"`
	OutputTokens    string `json:"output_tokens"`
	LastActive      string `json:"last_active"`
	lastActiveEpoch int64  // unexported, for sorting only
}

func runAgentStatus(cmd *cobra.Command, args []string) error {
	home := homeDir()

	wts, err := worktree.ListAll(cfg)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	var entries []agentStatusEntry
	var totalRunning, totalStopped int

	for _, wt := range wts {
		sessions, _ := session.FindSessions(wt.Path)
		if len(sessions) == 0 {
			continue
		}

		// Only show the most recent session per worktree
		s := sessions[0]
		filePath := session.SessionFilePath(wt.Path, s.ID)

		var model string
		var tokens session.TokenUsage
		if agentFull {
			model, tokens, _ = session.ParseSessionDetailFull(filePath)
		} else {
			model, tokens, _ = session.ParseSessionDetailTail(filePath)
		}

		running := session.IsProcessRunning(s.ID)

		if agentRunning && !running {
			continue
		}

		status := "stopped"
		if running {
			status = "running"
			totalRunning++
		} else {
			totalStopped++
		}

		lastActive := time.Unix(s.Modified, 0)

		entry := agentStatusEntry{
			Worktree:        ui.ShortenHome(wt.Path, home),
			SessionID:       s.ID,
			Status:          status,
			Size:            s.SizeStr,
			Model:           session.ShortenModel(model),
			InputTokens:     session.FormatTokenCount(tokens.InputTokens),
			OutputTokens:    session.FormatTokenCount(tokens.OutputTokens),
			LastActive:      session.FormatAge(lastActive),
			lastActiveEpoch: s.Modified,
		}
		entries = append(entries, entry)
	}

	// Sort by last active (most recent first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastActiveEpoch > entries[j].lastActiveEpoch
	})

	if jsonFlag {
		printJSON(entries)
		return nil
	}

	if len(entries) == 0 {
		if agentRunning {
			fmt.Println("No running sessions found.")
		} else {
			fmt.Println("No sessions found across worktrees.")
		}
		return nil
	}

	fmt.Println()
	ui.SectionHeader("Agent Sessions")
	fmt.Println()

	// Compute max worktree name width for alignment
	maxWT := len("WORKTREE")
	for _, e := range entries {
		name := worktreeDisplayName(e.Worktree)
		if len(name) > maxWT {
			maxWT = len(name)
		}
	}

	// Use tabwriter only for plain-text columns, then append colored status after
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%-7s  %-*s  %-7s  %-6s  %-12s  %s\n", "STATUS", maxWT, "WORKTREE", "SIZE", "MODEL", "TOKENS(I/O)", "LAST ACTIVE")
	fmt.Fprintf(w, "%-7s  %-*s  %-7s  %-6s  %-12s  %s\n", "───────", maxWT, strings.Repeat("─", maxWT), "───────", "──────", "────────────", "───────────")

	for _, e := range entries {
		statusStr := fmt.Sprintf("%-7s", e.Status)
		if e.Status == "running" {
			statusStr = ui.GreenText(statusStr)
		} else {
			statusStr = ui.DimText(statusStr)
		}

		tokenStr := fmt.Sprintf("%s/%s", e.InputTokens, e.OutputTokens)
		name := worktreeDisplayName(e.Worktree)

		fmt.Fprintf(w, "%s  %-*s  %-7s  %-6s  %-12s  %s\n",
			statusStr, maxWT, name, e.Size, e.Model, tokenStr, ui.DimText(e.LastActive))
	}
	w.Flush()

	fmt.Println()
	total := totalRunning + totalStopped
	fmt.Printf("%s %d sessions (%s running, %s stopped)\n",
		ui.DimText("Total:"),
		total,
		ui.GreenText(fmt.Sprintf("%d", totalRunning)),
		ui.DimText(fmt.Sprintf("%d", totalStopped)),
	)
	fmt.Println()

	return nil
}

// worktreeDisplayName extracts the last path component (worktree dir name) for display.
func worktreeDisplayName(path string) string {
	if parts := strings.Split(path, "/"); len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}
