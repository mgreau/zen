package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mgreau/zen/internal/board"
	"github.com/spf13/cobra"
)

var boardCmd = &cobra.Command{
	Use:   "board",
	Short: "Live view of your open PRs and pending review requests",
	Long: `Live view of your open PRs and pending review requests.

Runs as an interactive terminal view, refreshing every 30 seconds (or on
demand with 'r'). Shows two tables:

  My Pull Requests   Your open PRs across all configured repos, grouped by
                      status: draft, failing CI, changes requested, ready to
                      merge, in review, in flight. PRs stacked on one of your
                      other open PRs (same repo, base branch = another PR's
                      head branch) are shown together with a |---- marker.
  Needs Your Review  PRs where you're a requested reviewer, across all
                      configured repos — unlike 'zen inbox', not filtered by
                      the 'authors' config. Sorted into three tiers:
                      configured authors, PRs touching a configured
                      watch_paths entry, then everyone else — newest first
                      within each tier.

Keys:
  up/down, j/k   move selection
  tab            switch between tables
  enter, o       open the selected PR in your browser
  v              start/resume a review for the selected PR (Needs Your
                 Review table only)
  s              show/hide the first 20 lines of the selected PR's
                 description (fetched on demand)
  r              refresh now
  q, ctrl+c      quit (esc closes the summary panel first, if open)`,
	RunE: runBoard,
}

func init() {
	rootCmd.AddCommand(boardCmd)
}

func runBoard(cmd *cobra.Command, args []string) error {
	p := tea.NewProgram(board.NewModel(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running board: %w", err)
	}
	return nil
}
