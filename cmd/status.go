package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/prcache"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"dashboard"},
	Short:   "Overview of all active work (reviews + features + sessions)",
	RunE:    runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

// StatusData holds the structured status output.
type StatusData struct {
	Worktrees    *worktree.Stats  `json:"worktrees"`
	PRReviews    []StatusPRReview `json:"pr_reviews"`
	Features     []worktree.Worktree `json:"features"`
	DaemonStatus string           `json:"daemon_status"`
	DaemonPID    string           `json:"daemon_pid,omitempty"`
}

// StatusPRReview enriches a worktree with remote PR state and cleanup info.
type StatusPRReview struct {
	worktree.Worktree
	Title      string `json:"title,omitempty"`
	State      string `json:"state,omitempty"`
	AgeDays    int    `json:"age_days"`
	CleanupIn  int    `json:"cleanup_in_days,omitempty"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	// Worktree stats
	wtStats, err := worktree.GetStats(cfg)
	if err != nil {
		return fmt.Errorf("getting worktree stats: %w", err)
	}

	// All worktrees
	wts, _ := worktree.ListAll(cfg)

	var prWTs []worktree.Worktree
	var features []worktree.Worktree
	for _, wt := range wts {
		switch wt.Type {
		case worktree.TypePRReview:
			prWTs = append(prWTs, wt)
		case worktree.TypeFeature:
			features = append(features, wt)
		}
	}

	// Enrich PR reviews with remote state
	prCache := prcache.Load()
	prReviews := enrichPRReviews(prWTs, prCache)

	// Daemon status
	daemonStatus, daemonPID := getDaemonStatus()

	if jsonFlag {
		printJSON(StatusData{
			Worktrees:    wtStats,
			PRReviews:    prReviews,
			Features:     features,
			DaemonStatus: daemonStatus,
			DaemonPID:    daemonPID,
		})
		return nil
	}

	// Human-readable output
	ui.Banner("Zen Status Dashboard")

	home := homeDir()

	// Worktrees
	ui.SectionHeader("Worktrees")
	fmt.Printf("  Total: %d  |  PR Reviews: %d  |  Features: %d\n\n",
		wtStats.Total, wtStats.PRReviews, wtStats.Features)

	// PR Reviews
	ui.SectionHeader("PR Reviews")
	if len(prReviews) == 0 {
		fmt.Println("  No PR review worktrees")
	} else {
		fmt.Printf("  %-8s  %-6s  %-42s  %s\n", "State", "PR", "Title", "Path")
		fmt.Printf("  %-8s  %-6s  %-42s  %s\n", "────────", "──────", "──────────────────────────────────────────", "──────────────────────────────")

		for i, r := range prReviews {
			if i >= 10 {
				fmt.Printf("  ... and %d more\n", len(prReviews)-10)
				break
			}
			title := ui.Truncate(r.Title, 40)
			stateCol := formatPRState(r.State, r.CleanupIn)
			fmt.Printf("  %s  %s  %-42s  %s\n",
				stateCol,
				ui.CyanText(fmt.Sprintf("#%-5d", r.PRNumber)),
				title,
				ui.DimText(ui.ShortenHome(r.Path, home)))
		}
	}
	ui.Hint("'zen review resume <number>' to open  |  'zen inbox' for new PRs")
	fmt.Println()

	// Features — sorted by age (newest first)
	ui.SectionHeader("Feature Work")
	if len(features) == 0 {
		fmt.Println("  No feature worktrees")
	} else {
		sort.Slice(features, func(i, j int) bool {
			di, _ := worktree.AgeDays(features[i].Path)
			dj, _ := worktree.AgeDays(features[j].Path)
			return di < dj
		})

		fmt.Printf("  %-42s  %-5s  %s\n", "Name", "Age", "Path")
		fmt.Printf("  %-42s  %-5s  %s\n", "──────────────────────────────────────────", "─────", "──────────────────────────────")

		for i, f := range features {
			if i >= 15 {
				fmt.Printf("  ... and %d more\n", len(features)-15)
				break
			}
			age := ""
			if days, err := worktree.AgeDays(f.Path); err == nil && days >= 0 {
				if days == 0 {
					if hours, err := worktree.AgeHours(f.Path); err == nil {
						age = fmt.Sprintf("%dh", hours)
					}
				} else {
					age = fmt.Sprintf("%dd", days)
				}
			}
			fmt.Printf("  %-42s  %-5s  %s\n", f.Name, ui.DimText(age), ui.DimText(ui.ShortenHome(f.Path, home)))
		}
	}
	ui.Hint("'zen work resume <name>' to continue  |  'zen work new <repo> <branch>' to start")
	fmt.Println()

	// Watch daemon
	ui.SectionHeader("Watch Daemon")
	switch daemonStatus {
	case "running":
		fmt.Printf("  Status: %s (PID: %s)\n", ui.GreenText("Running"), daemonPID)
	case "stale":
		fmt.Printf("  Status: %s (daemon not running)\n", ui.YellowText("Stale PID file"))
	default:
		fmt.Printf("  Status: %s\n", ui.DimText("Not running"))
	}
	ui.Hint("'zen watch start/stop' to control  |  'zen watch logs' for logs")
	fmt.Println()

	return nil
}

// enrichPRReviews builds StatusPRReview entries with remote state and cleanup ETA.
// Falls back gracefully if GitHub is unreachable.
func enrichPRReviews(wts []worktree.Worktree, prCache map[string]prcache.PRMeta) []StatusPRReview {
	ctx := context.Background()
	ghClient, _ := github.NewClient(ctx)

	cleanupDays := cfg.Watch.GetCleanupAfterDays()
	reviews := make([]StatusPRReview, 0, len(wts))

	for _, wt := range wts {
		r := StatusPRReview{Worktree: wt}

		// Title from cache
		key := fmt.Sprintf("%s/%d", wt.Repo, wt.PRNumber)
		if meta, ok := prCache[key]; ok && meta.Title != "" {
			r.Title = meta.Title
		}

		// Age
		if days, err := worktree.AgeDays(wt.Path); err == nil && days >= 0 {
			r.AgeDays = days
		}

		// Remote state
		if ghClient != nil && wt.PRNumber > 0 {
			fullRepo := cfg.RepoFullName(wt.Repo)
			if state, err := ghClient.GetPRState(ctx, fullRepo, wt.PRNumber); err == nil {
				r.State = state
				if state == "MERGED" {
					remaining := cleanupDays - r.AgeDays
					if remaining < 0 {
						remaining = 0
					}
					r.CleanupIn = remaining
				}
			}
		}

		reviews = append(reviews, r)
	}
	return reviews
}

// formatPRState returns a colored, pre-padded state string for display.
// Padding is applied before color codes so ANSI escapes don't break alignment.
func formatPRState(state string, cleanupIn int) string {
	padded := fmt.Sprintf("%-8s", state)
	switch state {
	case "OPEN":
		return ui.GreenText(padded)
	case "MERGED":
		return ui.DimText(padded)
	case "CLOSED":
		return ui.YellowText(padded)
	default:
		return padded
	}
}

func getDaemonStatus() (string, string) {
	pidFile := filepath.Join(config.StateDir(), "watch.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return "stopped", ""
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return "stopped", ""
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return "stale", pidStr
	}
	return "running", pidStr
}
