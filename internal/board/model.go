package board

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/github"
)

// refreshInterval is how often the board refetches data on its own.
const refreshInterval = 30 * time.Second

// Focus identifies which of the two tables has keyboard focus.
type Focus int

const (
	FocusMyPRs Focus = iota
	FocusReview
)

// Column sizing. Each column also costs 2 chars of padding (1 per side,
// see table.DefaultStyles) that must be accounted for separately.
const (
	statusColWidthMy     = 20
	statusColWidthReview = 14
	prColWidth           = 8
	repoColWidth         = 12
	authorColWidth       = 14
	ageColWidth          = 5
	colPad               = 2
	minTitleWidth        = 12
	minTableHeight       = 4
	// maxTableHeight caps how tall either table grows for a short list, so a
	// handful of PRs doesn't claim the whole screen when the other table is
	// long.
	maxTableHeight = 15
	// Lines consumed by everything in View() other than the two tables:
	// title/status, blank, 2 section headers, 2 blanks, status/error line,
	// footer.
	chromeLines = 8
)

// Model is the zen board bubbletea model: two live tables — the user's own
// open PRs grouped by status, and PRs they've been asked to review.
type Model struct {
	cfg *config.Config

	myTable     table.Model
	reviewTable table.Model
	myRows      []MyPRRow
	reviewRows  []ReviewRow

	focus       Focus
	loading     bool
	err         error
	statusMsg   string
	lastUpdated time.Time
	width       int
	height      int
	quitting    bool

	summaryOpen    bool
	summaryLoading bool
	summaryLines   []string
	summaryErr     error
	summaryTitle   string
}

// NewModel builds the initial board model for cfg. Data is fetched once
// Init()'s command runs; until then the tables render empty.
func NewModel(cfg *config.Config) Model {
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Foreground(cyan).Bold(true)
	styles.Selected = styles.Selected.Foreground(background).Background(cyan).Bold(true)

	myTable := table.New(
		table.WithColumns(myColumns(defaultWidth)),
		table.WithFocused(true),
		table.WithHeight(defaultTableHeight),
	)
	myTable.SetStyles(styles)

	reviewTable := table.New(
		table.WithColumns(reviewColumns(defaultWidth)),
		table.WithHeight(defaultTableHeight),
	)
	reviewTable.SetStyles(styles)

	m := Model{
		cfg:         cfg,
		myTable:     myTable,
		reviewTable: reviewTable,
		focus:       FocusMyPRs,
		loading:     true,
		width:       defaultWidth,
		height:      defaultHeight,
	}
	m.myTable.SetWidth(defaultWidth)
	m.reviewTable.SetWidth(defaultWidth)
	return m
}

const (
	defaultWidth       = 100
	defaultHeight      = 30
	defaultTableHeight = 8
)

// dataMsg carries the result of a fetch.
type dataMsg struct {
	myRows     []MyPRRow
	reviewRows []ReviewRow
	err        error
}

// tickMsg fires the periodic auto-refresh.
type tickMsg time.Time

// actionResultMsg carries feedback from an 'o'/'v' keypress.
type actionResultMsg struct {
	msg string
	err error
}

func fetchCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var myRows []MyPRRow
		var reviewRows []ReviewRow
		var err1, err2 error

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			myRows, err1 = FetchMyPRs(ctx, cfg)
		}()
		go func() {
			defer wg.Done()
			reviewRows, err2 = FetchReviewRequests(ctx, cfg)
		}()
		wg.Wait()

		err := err1
		if err == nil {
			err = err2
		}
		return dataMsg{myRows: myRows, reviewRows: reviewRows, err: err}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func openCmd(url string) tea.Cmd {
	return func() tea.Msg {
		if url == "" {
			return actionResultMsg{err: fmt.Errorf("selected row has no URL")}
		}
		if err := openURL(url); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: "Opened in browser"}
	}
}

func launchReviewCmd(repo string, number int) tea.Cmd {
	return func() tea.Msg {
		if err := launchReview(repo, number); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Opened review for #%d", number)}
	}
}

// Init starts the first fetch and the auto-refresh ticker.
func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(m.cfg), tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil

	case dataMsg:
		m.loading = false
		m.lastUpdated = time.Now()
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.myRows = msg.myRows
		m.reviewRows = msg.reviewRows
		// Row counts just changed, so table heights (computed from row
		// counts in resize) need recomputing too, not just cell content.
		m.resize()
		return m, nil

	case tickMsg:
		m.loading = true
		return m, tea.Batch(fetchCmd(m.cfg), tickCmd())

	case actionResultMsg:
		if msg.err != nil {
			m.statusMsg = "Error: " + msg.err.Error()
		} else {
			m.statusMsg = msg.msg
		}
		return m, nil

	case summaryMsg:
		m.summaryLoading = false
		m.summaryErr = msg.err
		m.summaryLines = msg.lines
		m.resize()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "esc":
			if m.summaryOpen {
				m.closeSummary()
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "tab":
			m.toggleFocus()
			return m, nil
		case "r":
			m.loading = true
			m.statusMsg = ""
			m.err = nil
			return m, fetchCmd(m.cfg)
		case "enter", "o":
			m.statusMsg = ""
			return m, openCmd(m.selectedURL())
		case "v":
			if m.focus == FocusReview {
				if row, ok := m.selectedReviewRow(); ok {
					m.statusMsg = fmt.Sprintf("Opening review for #%d...", row.Number)
					return m, launchReviewCmd(row.Repo, row.Number)
				}
			}
			return m, nil
		case "s":
			if m.summaryOpen {
				m.closeSummary()
				return m, nil
			}
			repo, number, ok := m.selectedRepoAndNumber()
			if !ok {
				return m, nil
			}
			m.summaryOpen = true
			m.summaryLoading = true
			m.summaryErr = nil
			m.summaryLines = nil
			m.summaryTitle = fmt.Sprintf("#%d", number)
			m.resize()
			return m, summaryCmd(m.cfg.RepoFullName(repo), number)
		}
	}

	var cmd tea.Cmd
	if m.focus == FocusMyPRs {
		m.myTable, cmd = m.myTable.Update(msg)
	} else {
		m.reviewTable, cmd = m.reviewTable.Update(msg)
	}
	return m, cmd
}

func (m *Model) toggleFocus() {
	if m.focus == FocusMyPRs {
		m.focus = FocusReview
		m.myTable.Blur()
		m.reviewTable.Focus()
	} else {
		m.focus = FocusMyPRs
		m.reviewTable.Blur()
		m.myTable.Focus()
	}
}

// closeSummary hides the summary panel and gives its reserved lines back to
// the tables.
func (m *Model) closeSummary() {
	m.summaryOpen = false
	m.summaryLoading = false
	m.summaryErr = nil
	m.summaryLines = nil
	m.resize()
}

// selectedRepoAndNumber returns the short repo name and PR number of
// whichever row is currently selected in the focused table.
func (m Model) selectedRepoAndNumber() (repo string, number int, ok bool) {
	if m.focus == FocusMyPRs {
		if row, ok := m.selectedMyRow(); ok {
			return row.Repo, row.Number, true
		}
		return "", 0, false
	}
	if row, ok := m.selectedReviewRow(); ok {
		return row.Repo, row.Number, true
	}
	return "", 0, false
}

func (m Model) selectedURL() string {
	if m.focus == FocusMyPRs {
		if row, ok := m.selectedMyRow(); ok {
			return row.URL
		}
		return ""
	}
	if row, ok := m.selectedReviewRow(); ok {
		return row.URL
	}
	return ""
}

func (m Model) selectedMyRow() (MyPRRow, bool) {
	i := m.myTable.Cursor()
	if i < 0 || i >= len(m.myRows) {
		return MyPRRow{}, false
	}
	return m.myRows[i], true
}

func (m Model) selectedReviewRow() (ReviewRow, bool) {
	i := m.reviewTable.Cursor()
	if i < 0 || i >= len(m.reviewRows) {
		return ReviewRow{}, false
	}
	return m.reviewRows[i], true
}

// resize recomputes column widths and table heights for the current
// terminal size, then rebuilds table content so truncation matches.
func (m *Model) resize() {
	avail := m.height - chromeLines - m.summaryChromeLines()
	if avail < 2*minTableHeight {
		avail = 2 * minTableHeight
	}
	myHeight, reviewHeight := splitTableHeights(avail, len(m.myRows), len(m.reviewRows))

	m.myTable.SetHeight(myHeight)
	m.reviewTable.SetHeight(reviewHeight)
	m.myTable.SetColumns(myColumns(m.width))
	m.reviewTable.SetColumns(reviewColumns(m.width))
	m.myTable.SetWidth(m.width)
	m.reviewTable.SetWidth(m.width)

	m.refreshTableContent()
}

// summaryChromeLines returns how many lines the summary panel currently
// reserves: 0 when closed, otherwise its border + title + content, plus a
// blank separator line before the footer.
func (m Model) summaryChromeLines() int {
	if !m.summaryOpen {
		return 0
	}
	return m.panelContentLineCount() + 3 + 1 // content + title + 2 border lines + separator
}

// panelContentLineCount is how many lines of actual content the summary
// panel is showing right now (a single status line while loading or on
// error, otherwise the fetched description lines, capped at
// summaryMaxLines by summaryCmd).
func (m Model) panelContentLineCount() int {
	if m.summaryLoading || m.summaryErr != nil || len(m.summaryLines) == 0 {
		return 1
	}
	return len(m.summaryLines)
}

// splitTableHeights allocates avail lines between the two tables based on
// how many rows each actually has, so a short list doesn't reserve dead
// space while a long list is starved for visible rows. Each table gets at
// least minTableHeight and at most maxTableHeight from its own row count;
// any leftover is handed out one line at a time to whichever table still
// has rows hidden below its current allocation, alternating so it's split
// fairly when both are still truncated. If neither has hidden rows, the
// leftover goes unused rather than stretching an already-fully-shown table.
func splitTableHeights(avail, myCount, reviewCount int) (myHeight, reviewHeight int) {
	myWant := clampHeight(myCount)
	reviewWant := clampHeight(reviewCount)

	if myWant+reviewWant > avail {
		total := myCount + reviewCount
		if total == 0 {
			half := avail / 2
			return max(half, minTableHeight), max(avail-half, minTableHeight)
		}
		myHeight = max(avail*myCount/total, minTableHeight)
		reviewHeight = max(avail-myHeight, minTableHeight)
		return myHeight, reviewHeight
	}

	leftover := avail - (myWant + reviewWant)
	myHidden := max(myCount-myWant, 0)
	reviewHidden := max(reviewCount-reviewWant, 0)
	for leftover > 0 && (myHidden > 0 || reviewHidden > 0) {
		if myHidden > 0 {
			myWant++
			myHidden--
			leftover--
		}
		if leftover == 0 {
			break
		}
		if reviewHidden > 0 {
			reviewWant++
			reviewHidden--
			leftover--
		}
	}

	return myWant, reviewWant
}

// clampHeight bounds a row count to [minTableHeight, maxTableHeight].
func clampHeight(rowCount int) int {
	return min(max(rowCount, minTableHeight), maxTableHeight)
}

func myColumns(totalWidth int) []table.Column {
	fixed := (statusColWidthMy + colPad) + (prColWidth + colPad) + (repoColWidth + colPad) + (ageColWidth + colPad)
	title := totalWidth - fixed
	if title < minTitleWidth {
		title = minTitleWidth
	}
	return []table.Column{
		{Title: "STATUS", Width: statusColWidthMy},
		{Title: "PR", Width: prColWidth},
		{Title: "REPO", Width: repoColWidth},
		{Title: "TITLE", Width: title},
		{Title: "AGE", Width: ageColWidth},
	}
}

func reviewColumns(totalWidth int) []table.Column {
	fixed := (statusColWidthReview + colPad) + (prColWidth + colPad) + (repoColWidth + colPad) + (authorColWidth + colPad) + (ageColWidth + colPad)
	title := totalWidth - fixed
	if title < minTitleWidth {
		title = minTitleWidth
	}
	return []table.Column{
		{Title: "STATUS", Width: statusColWidthReview},
		{Title: "PR", Width: prColWidth},
		{Title: "REPO", Width: repoColWidth},
		{Title: "TITLE", Width: title},
		{Title: "AUTHOR", Width: authorColWidth},
		{Title: "AGE", Width: ageColWidth},
	}
}

func (m *Model) refreshTableContent() {
	myTableRows := make([]table.Row, len(m.myRows))
	for i, r := range m.myRows {
		myTableRows[i] = table.Row{
			r.Status.Icon() + " " + r.Status.Label(),
			fmt.Sprintf("#%d", r.Number),
			r.Repo,
			depthPrefix(r.Depth) + r.Title,
			r.Age,
		}
	}
	m.myTable.SetRows(myTableRows)
	fixStuckCursor(&m.myTable, len(myTableRows))

	reviewTableRows := make([]table.Row, len(m.reviewRows))
	for i, r := range m.reviewRows {
		reviewTableRows[i] = table.Row{
			reviewKindLabel(r.Kind),
			fmt.Sprintf("#%d", r.Number),
			r.Repo,
			r.Title,
			r.Author,
			r.Age,
		}
	}
	m.reviewTable.SetRows(reviewTableRows)
	fixStuckCursor(&m.reviewTable, len(reviewTableRows))
}

// fixStuckCursor works around a bubbles/table quirk: SetRows clamps the
// cursor down when it's too high, but never back up when it's too low. The
// first SetRows call after NewModel always has zero rows (data hasn't
// loaded yet), which drives the cursor to -1 — and it silently stays there
// forever once real rows arrive, since -1 is never ">" the new row count.
// A cursor of -1 matches no row, so selection, the "current row" highlight,
// and the 's'/'enter'/'o'/'v' keys would all silently no-op until the user
// happened to press an arrow key first. Once the cursor is at a real row
// (>= 0), this is a no-op — an in-progress selection is never reset.
func fixStuckCursor(t *table.Model, rowCount int) {
	if t.Cursor() < 0 && rowCount > 0 {
		t.SetCursor(0)
	}
}

func reviewKindLabel(kind string) string {
	if kind == github.ReviewKindRereview {
		return "🔁 Re-review"
	}
	return "🆕 New"
}

// depthPrefix renders the tree marker for a My PRs row stacked on another:
// "|---- " for a direct child, indented further per level of nesting.
func depthPrefix(depth int) string {
	if depth == 0 {
		return ""
	}
	return strings.Repeat("     ", depth-1) + "|---- "
}
