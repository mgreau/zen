package tmux

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// zen keeps its tmux settings in a file of its own (ZenConf), which it
// rewrites freely, and adds one line to the user's tmux config to read it,
// between these markers.
const (
	confBegin = "# >>> zen >>> managed by `zen setup tmux`: edits between these lines are replaced"
	confEnd   = "# <<< zen <<<"
)

// sessionHookIndex is the index of zen's session-created hook. A fixed index
// means sourcing the file again replaces the hook rather than adding another,
// and leaves the user's own hooks, which tmux numbers from 0, alone.
const sessionHookIndex = 100

// ZenConf returns zen's tmux settings, which run zenBin from a hook.
func ZenConf(zenBin string) (string, error) {
	if err := tmuxQuotable(zenBin); err != nil {
		return "", err
	}
	return strings.Join([]string{
		"# Managed by `zen setup tmux`, which rewrites this file: change",
		"# ~/.zen/config.yaml instead, or put your own settings in your tmux config.",
		"",
		"# Track which terminals have focus, so zen doesn't notify you about the",
		"# window you are looking at.",
		"set -g focus-events on",
		"",
		"# Mark windows whose agent is waiting on you: tmux's default format, with",
		"# a reverse \"● \" in front.",
		`set -g window-status-format '#{?#{==:#{@zen-status},waiting},#[reverse]● ,}#I:#W#{?window_flags,#{window_flags}, }#[default]'`,
		"",
		"# prefix + W: pick from the windows waiting on you.",
		`bind W choose-tree -Zw -f '#{==:#{@zen-status},waiting}'`,
		"",
		"# Set up each new session as config.yaml's tmux section says: zen inbox in",
		"# its first window, an Agents window.",
		fmt.Sprintf(`set-hook -g 'session-created[%d]' { run-shell -b "'%s' hook session-created '#{session_id}'" }`, sessionHookIndex, zenBin),
		"",
	}, "\n"), nil
}

// ConfBlock returns the block zen keeps in the user's tmux config: a line
// reading zen's own settings from zenConf. -q keeps tmux quiet if the file
// goes missing, such as after zen is uninstalled.
func ConfBlock(zenConf string) (string, error) {
	if err := tmuxQuotable(zenConf); err != nil {
		return "", err
	}
	return confBegin + "\n" + fmt.Sprintf("source-file -q '%s'", zenConf) + "\n" + confEnd + "\n", nil
}

// tmuxQuotable checks that path can go between single quotes in tmux config,
// and between the shell's single quotes inside run-shell's double quotes:
// tmux has no escape inside single quotes, and expands "$" and "~" inside
// double ones.
func tmuxQuotable(path string) error {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "'\"$`\\\n#{}") {
		return fmt.Errorf("%q cannot be quoted in tmux config: use a plain absolute path", path)
	}
	return nil
}

// ConfPath returns the tmux config file to edit: ~/.tmux.conf, unless only
// the XDG location, ~/.config/tmux/tmux.conf, exists.
func ConfPath(home string) string {
	dot := filepath.Join(home, ".tmux.conf")
	xdg := filepath.Join(home, ".config", "tmux", "tmux.conf")
	if _, err := os.Stat(dot); err != nil {
		if _, err := os.Stat(xdg); err == nil {
			return xdg
		}
	}
	return dot
}

// InstallConf writes zen's settings to zenConf, then the block that reads
// them into the user's tmux config at path, creating either file if needed.
// An existing zen block is replaced, so running it again updates it; the
// rest of the user's file is left as it is. It reports whether either file
// changed.
func InstallConf(path, zenConf, zenBin string) (bool, error) {
	settings, err := ZenConf(zenBin)
	if err != nil {
		return false, err
	}
	block, err := ConfBlock(zenConf)
	if err != nil {
		return false, err
	}
	zenChanged, err := writeIfChanged(zenConf, settings)
	if err != nil {
		return false, err
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	userChanged, err := writeIfChanged(path, withBlock(string(old), block))
	return zenChanged || userChanged, err
}

// writeIfChanged writes content to path unless it already holds it, keeping
// the file's mode, and reports whether it wrote.
func writeIfChanged(path, content string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	updated := content
	if err == nil && string(old) == updated {
		return false, nil
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(updated), mode)
}

// withBlock returns conf with block in place of its zen block, or appended
// when it has none.
func withBlock(conf, block string) string {
	begin := strings.Index(conf, confBegin)
	if begin >= 0 {
		if rel := strings.Index(conf[begin:], confEnd); rel >= 0 {
			end := begin + rel + len(confEnd)
			if end < len(conf) && conf[end] == '\n' {
				end++
			}
			return conf[:begin] + block + conf[end:]
		}
	}
	if conf == "" {
		return block
	}
	if !strings.HasSuffix(conf, "\n") {
		conf += "\n"
	}
	return conf + "\n" + block
}

// Reload makes the running tmux server read the config at path, if zen runs
// inside tmux. It reports whether it did.
func Reload(path string) (bool, error) {
	if os.Getenv("TMUX") == "" {
		return false, nil
	}
	return true, run("source-file", path)
}

// Installed reports whether tmux is on PATH.
func Installed() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}
