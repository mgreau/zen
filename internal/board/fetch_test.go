package board

import (
	"context"
	"testing"

	"github.com/mgreau/zen/internal/config"
)

func TestTouchesWatchPaths(t *testing.T) {
	tests := []struct {
		name       string
		files      []string
		watchPaths []string
		want       bool
	}{
		{"file under watched dir", []string{"driftlessaf/foo.go"}, []string{"driftlessaf"}, true},
		{"file is exactly the watched name", []string{"driftlessaf"}, []string{"driftlessaf"}, true},
		{"no match", []string{"other/foo.go"}, []string{"driftlessaf"}, false},
		{"one of several files matches", []string{"a/b.go", "driftlessaf/c.go"}, []string{"driftlessaf"}, true},
		{"one of several watch paths matches", []string{"agents/x.go"}, []string{"driftlessaf", "agents"}, true},
		{"no watch paths configured", []string{"driftlessaf/foo.go"}, nil, false},
		{"no files", nil, []string{"driftlessaf"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := touchesWatchPaths(tt.files, tt.watchPaths); got != tt.want {
				t.Errorf("touchesWatchPaths(%v, %v) = %v, want %v", tt.files, tt.watchPaths, got, tt.want)
			}
		})
	}
}

func TestAssignReviewTiers_authorTierNeedsNoNetwork(t *testing.T) {
	// No watch_paths configured, so non-author rows must resolve to
	// reviewTierOther without ever needing a GitHub client.
	cfg := &config.Config{Authors: []string{"alice"}}
	rows := []ReviewRow{
		{Author: "alice", Number: 1},
		{Author: "bob", Number: 2},
	}

	assignReviewTiers(context.Background(), cfg, rows)

	if rows[0].tier != reviewTierAuthor {
		t.Errorf("alice's PR tier = %d, want reviewTierAuthor", rows[0].tier)
	}
	if rows[1].tier != reviewTierOther {
		t.Errorf("bob's PR tier = %d, want reviewTierOther", rows[1].tier)
	}
}

func TestAssignReviewTiers_emptyRows(t *testing.T) {
	cfg := &config.Config{Authors: []string{"alice"}, WatchPaths: []string{"driftlessaf"}}
	// Must not panic or hang when there's nothing to tier.
	assignReviewTiers(context.Background(), cfg, nil)
}
