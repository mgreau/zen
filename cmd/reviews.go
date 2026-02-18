package cmd

import (
	"fmt"

	"github.com/mgreau/zen/internal/prcache"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var reviewsCmd = &cobra.Command{
	Use:   "reviews",
	Short: "Show PR reviews from past N days",
	RunE:  runReviews,
}

var reviewsDays int

func init() {
	reviewsCmd.Flags().IntVarP(&reviewsDays, "days", "d", 7, "Show reviews from past N days")
	rootCmd.AddCommand(reviewsCmd)
}

// ReviewEntry holds enriched review data for JSON output.
type ReviewEntry struct {
	worktree.Worktree
	Title      string `json:"title,omitempty"`
	HasSession bool   `json:"has_active_session"`
}

func runReviews(cmd *cobra.Command, args []string) error {
	wts, err := worktree.ListAll(cfg)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	// Filter to PR reviews within age limit
	var reviews []worktree.Worktree
	for _, wt := range wts {
		if wt.Type != worktree.TypePRReview {
			continue
		}
		if reviewsDays > 0 {
			age, err := worktree.AgeDays(wt.Path)
			if err != nil || age > reviewsDays {
				continue
			}
		}
		reviews = append(reviews, wt)
	}

	prCache := prcache.Load()

	if jsonFlag {
		var entries []ReviewEntry
		for _, r := range reviews {
			key := fmt.Sprintf("%s/%d", r.Repo, r.PRNumber)
			title := ""
			if meta, ok := prCache[key]; ok {
				title = meta.Title
			}
			entries = append(entries, ReviewEntry{
				Worktree:   r,
				Title:      title,
				HasSession: session.HasActiveSession(r.Path),
			})
		}
		printJSON(entries)
		return nil
	}

	// Human-readable output
	fmt.Println()
	fmt.Println(ui.BoldText(fmt.Sprintf("PR Reviews (past %d days)", reviewsDays)))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	if len(reviews) == 0 {
		fmt.Println("No PR review worktrees found in the past", reviewsDays, "days.")
		return nil
	}

	fmt.Printf("%-8s %-12s %-45s %s\n", "PR#", "Repo", "Title", "Session")
	fmt.Printf("%-8s %-12s %-45s %s\n", "────────", "────────────", "─────────────────────────────────────────────", "───────")

	home := homeDir()
	for _, r := range reviews {
		key := fmt.Sprintf("%s/%d", r.Repo, r.PRNumber)
		title := ""
		if meta, ok := prCache[key]; ok {
			title = meta.Title
		}

		sessionIndicator := ""
		if session.HasActiveSession(r.Path) {
			sessionIndicator = ui.GreenText("●")
		}

		shortTitle := ui.Truncate(title, 43)
		if shortTitle == "" {
			shortTitle = r.Name
		}

		fmt.Printf("%-8s %-12s %-45s %s\n", fmt.Sprintf("#%d", r.PRNumber), r.Repo, shortTitle, sessionIndicator)
		fmt.Printf("         %s\n", ui.DimText(ui.ShortenHome(r.Path, home)))
	}

	fmt.Println()
	ui.Hint("● = Active Claude session")
	fmt.Println()
	return nil
}
