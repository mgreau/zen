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
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/prcache"
	"github.com/mgreau/zen/internal/reconciler"
	"github.com/mgreau/zen/internal/session"
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
	Features     []StatusFeature  `json:"features"`
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

// StatusFeature enriches a feature worktree with session and age info.
type StatusFeature struct {
	worktree.Worktree
	AgeDays       int    `json:"age_days"`
	AgeStr        string `json:"age_str"`
	HasSession    bool   `json:"has_session"`
	Running       bool   `json:"running"`
	SessionStatus string `json:"session_status,omitempty"` // "running", "waiting", "stopped", or ""
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

	// Enrich features with session and age info
	enrichedFeatures := enrichFeatures(features)

	// Daemon status
	daemonStatus, daemonPID := getDaemonStatus()

	if jsonFlag {
		printJSON(StatusData{
			Worktrees:    wtStats,
			PRReviews:    prReviews,
			Features:     enrichedFeatures,
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
	if len(enrichedFeatures) == 0 {
		fmt.Println("  No feature worktrees")
	} else {
		sort.Slice(enrichedFeatures, func(i, j int) bool {
			return enrichedFeatures[i].AgeDays < enrichedFeatures[j].AgeDays
		})

		fmt.Printf("  %-3s  %-34s  %-22s  %-5s  %s\n", "", "Name", "Branch", "Age", "Path")
		fmt.Printf("  %-3s  %-34s  %-22s  %-5s  %s\n", "───", "──────────────────────────────────", "──────────────────────", "─────", "──────────────────────────────")

		for i, f := range enrichedFeatures {
			if i >= 15 {
				fmt.Printf("  ... and %d more\n", len(enrichedFeatures)-15)
				break
			}
			sessionIcon := "   "
			switch f.SessionStatus {
			case "running":
				sessionIcon = ui.GreenText(" ● ")
			case "waiting":
				sessionIcon = ui.YellowText(" ● ")
			default:
				if f.HasSession {
					sessionIcon = ui.DimText(" ○ ")
				}
			}
			branch := ui.Truncate(f.Branch, 22)
			name := ui.Truncate(f.Name, 34)
			fmt.Printf("  %s  %-34s  %s  %-5s  %s\n",
				sessionIcon,
				name,
				ui.CyanText(fmt.Sprintf("%-22s", branch)),
				ui.DimText(f.AgeStr),
				ui.DimText(ui.ShortenHome(f.Path, home)))
		}
	}
	ui.Hint("'zen work resume <name>' to continue  |  'zen work new <repo> <branch>' to start  |  " + ui.GreenText("●") + " running  " + ui.YellowText("●") + " waiting")
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

// enrichFeatures builds StatusFeature entries with age and session info.
// Uses cached session snapshot when available and fresh (< 60s), falls back
// to real-time scanning otherwise.
func enrichFeatures(wts []worktree.Worktree) []StatusFeature {
	// Try to use cached session data
	sessionMap := make(map[string]reconciler.SessionState)
	snapshot, _ := reconciler.ReadSessionSnapshot()
	if reconciler.IsSnapshotFresh(snapshot, 60*time.Second) {
		for _, s := range snapshot.Sessions {
			sessionMap[s.WorktreePath] = s
		}
	}

	features := make([]StatusFeature, 0, len(wts))
	for _, wt := range wts {
		f := StatusFeature{Worktree: wt}

		// Age
		if days, err := worktree.AgeDays(wt.Path); err == nil && days >= 0 {
			f.AgeDays = days
			if days == 0 {
				if hours, err := worktree.AgeHours(wt.Path); err == nil {
					f.AgeStr = fmt.Sprintf("%dh", hours)
				}
			} else {
				f.AgeStr = fmt.Sprintf("%dd", days)
			}
		}

		// Session status — prefer cache, fall back to real-time
		if cached, ok := sessionMap[wt.Path]; ok {
			f.HasSession = true
			f.Running = cached.Status == "running" || cached.Status == "waiting"
			f.SessionStatus = cached.Status
		} else if len(sessionMap) == 0 {
			// No cache available — fall back to real-time scanning
			sessions, _ := session.FindSessions(wt.Path)
			if len(sessions) > 0 {
				f.HasSession = true
				f.Running = session.IsProcessRunning(sessions[0].ID)
				if f.Running {
					f.SessionStatus = "running"
				} else {
					f.SessionStatus = "stopped"
				}
			}
		}

		features = append(features, f)
	}
	return features
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
