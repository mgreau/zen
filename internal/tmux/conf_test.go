package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithBlock(t *testing.T) {
	block := ConfBlock()
	if got := withBlock("", block); got != block {
		t.Errorf("empty config: %q", got)
	}
	if got, want := withBlock("set -g mouse on", block), "set -g mouse on\n\n"+block; got != want {
		t.Errorf("config without a trailing newline:\n%q\nwant\n%q", got, want)
	}

	// An old block is replaced in place; what surrounds it is kept.
	old := "set -g mouse on\n" + confBegin + "\nset -g old 1\n" + confEnd + "\nbind r source-file ~/.tmux.conf\n"
	want := "set -g mouse on\n" + block + "bind r source-file ~/.tmux.conf\n"
	if got := withBlock(old, block); got != want {
		t.Errorf("replace:\n%q\nwant\n%q", got, want)
	}
	if got := withBlock(want, block); got != want {
		t.Error("replacing an up-to-date block changed the config")
	}
}

func TestInstallConf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux.conf")
	if err := os.WriteFile(path, []byte("set -g mouse on\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := InstallConf(path); err != nil || !changed {
		t.Fatalf("first install = %v, %v; want changed", changed, err)
	}
	if changed, err := InstallConf(path); err != nil || changed {
		t.Fatalf("second install = %v, %v; want unchanged", changed, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "set -g mouse on\n") || strings.Count(string(data), confBegin) != 1 {
		t.Errorf("config after install:\n%s", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", info.Mode().Perm())
	}

	created := filepath.Join(t.TempDir(), "new", "tmux.conf")
	if changed, err := InstallConf(created); err != nil || !changed {
		t.Errorf("install into a missing file = %v, %v", changed, err)
	}
}

func TestConfPath(t *testing.T) {
	home := t.TempDir()
	if got := ConfPath(home); got != filepath.Join(home, ".tmux.conf") {
		t.Errorf("no config: %s", got)
	}
	xdg := filepath.Join(home, ".config", "tmux", "tmux.conf")
	os.MkdirAll(filepath.Dir(xdg), 0o755)
	os.WriteFile(xdg, nil, 0o644)
	if got := ConfPath(home); got != xdg {
		t.Errorf("only XDG config: %s, want %s", got, xdg)
	}
	os.WriteFile(filepath.Join(home, ".tmux.conf"), nil, 0o644)
	if got := ConfPath(home); got != filepath.Join(home, ".tmux.conf") {
		t.Errorf("both configs: %s, want ~/.tmux.conf", got)
	}
}

// The block must be valid tmux config.
func TestConfBlockLoads(t *testing.T) {
	startServer(t)
	path := filepath.Join(t.TempDir(), "tmux.conf")
	if _, err := InstallConf(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Reload(path); err != nil {
		t.Fatalf("tmux rejected the block: %v", err)
	}
	out, _ := tmuxOutput(t, "show-options", "-gv", "focus-events")
	if out != "on" {
		t.Errorf("focus-events after reload = %q, want on", out)
	}
}
