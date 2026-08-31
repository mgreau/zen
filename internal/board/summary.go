package board

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mgreau/zen/internal/github"
)

// summaryMaxLines is how many lines of a PR's description the 's' panel shows.
const summaryMaxLines = 20

// summaryMsg carries the result of an on-demand PR body fetch.
type summaryMsg struct {
	lines []string
	err   error
}

// summaryCmd fetches a single PR's description on demand — only called when
// the user presses 's', not as part of the normal 30s refresh, so the
// regular list fetch never pays for every PR's full body.
func summaryCmd(fullRepo string, number int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		client, err := github.NewClient(ctx)
		if err != nil {
			return summaryMsg{err: err}
		}
		details, err := client.GetPRDetails(ctx, fullRepo, number)
		if err != nil {
			return summaryMsg{err: err}
		}
		return summaryMsg{lines: truncateBodyLines(details.Body, summaryMaxLines)}
	}
}

// truncateBodyLines splits body into lines and caps it at max, normalizing
// CRLF line endings first.
func truncateBodyLines(body string, max int) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if body == "" {
		return nil
	}
	lines := strings.Split(body, "\n")
	if len(lines) > max {
		lines = lines[:max]
	}
	return lines
}
