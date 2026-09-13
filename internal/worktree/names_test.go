package worktree

import (
	"path/filepath"
	"testing"
)

func TestPRName(t *testing.T) {
	if got := PRName("mono", 42); got != "mono-pr-42" {
		t.Fatalf("PRName = %q", got)
	}
}

func TestPRPath(t *testing.T) {
	want := filepath.Join("/base", "mono-pr-42")
	if got := PRPath("/base", "mono", 42); got != want {
		t.Fatalf("PRPath = %q want %q", got, want)
	}
}
