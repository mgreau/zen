package worktree

import "testing"

func TestRepoFromRemoteURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:chainguard-dev/mono.git", "chainguard-dev/mono"},
		{"git@github.com:sergiusens/mono.git", "sergiusens/mono"},
		{"https://github.com/chainguard-dev/mono.git", "chainguard-dev/mono"},
		{"https://github.com/chainguard-dev/mono", "chainguard-dev/mono"},
		{"ssh://git@github.com/chainguard-dev/mono.git", "chainguard-dev/mono"},
		{"git@github.com:Chainguard-Dev/Mono.git", "chainguard-dev/mono"},
	}
	for _, tt := range tests {
		if got := repoFromRemoteURL(tt.url); got != tt.want {
			t.Errorf("repoFromRemoteURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
