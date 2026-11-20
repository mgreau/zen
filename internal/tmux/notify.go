package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// Sequence is the escape sequence a desktop notification is sent as.
type Sequence string

const (
	// SequenceAuto picks OSC777 when every terminal attached to the pane's
	// session is Ghostty, which shows a title and a body, and OSC9 otherwise.
	SequenceAuto Sequence = "auto"
	// OSC777 is "ESC ] 777 ; notify ; title ; body BEL", shown by Ghostty.
	OSC777 Sequence = "osc777"
	// OSC9 is "ESC ] 9 ; text BEL", shown by iTerm2, Ghostty, WezTerm and
	// others. It has no separate title, so the title leads the text.
	OSC9 Sequence = "osc9"
)

// Sequences lists the values a notification sequence can be configured as.
func Sequences() []Sequence { return []Sequence{SequenceAuto, OSC777, OSC9} }

// Length limits, in characters: enough to tell notifications apart, short
// enough that macOS shows them whole.
const (
	maxTitle = 100
	maxBody  = 180
)

// Notification is a desktop notification for the terminal tmux runs in.
type Notification struct {
	Title string
	Body  string
}

// Notify sends n from pane (a pane ID such as %3) to the terminals attached
// to the pane's session, through tmux's passthrough (see allowPassthrough),
// so it reaches the computer those terminals run on, across ssh.
//
// It sends nothing when no terminal is attached, or when, with tmux's
// focus-events on, a focused terminal is showing the pane's window: you are
// already looking at it. Without focus-events tmux cannot tell, so it always
// sends.
//
// Title and body are cleaned first (see Clean): they can quote untrusted
// text, such as a model's reply, and must not reach the terminal as escape
// sequences.
func Notify(pane string, n Notification, seq Sequence) error {
	p, err := paneInfo(pane)
	if err != nil {
		return err
	}
	clients, err := sessionClients(p.Session)
	if err != nil {
		return err
	}
	if !shouldSend(p, clients, focusEvents()) {
		return nil
	}
	if seq == SequenceAuto {
		seq = autoSequence(clients)
	}
	return writeTTY(p.TTY, passthrough(encode(seq, Clean(n.Title, maxTitle), Clean(n.Body, maxBody))))
}

// Clean makes s safe to place in an OSC sequence and short enough to read:
// it drops control characters (which could end the sequence or start
// another) and invisible formatting ones (which could disguise the text),
// turns ";" (the OSC field separator) into ",", collapses whitespace, and
// truncates to max characters.
func Clean(s string, max int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			continue
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = b.Len() > 0
			continue
		case unicode.Is(unicode.Cf, r):
			continue
		case r == ';':
			r = ','
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > max {
		out = string([]rune(out)[:max-1]) + "…"
	}
	return out
}

// encode builds the notification sequence. title and body must be clean.
func encode(seq Sequence, title, body string) string {
	if seq == OSC777 {
		return "\x1b]777;notify;" + title + ";" + body + "\a"
	}
	text := body
	if title != "" {
		text = title + ": " + body
	}
	return "\x1b]9;" + text + "\a"
}

// passthrough wraps seq so tmux hands it to the outer terminal as is, rather
// than interpreting it: "ESC P tmux ;", seq with each ESC doubled, "ESC \".
func passthrough(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// pane is what Notify needs to know about the sending pane.
type pane struct {
	TTY     string
	Window  string // window ID
	Session string // session ID
}

// client is a terminal attached to tmux.
type client struct {
	TermType string // such as "ghostty 1.4.0" or "iTerm2 3.5.0"
	Focused  bool
	Window   string // ID of the window it shows
}

func paneInfo(id string) (pane, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", id,
		"#{pane_tty}\t#{window_id}\t#{session_id}").Output()
	if err != nil {
		return pane{}, fmt.Errorf("tmux display-message: %w", err)
	}
	f := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(f) != 3 || f[0] == "" {
		return pane{}, fmt.Errorf("tmux: no pane %s", id)
	}
	return pane{TTY: f[0], Window: f[1], Session: f[2]}, nil
}

// sessionClients lists the terminals attached to session.
func sessionClients(session string) ([]client, error) {
	out, err := exec.Command("tmux", "list-clients", "-t", session,
		"-F", "#{client_termtype}\t#{client_flags}\t#{window_id}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-clients: %w", err)
	}
	return parseClients(string(out)), nil
}

func parseClients(out string) []client {
	var clients []client
	for line := range strings.Lines(out) {
		f := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(f) != 3 {
			continue
		}
		focused := false
		for flag := range strings.SplitSeq(f[1], ",") {
			focused = focused || flag == "focused"
		}
		clients = append(clients, client{TermType: f[0], Focused: focused, Window: f[2]})
	}
	return clients
}

// focusEvents reports whether tmux tracks which terminals have focus.
func focusEvents() bool {
	out, err := exec.Command("tmux", "show-options", "-gv", "focus-events").Output()
	return err == nil && strings.TrimSpace(string(out)) == "on"
}

// shouldSend reports whether anyone would see a notification from p that
// they are not already looking at.
func shouldSend(p pane, clients []client, focusTracked bool) bool {
	if len(clients) == 0 {
		return false
	}
	if !focusTracked {
		return true
	}
	for _, c := range clients {
		if c.Focused && c.Window == p.Window {
			return false
		}
	}
	return true
}

func autoSequence(clients []client) Sequence {
	for _, c := range clients {
		if !strings.HasPrefix(strings.ToLower(c.TermType), "ghostty") {
			return OSC9
		}
	}
	return OSC777
}

// writeTTY writes s to the pane's terminal in one write, as output of the
// program running there, which tmux reads like any other output.
func writeTTY(path, s string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(s)
	return errors.Join(err, f.Close())
}
