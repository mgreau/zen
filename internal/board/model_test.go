package board

import "testing"

func TestSplitTableHeights(t *testing.T) {
	tests := []struct {
		name                     string
		avail, myCount, revCount int
		wantMy, wantReview       int
	}{
		{
			name:  "short my list gives leftover to long review list",
			avail: 32, myCount: 10, revCount: 60,
			wantMy: 10, wantReview: 22,
		},
		{
			name:  "both short and fully visible: leftover goes unused",
			avail: 32, myCount: 2, revCount: 3,
			wantMy: 4, wantReview: 4,
		},
		{
			name:  "both long and equally truncated: leftover splits evenly",
			avail: 32, myCount: 100, revCount: 100,
			wantMy: 16, wantReview: 16,
		},
		{
			name:  "both empty: minimum height, no artificial stretch",
			avail: 32, myCount: 0, revCount: 0,
			wantMy: 4, wantReview: 4,
		},
		{
			name:  "wants exceed avail: proportional fallback",
			avail: 10, myCount: 30, revCount: 30,
			wantMy: 5, wantReview: 5,
		},
		{
			name:  "wants exceed avail, skewed counts",
			avail: 12, myCount: 10, revCount: 90,
			wantMy: 4, wantReview: 8,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMy, gotReview := splitTableHeights(tt.avail, tt.myCount, tt.revCount)
			if gotMy != tt.wantMy || gotReview != tt.wantReview {
				t.Errorf("splitTableHeights(%d, %d, %d) = (%d, %d), want (%d, %d)",
					tt.avail, tt.myCount, tt.revCount, gotMy, gotReview, tt.wantMy, tt.wantReview)
			}
			if gotMy+gotReview > tt.avail {
				t.Errorf("splitTableHeights(%d, %d, %d) totals %d, exceeds avail", tt.avail, tt.myCount, tt.revCount, gotMy+gotReview)
			}
		})
	}
}

func TestClampHeight(t *testing.T) {
	tests := []struct {
		rowCount int
		want     int
	}{
		{0, minTableHeight},
		{1, minTableHeight},
		{minTableHeight, minTableHeight},
		{10, 10},
		{maxTableHeight, maxTableHeight},
		{1000, maxTableHeight},
	}
	for _, tt := range tests {
		if got := clampHeight(tt.rowCount); got != tt.want {
			t.Errorf("clampHeight(%d) = %d, want %d", tt.rowCount, got, tt.want)
		}
	}
}

func TestMyRepoColWidth(t *testing.T) {
	tests := []struct {
		name  string
		repos []string
		want  int
	}{
		{"short config names keep the default", []string{"zen", "flox"}, repoColWidth},
		{"an owner/repo label widens the column to fit", []string{"zen", "numtide/llm-agents.nix"}, len("numtide/llm-agents.nix")},
		{"a very long label is capped", []string{"some-organisation/a-rather-long-repository"}, maxRepoColWidth},
		{"no rows keep the default", nil, repoColWidth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := make([]MyPRRow, len(tt.repos))
			for i, r := range tt.repos {
				rows[i].Repo = r
			}
			if got := myRepoColWidth(rows); got != tt.want {
				t.Errorf("myRepoColWidth(%v) = %d, want %d", tt.repos, got, tt.want)
			}
		})
	}
}
