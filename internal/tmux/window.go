// Package tmux opens windows in the tmux session zen runs in. It suits hosts
// reached over ssh, such as a cloud workstation, where there is no terminal
// application to drive.
package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ErrNotInTmux is returned when zen is not running inside a tmux session.
var ErrNotInTmux = errors.New("not running inside tmux: start zen from a tmux session, or pass --no-terminal")

// OpenWindow opens a new window in the current tmux session, named after the
// base name of workDir, and runs command there through the user's login shell.
// When command exits, the window keeps an interactive shell in workDir, so a
// finished or failed agent leaves a prompt instead of closing the window.
func OpenWindow(workDir, command string) error {
	if os.Getenv("TMUX") == "" {
		return ErrNotInTmux
	}
	out, err := exec.Command("tmux", windowArgs(workDir, command, userShell())...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-window: %w: %s", err, out)
	}
	return nil
}

func userShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// windowArgs builds the tmux new-window arguments. tmux runs the shell and its
// arguments directly, so command reaches the shell as one argument and tmux
// never parses it. A login shell loads the same profile as an ssh session.
func windowArgs(workDir, command, shell string) []string {
	return []string{
		"new-window", "-n", filepath.Base(workDir), "-c", workDir,
		"--", shell, "-l", "-c", command + `; exec "${SHELL:-/bin/sh}"`,
	}
}
