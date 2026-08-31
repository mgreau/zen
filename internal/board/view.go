package board

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mgreau/zen/internal/ui"
)

var (
	cyan       = lipgloss.Color("14")
	background = lipgloss.Color("0")
	red        = lipgloss.Color("9")
	green      = lipgloss.Color("10")
	grey       = lipgloss.Color("245")

	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	dimStyle       = lipgloss.NewStyle().Foreground(grey)
	errorStyle     = lipgloss.NewStyle().Foreground(red)
	statusMsgStyle = lipgloss.NewStyle().Foreground(green)
	footerStyle    = lipgloss.NewStyle().Foreground(grey)

	sectionActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	sectionInactiveStyle = lipgloss.NewStyle().Bold(true).Foreground(grey)

	summaryBoxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cyan).Padding(0, 1)
)

func sectionStyle(focused bool) lipgloss.Style {
	if focused {
		return sectionActiveStyle
	}
	return sectionInactiveStyle
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	b.WriteString(titleStyle.Render("zen board"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(m.headerStatus()))
	b.WriteString("\n\n")

	b.WriteString(sectionStyle(m.focus == FocusMyPRs).Render(fmt.Sprintf("MY PULL REQUESTS (%d)", len(m.myRows))))
	b.WriteString("\n")
	b.WriteString(m.myTable.View())
	b.WriteString("\n\n")

	b.WriteString(sectionStyle(m.focus == FocusReview).Render(fmt.Sprintf("NEEDS YOUR REVIEW (%d)", len(m.reviewRows))))
	b.WriteString("\n")
	b.WriteString(m.reviewTable.View())
	b.WriteString("\n\n")

	if m.summaryOpen {
		b.WriteString(m.renderSummaryPanel())
		b.WriteString("\n\n")
	}

	switch {
	case m.err != nil:
		b.WriteString(errorStyle.Render("Error: " + m.err.Error()))
		b.WriteString("\n")
	case m.statusMsg != "":
		b.WriteString(statusMsgStyle.Render(m.statusMsg))
		b.WriteString("\n")
	}

	b.WriteString(footerStyle.Render("↑/↓ move · tab switch table · enter/o open in browser · v review · s summary · r refresh · q quit"))

	return b.String()
}

func (m Model) headerStatus() string {
	if m.loading {
		return "refreshing…"
	}
	if m.lastUpdated.IsZero() {
		return ""
	}
	return fmt.Sprintf("updated %s ago", ui.FormatDuration(int(time.Since(m.lastUpdated).Seconds())))
}

// renderSummaryPanel draws the on-demand PR description panel opened by 's'.
func (m Model) renderSummaryPanel() string {
	if !m.summaryOpen {
		return ""
	}

	var body string
	switch {
	case m.summaryLoading:
		body = dimStyle.Render("Loading description…")
	case m.summaryErr != nil:
		body = errorStyle.Render("Error: " + m.summaryErr.Error())
	case len(m.summaryLines) == 0:
		body = dimStyle.Render("(no description)")
	default:
		body = strings.Join(m.summaryLines, "\n")
	}

	title := titleStyle.Render("Summary " + m.summaryTitle)
	width := m.width - 4
	if width < 20 {
		width = 20
	}
	return summaryBoxStyle.Width(width).Render(title + "\n" + body)
}
