package tmux

import (
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "Claude needs your permission to use Bash", "Claude needs your permission to use Bash"},
		{"whitespace", "  line one\n\n\tline two  ", "line one line two"},
		{"osc injection", "done\x1b]52;c;ZXZpbA==\x07 \x1b[31mred", "done ]52,c,ZXZpbA== [31mred"},
		{"c1 controls", "a\u009b31mb\u009dc", "a 31mb c"},
		{"separator", "a;b", "a,b"},
		{"bidi override", "safe‮gnp.exe", "safegnp.exe"},
		{"unicode", "Résumé ✓ 日本", "Résumé ✓ 日本"},
		{"invalid utf8", "ok\xff\xfeok", "okok"},
	}
	for _, tt := range tests {
		if got := Clean(tt.in, 100); got != tt.want {
			t.Errorf("%s: Clean(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
	if got := Clean(strings.Repeat("é", 300), 10); got != strings.Repeat("é", 9)+"…" {
		t.Errorf("Clean() truncation = %q", got)
	}
}

func TestEncode(t *testing.T) {
	if got, want := encode(OSC777, "Claude · x", "hi"), "\x1b]777;notify;Claude · x;hi\a"; got != want {
		t.Errorf("osc777 = %q, want %q", got, want)
	}
	if got, want := encode(OSC9, "Claude · x", "hi"), "\x1b]9;Claude · x: hi\a"; got != want {
		t.Errorf("osc9 = %q, want %q", got, want)
	}
	if got, want := passthrough("\x1b]9;hi\a"), "\x1bPtmux;\x1b\x1b]9;hi\a\x1b\\"; got != want {
		t.Errorf("passthrough = %q, want %q", got, want)
	}
}

func TestClients(t *testing.T) {
	clients := parseClients("ghostty 1.4.0\tattached,focused,UTF-8\t@1\n" +
		"iTerm2 3.5.0\tattached,UTF-8\t@2\n" +
		"bad line\n")
	if len(clients) != 2 || !clients[0].Focused || clients[1].Focused || clients[1].Window != "@2" {
		t.Fatalf("parseClients() = %+v", clients)
	}

	if got := autoSequence(clients[:1]); got != OSC777 {
		t.Errorf("autoSequence(ghostty) = %s, want osc777", got)
	}
	if got := autoSequence(clients); got != OSC9 {
		t.Errorf("autoSequence(ghostty, iTerm2) = %s, want osc9", got)
	}

	p := pane{Window: "@1"}
	if shouldSend(p, nil, true) {
		t.Error("shouldSend with no client = true")
	}
	if shouldSend(p, clients, true) {
		t.Error("shouldSend while a focused client shows the window = true")
	}
	if !shouldSend(p, clients, false) {
		t.Error("shouldSend without focus-events = false; tmux can't tell, so it must send")
	}
	if !shouldSend(pane{Window: "@3"}, clients, true) {
		t.Error("shouldSend for a window nobody is looking at = false")
	}
}
