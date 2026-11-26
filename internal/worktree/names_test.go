package worktree

import "testing"

func TestPRName(t *testing.T) {
	if got := PRName("mono", 42); got != "mono-pr-42" {
		t.Fatalf("PRName = %q", got)
	}
}
