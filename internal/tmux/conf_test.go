package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithBlock(t *testing.T) {
	block, err := ConfBlock("/home/u/.zen/tmux.conf")
	if err != nil {
		t.Fatal(err)
	}
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

func TestTmuxQuotable(t *testing.T) {
	for _, ok := range []string{"/home/u/.local/bin/zen", "/opt/zen 2/zen", "/nix/store/abc-zen-1.0/bin/zen"} {
		if err := tmuxQuotable(ok); err != nil {
			t.Errorf("tmuxQuotable(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"zen", "/a'b/zen", `/a"b`, "/$HOME/zen", "/a#{b}", "/a\nb", "/a\\b"} {
		if err := tmuxQuotable(bad); err == nil {
			t.Errorf("tmuxQuotable(%q) = nil, want an error", bad)
		}
	}
	if _, err := ZenConf("/a'b/zen"); err == nil {
		t.Error("ZenConf took a path it cannot quote")
	}
}

func TestInstallConf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux.conf")
	zenConf := filepath.Join(dir, "zen", "tmux.conf")
	if err := os.WriteFile(path, []byte("set -g mouse on\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := InstallConf(path, zenConf, "/usr/bin/zen"); err != nil || !changed {
		t.Fatalf("first install = %v, %v; want changed", changed, err)
	}
	if changed, err := InstallConf(path, zenConf, "/usr/bin/zen"); err != nil || changed {
		t.Fatalf("second install = %v, %v; want unchanged", changed, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "set -g mouse on\n") || strings.Count(string(data), confBegin) != 1 ||
		!strings.Contains(string(data), "source-file -q '"+zenConf+"'") {
		t.Errorf("user config after install:\n%s", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", info.Mode().Perm())
	}
	settings, _ := os.ReadFile(zenConf)
	if !strings.Contains(string(settings), `'/usr/bin/zen' hook session-created`) {
		t.Errorf("zen settings:\n%s", settings)
	}

	// A new zen path rewrites zen's file only.
	if changed, err := InstallConf(path, zenConf, "/opt/zen/zen"); err != nil || !changed {
		t.Errorf("install with a new zen path = %v, %v; want changed", changed, err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(data) {
		t.Error("a new zen path changed the user's config")
	}
}

func TestConfPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
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

// What zen writes must be valid tmux config, and run its hook for each new
// session, the first included.
func TestConfLoads(t *testing.T) {
	startServer(t)
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	fakeZen := filepath.Join(dir, "zen")
	if err := os.WriteFile(fakeZen, []byte("#!/bin/sh\necho \"$*\" >> "+rec+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tmux.conf")
	if _, err := InstallConf(path, filepath.Join(dir, "zen.conf"), fakeZen); err != nil {
		t.Fatal(err)
	}
	// Twice: a second source must replace zen's hook, not add another.
	for range 2 {
		if _, err := Reload(path); err != nil {
			t.Fatalf("tmux rejected the settings: %v", err)
		}
	}
	if out, _ := tmuxOutput(t, "show-options", "-gv", "focus-events"); out != "on" {
		t.Errorf("focus-events after reload = %q, want on", out)
	}
	if out, _ := tmuxOutput(t, "show-hooks", "-g", "session-created"); strings.Count(out, "hook session-created") != 1 {
		t.Errorf("session-created hooks:\n%s\nwant zen's once", out)
	}

	tmuxCmd(t, "new-session", "-d", "-s", "later")
	waitFor(t, "the hook", func() bool {
		data, _ := os.ReadFile(rec)
		return strings.HasPrefix(string(data), "hook session-created $")
	})
}

func TestWithBlockMissingEnd(t *testing.T) {
	block, _ := ConfBlock("/home/u/.zen/tmux.conf")
	// zen's own line goes with the marker; a user's line after it stays.
	lines := strings.Split(block, "\n")
	broken := "set -g mouse on\n" + confBegin + "\n" + lines[1] + "\nsource-file -q ~/.tmux/theme.conf\n"
	want := "set -g mouse on\n" + block + "source-file -q ~/.tmux/theme.conf\n"
	if got := withBlock(broken, block); got != want {
		t.Errorf("block without its end marker:\n%q\nwant\n%q", got, want)
	}
	// An older zen line, for a settings file that moved, goes too.
	stale := "set -g mouse on\n" + confBegin + "\nsource-file -q '/home/old/.zen/tmux.conf'\n"
	if got := withBlock(stale, block); got != "set -g mouse on\n"+block {
		t.Errorf("stale zen line kept:\n%q", got)
	}
	dotfiles := "set -g mouse on\n" + confBegin + "\nsource-file -q '/home/u/dotfiles/tmux/tmux.conf'\n"
	if got := withBlock(dotfiles, block); !strings.Contains(got, "dotfiles/tmux/tmux.conf") {
		t.Errorf("repair dropped the user's own tmux.conf line:\n%q", got)
	}
	userLine := "set -g mouse on\n" + confBegin + "\nsource-file -q ~/.tmux/theme.conf\n"
	if got := withBlock(userLine, block); !strings.Contains(got, "theme.conf") {
		t.Errorf("repair dropped the user's line:\n%q", got)
	}
}

// An orphan begin marker before a whole block must not be paired with that
// block's end, which would delete the user's lines between them.
func TestWithBlockOrphanBeforeBlock(t *testing.T) {
	block, _ := ConfBlock("/home/u/.zen/tmux.conf")
	conf := confBegin + "\nset -g mouse on\nbind r source-file ~/.tmux.conf\n\n" + block
	got := withBlock(conf, block)
	if want := "set -g mouse on\nbind r source-file ~/.tmux.conf\n\n" + block; got != want {
		t.Errorf("withBlock:\n%q\nwant\n%q", got, want)
	}
}

func TestInstallConfUserError(t *testing.T) {
	dir := t.TempDir()
	zenConf := filepath.Join(dir, "zen.conf")
	readOnly := filepath.Join(dir, "ro")
	if err := os.Mkdir(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	_, err := InstallConf(filepath.Join(readOnly, "tmux.conf"), zenConf, "/usr/bin/zen")
	var userErr *UserConfError
	if !errors.As(err, &userErr) {
		t.Fatalf("InstallConf into a read-only directory = %v, want a UserConfError", err)
	}
	if _, err := os.Stat(zenConf); err != nil {
		t.Errorf("zen's own settings were not written first: %v", err)
	}
}

// zen's marker goes in front of the user's own window format, once.
func TestConfKeepsUserFormat(t *testing.T) {
	startServer(t)
	tmuxCmd(t, "set-option", "-g", "window-status-format", "#[fg=green]#I|#W")
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux.conf")
	if _, err := InstallConf(path, filepath.Join(dir, "zen.conf"), "/usr/bin/zen"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := Reload(path); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := tmuxOutput(t, "show-options", "-gv", "window-status-format")
	if want := "#{?#{==:#{@zen-status},waiting},#[reverse]● #[noreverse],}#[fg=green]#I|#W"; got != want {
		t.Errorf("window-status-format = %q, want %q", got, want)
	}
}

// tmux reads $XDG_CONFIG_HOME/tmux/tmux.conf before ~/.config/tmux/tmux.conf,
// and only the first it finds: zen must edit that one, not create another.
func TestConfPathXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdgHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	conf := filepath.Join(xdgHome, "tmux", "tmux.conf")
	os.MkdirAll(filepath.Dir(conf), 0o755)
	os.WriteFile(conf, nil, 0o644)
	if got := ConfPath(home); got != conf {
		t.Errorf("ConfPath = %s, want %s", got, conf)
	}
}
