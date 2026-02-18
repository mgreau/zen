package cmd

import (
	"fmt"
	"os"
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
	Worktree    string `json:"worktree"`
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	Size        string `json:"size"`
	Model       string `json:"model"`
	InputTokens string `json:"input_tokens"`
	OutputTokens string `json:"output_tokens"`
	LastActive  string `json:"last_active"`
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
			Worktree:     ui.ShortenHome(wt.Path, home),
			SessionID:    s.ID,
			Status:       status,
			Size:         s.SizeStr,
			Model:        session.ShortenModel(model),
			InputTokens:  session.FormatTokenCount(tokens.InputTokens),
			OutputTokens: session.FormatTokenCount(tokens.OutputTokens),
			LastActive:   session.FormatAge(lastActive),
		}
		entries = append(entries, entry)
	}

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

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "WORKTREE\tSTATUS\tSIZE\tMODEL\tTOKENS (IN/OUT)\tLAST ACTIVE")
	fmt.Fprintln(w, "--------\t------\t----\t-----\t---------------\t-----------")

	for _, e := range entries {
		statusStr := e.Status
		if e.Status == "running" {
			statusStr = ui.GreenText("running")
		} else {
			statusStr = ui.DimText("stopped")
		}

		tokenStr := fmt.Sprintf("%s/%s", e.InputTokens, e.OutputTokens)

		// Shorten worktree path for display
		wtDisplay := e.Worktree
		if parts := strings.Split(wtDisplay, "/"); len(parts) > 2 {
			wtDisplay = "~/" + strings.Join(parts[len(parts)-2:], "/")
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			wtDisplay, statusStr, e.Size, e.Model, tokenStr, e.LastActive)
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
