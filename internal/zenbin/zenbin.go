// Package zenbin finds a path to run this zen by from other programs.
package zenbin

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Path returns a path to run this zen by from other programs, such as
// a tmux hook or Claude's hooks, that keeps working across upgrades: the zen
// on PATH when it is this one, since os.Executable resolves symlinks to a
// versioned path (a Homebrew Cellar, the Nix store) that an upgrade removes.
func Path() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if onPath, err := exec.LookPath("zen"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil && sameFile(abs, self) {
			return abs, nil
		}
	}
	return self, nil
}

func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}
