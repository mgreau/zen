package tmux

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		"# Mark windows whose agent is waiting on you: a reverse \"● \" in front of",
		"# your window-status-format, as set when this file is read. -F expands",
		"# #{window-status-format} to it now, and ## keeps zen's part for later;",
		"# %if adds it only once.",
		`%if "#{?#{m:*@zen-status*,#{window-status-format}},0,1}"`,
		`set -gF window-status-format '##{?##{==:##{@zen-status},waiting},#[reverse]● #[noreverse],}#{window-status-format}'`,
		"%endif",
		"",
		"# prefix + W: pick from the windows waiting on you.",
		`bind W choose-tree -Zw -f '#{==:#{@zen-status},waiting}'`,
		"",
		"# Set up each new session as config.yaml's tmux section says: zen inbox in",
		"# its first window, an Agents window.",
		"# Its output goes nowhere: tmux would show it in the new session's pane.",
		"# zen logs problems to ~/.zen/state/tmux-hook.log instead.",
		fmt.Sprintf(`set-hook -g 'session-created[%d]' { run-shell -b "'%s' hook session-created '#{session_id}' >/dev/null 2>&1" }`, sessionHookIndex, zenBin),
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
		return fmt.Errorf("%w: %q: use a plain absolute path", ErrUnquotable, path)
	}
	return nil
}

// ErrUnquotable is a path that cannot be written into tmux config: relative,
// or with a character tmux or the shell would read specially.
var ErrUnquotable = errors.New("path cannot be used in tmux config")

// ConfPath returns the tmux config file to edit: the first that exists of
// those tmux reads, in its order (~/.tmux.conf, $XDG_CONFIG_HOME/tmux/tmux.conf,
// ~/.config/tmux/tmux.conf), else ~/.tmux.conf. Recent tmux reads every one
// of them that exists, and older tmux fewer, but all read the first: zen's
// line goes where the user's settings are, and works on any version.
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
		return zenChanged, &UserConfError{Path: path, Err: err}
	}
	userChanged, err := writeIfChanged(path, withBlock(string(old), block))
	if err != nil {
		return zenChanged, &UserConfError{Path: path, Err: err}
	}
	return zenChanged || userChanged, nil
}

// UserConfError is InstallConf failing on the user's tmux config after
// writing zen's own settings, which are then up to date: adding the
// source-file line by hand is enough.
type UserConfError struct {
	Path string
	Err  error
}

func (e *UserConfError) Error() string { return fmt.Sprintf("updating %s: %v", e.Path, e.Err) }
func (e *UserConfError) Unwrap() error { return e.Err }

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
		var end int
		rel := strings.Index(conf[begin:], confEnd)
		// Another begin marker before the end means this one lost its end:
		// pairing it with the later block's end would delete the user's
		// lines between them. Drop the orphan, then handle what follows.
		if next := strings.Index(conf[begin+len(confBegin):], confBegin); next >= 0 && (rel < 0 || begin+len(confBegin)+next < begin+rel) {
			return withBlock(dropOrphanBegin(conf, begin, block), block)
		}
		if rel >= 0 {
			end = begin + rel + len(confEnd)
		} else {
			// The end marker is gone, as after a hand edit: replace the
			// begin marker, and a source-file line of zen's that follows it
			// (for this zen settings file or an older one), so the block
			// isn't added again below. Any other line is the user's.
			end = lineEnd(conf, begin)
			if next := lineEnd(conf, end+1); end+1 <= len(conf) && isZenSourceLine(conf[end+1:next], block) {
				end = next
			}
		}
		if end < len(conf) && conf[end] == '\n' {
			end++
		}
		return conf[:begin] + block + conf[end:]
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

// dropOrphanBegin removes the begin marker at begin, which has no end
// marker, and a zen source-file line right after it.
func dropOrphanBegin(conf string, begin int, block string) string {
	end := lineEnd(conf, begin)
	if next := lineEnd(conf, end+1); end+1 <= len(conf) && isZenSourceLine(conf[end+1:next], block) {
		end = next
	}
	if end < len(conf) {
		end++ // the newline
	}
	return conf[:begin] + conf[end:]
}

// isZenSourceLine reports whether line is one ConfBlock wrote: the one in
// block, or one for zen's settings file in the default ZEN_HOME, ~/.zen,
// from before it moved. A user's own source-file line is never matched.
func isZenSourceLine(line, block string) bool {
	if lines := strings.Split(block, "\n"); len(lines) > 1 && line == lines[1] {
		return true
	}
	return zenSourceLine.MatchString(line)
}

var zenSourceLine = regexp.MustCompile(`^source-file -q '/[^']*/\.zen/tmux\.conf'$`)

// lineEnd returns the index of the newline ending the line at i, or the
// length of s when that line is the last.
func lineEnd(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
		return i + n
	}
	return len(s)
}
