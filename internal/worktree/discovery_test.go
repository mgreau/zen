package worktree

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		wantT  Type
		wantPR int
	}{
		{"mono-pr-31640", TypePRReview, 31640},
		{"mono-pr-1", TypePRReview, 1},
		{"os-pr-999", TypePRReview, 999},
		{"mono-feature-branch", TypeFeature, 0},
		{"mono-claude-skills", TypeFeature, 0},
		{"infra-images-pr-500", TypePRReview, 500},
		{"solo", TypeFeature, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotT, gotPR := Classify(tt.name)
			if gotT != tt.wantT {
				t.Errorf("Classify(%q) type = %q, want %q", tt.name, gotT, tt.wantT)
			}
			if gotPR != tt.wantPR {
				t.Errorf("Classify(%q) pr = %d, want %d", tt.name, gotPR, tt.wantPR)
			}
		})
	}
}

func TestParseRepoFromName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"mono-pr-31640", "mono"},
		{"mono-feature-branch", "mono"},
		{"os-pr-100", "os"},
		{"solo", "solo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRepoFromName(tt.name)
			if got != tt.want {
				t.Errorf("ParseRepoFromName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestParseBranchFromName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"mono-pr-31640", "pr-31640"},
		{"mono-feature-branch", "feature-branch"},
		{"mono-claude-skills", "claude-skills"},
		{"solo", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBranchFromName(tt.name)
			if got != tt.want {
				t.Errorf("ParseBranchFromName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
