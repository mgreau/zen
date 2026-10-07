package worktree

import (
	"path/filepath"
	"testing"

	"github.com/mgreau/zen/internal/config"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		detached bool
		wantT    Type
		wantPR   int
	}{
		{name: "mono-pr-31640", branch: "pr-31640", wantT: TypePRReview, wantPR: 31640},
		{name: "mono-pr-1", branch: "pr-1", wantT: TypePRReview, wantPR: 1},
		{name: "os-pr-999", branch: "pr-999", wantT: TypePRReview, wantPR: 999},
		{name: "infra-images-pr-500", branch: "pr-500", wantT: TypePRReview, wantPR: 500},
		{name: "mono-pr-123", detached: true, wantT: TypePRReview, wantPR: 123},
		{name: "mono-feature-branch", branch: "mgreau/feature-branch", wantT: TypeFeature},
		{name: "mono-claude-skills", branch: "mgreau/claude-skills", wantT: TypeFeature},
		{name: "solo", wantT: TypeFeature},
		// Work streams whose name ends in -pr-<N> are not reviews of PR N.
		{name: "mono-fix-pr-65446", branch: "mgreau/fix-pr-65446", wantT: TypeFeature},
		{name: "mono-fix-pr-65446", branch: "fix-pr-65446", wantT: TypeFeature},
		{name: "mono-pr-123", branch: "mgreau/pr-123", wantT: TypeFeature},
		// The branch must be the review branch for the same PR.
		{name: "mono-pr-123", branch: "pr-124", wantT: TypeFeature},
		// No branch and not detached means unknown, not a review.
		{name: "mono-pr-123", wantT: TypeFeature},
		{name: "mono-pr-0", branch: "pr-0", wantT: TypeFeature},
	}

	for _, tt := range tests {
		t.Run(tt.name+"@"+tt.branch, func(t *testing.T) {
			gotT, gotPR := Classify(tt.name, tt.branch, tt.detached)
			if gotT != tt.wantT {
				t.Errorf("Classify(%q, %q, %v) type = %q, want %q", tt.name, tt.branch, tt.detached, gotT, tt.wantT)
			}
			if gotPR != tt.wantPR {
				t.Errorf("Classify(%q, %q, %v) pr = %d, want %d", tt.name, tt.branch, tt.detached, gotPR, tt.wantPR)
			}
		})
	}
}

func TestListForRepoClassifiesByBranch(t *testing.T) {
	basePath := t.TempDir()
	origin := filepath.Join(basePath, "mono")
	runRemovalGit(t, basePath, "init", "-b", "main", origin)
	runRemovalGit(t, origin, "config", "user.email", "test@example.com")
	runRemovalGit(t, origin, "config", "user.name", "Test")
	runRemovalGit(t, origin, "config", "commit.gpgsign", "false")
	writeRemovalFile(t, origin, "tracked", "base")
	runRemovalGit(t, origin, "add", "tracked")
	runRemovalGit(t, origin, "commit", "-m", "initial")
	runRemovalGit(t, origin, "worktree", "add", "-b", "pr-123", filepath.Join(basePath, "mono-pr-123"))
	runRemovalGit(t, origin, "worktree", "add", "-b", "mgreau/fix-pr-123", filepath.Join(basePath, "mono-fix-pr-123"))
	runRemovalGit(t, origin, "worktree", "add", "--detach", filepath.Join(basePath, "mono-pr-456"))

	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "example/mono", BasePath: basePath},
	}}
	wts, err := ListForRepo(cfg, "mono")
	if err != nil {
		t.Fatal(err)
	}

	type got struct {
		kind Type
		pr   int
	}
	byName := make(map[string]got)
	for _, w := range wts {
		byName[w.Name] = got{w.Type, w.PRNumber}
	}
	want := map[string]got{
		"mono-pr-123":     {TypePRReview, 123},
		"mono-fix-pr-123": {TypeFeature, 0},
		"mono-pr-456":     {TypePRReview, 456},
	}
	for name, w := range want {
		if g, ok := byName[name]; !ok || g != w {
			t.Errorf("%s = %+v (listed %v), want %+v", name, g, ok, w)
		}
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
