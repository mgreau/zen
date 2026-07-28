package worktree

import "testing"

func TestRepoFromRemoteURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:mgreau/zen.git", "mgreau/zen"},
		{"git@github.com:sergiusens/zen.git", "sergiusens/zen"},
		{"https://github.com/mgreau/zen.git", "mgreau/zen"},
		{"https://github.com/mgreau/zen", "mgreau/zen"},
		{"ssh://git@github.com/mgreau/zen.git", "mgreau/zen"},
		{"git@github.com:MGreau/Zen.git", "mgreau/zen"},
	}
	for _, tt := range tests {
		if got := repoFromRemoteURL(tt.url); got != tt.want {
			t.Errorf("repoFromRemoteURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
