package tmux

import (
	"errors"
	"slices"
	"testing"
)

func TestWindowArgs(t *testing.T) {
	got := windowArgs("/home/u/git/repo-mono/mono-fix-x", `claude "review it"`, "/bin/bash")
	want := []string{
		"new-window", "-n", "mono-fix-x", "-c", "/home/u/git/repo-mono/mono-fix-x",
		"--", "/bin/bash", "-l", "-c", `claude "review it"; exec "${SHELL:-/bin/sh}"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("windowArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

func TestOpenWindowOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	if err := OpenWindow(t.TempDir(), "true"); !errors.Is(err, ErrNotInTmux) {
		t.Errorf("OpenWindow() outside tmux = %v, want ErrNotInTmux", err)
	}
}

func TestUserShell(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := userShell(); got != "/bin/sh" {
		t.Errorf("userShell() with SHELL unset = %q, want /bin/sh", got)
	}
	t.Setenv("SHELL", "/usr/bin/zsh")
	if got := userShell(); got != "/usr/bin/zsh" {
		t.Errorf("userShell() = %q, want /usr/bin/zsh", got)
	}
}
