package tmux

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The markers around the settings zen manages in the user's tmux config.
const (
	confBegin = "# >>> zen >>> managed by `zen setup tmux`: edits between these lines are replaced"
	confEnd   = "# <<< zen <<<"
)

// confSettings are the tmux settings zen's tmux support relies on.
var confSettings = []string{
	"# Track which terminals have focus, so zen doesn't notify you about the",
	"# window you are looking at.",
	"set -g focus-events on",
	"# Mark windows whose agent is waiting on you: a reverse \"● \" in front of",
	"# your window-status-format, as set when this block is read. -F expands",
	"# #{window-status-format} to it now, and ## keeps zen's part for later;",
	"# %if adds it only once.",
	`%if "#{?#{m:*@zen-status*,#{window-status-format}},0,1}"`,
	`set -gF window-status-format '##{?##{==:##{@zen-status},waiting},#[reverse]● #[noreverse],}#{window-status-format}'`,
	"%endif",
	"# prefix + W: pick from the windows waiting on you.",
	`bind W choose-tree -Zw -f '#{==:#{@zen-status},waiting}'`,
}

// ConfBlock returns the block of settings zen keeps in the tmux config.
func ConfBlock() string {
	return confBegin + "\n" + strings.Join(confSettings, "\n") + "\n" + confEnd + "\n"
}

// ConfPath returns the tmux config file to edit: the first that exists of
// those tmux reads, in its order (~/.tmux.conf, $XDG_CONFIG_HOME/tmux/tmux.conf,
// ~/.config/tmux/tmux.conf), else ~/.tmux.conf. Recent tmux reads every one
// of them that exists, and older tmux fewer, but all read the first: zen's
// block goes where the user's settings are, and works on any version.
func ConfPath(home string) string {
	dot := filepath.Join(home, ".tmux.conf")
	candidates := []string{dot}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		candidates = append(candidates, filepath.Join(xdg, "tmux", "tmux.conf"))
	}
	candidates = append(candidates, filepath.Join(home, ".config", "tmux", "tmux.conf"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return dot
}

// InstallConf writes zen's block into the tmux config at path, creating the
// file if needed. An existing zen block is replaced, so running it again
// updates the settings; the rest of the file is left as it is. It reports
// whether the file changed.
func InstallConf(path string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	updated := withBlock(string(old), ConfBlock())
	if updated == string(old) {
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
