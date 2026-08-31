package board

import (
	"testing"

	"github.com/charmbracelet/bubbles/table"
)

func TestFixStuckCursor_recoversFromStuckNegativeOne(t *testing.T) {
	tbl := table.New(table.WithColumns([]table.Column{{Title: "X", Width: 5}}))

	// Mirrors what actually happens on a fresh board: the table is first
	// populated with zero rows (data hasn't loaded yet), which drives
	// bubbles/table's cursor to -1 and leaves it stuck there.
	tbl.SetRows(nil)
	if got := tbl.Cursor(); got != -1 {
		t.Fatalf("precondition failed: cursor = %d, want -1 after SetRows(nil)", got)
	}

	tbl.SetRows([]table.Row{{"a"}, {"b"}, {"c"}})
	fixStuckCursor(&tbl, 3)

	if got := tbl.Cursor(); got != 0 {
		t.Errorf("cursor = %d, want 0 after recovering from stuck -1", got)
	}
}

func TestFixStuckCursor_doesNotResetAnInProgressSelection(t *testing.T) {
	tbl := table.New(table.WithColumns([]table.Column{{Title: "X", Width: 5}}))
	tbl.SetRows([]table.Row{{"a"}, {"b"}, {"c"}})
	tbl.SetCursor(2)

	fixStuckCursor(&tbl, 3)

	if got := tbl.Cursor(); got != 2 {
		t.Errorf("cursor = %d, want unchanged 2 (user had already selected a row)", got)
	}
}

func TestFixStuckCursor_noRowsStaysAtMinusOne(t *testing.T) {
	tbl := table.New(table.WithColumns([]table.Column{{Title: "X", Width: 5}}))
	tbl.SetRows(nil)

	fixStuckCursor(&tbl, 0)

	if got := tbl.Cursor(); got != -1 {
		t.Errorf("cursor = %d, want -1 (no rows to select)", got)
	}
}
