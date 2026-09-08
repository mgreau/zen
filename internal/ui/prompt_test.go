package ui

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// setPromptIO points the prompt at a fixed stdin and captures its output.
func setPromptIO(t *testing.T, tty bool, input string) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	oldIn, oldOut, oldTTY, oldReader, oldSrc := promptIn, promptOut, stdinIsTTY, promptReader, promptSource
	promptIn = strings.NewReader(input)
	promptOut = &out
	stdinIsTTY = func() bool { return tty }
	promptReader, promptSource = nil, nil
	t.Cleanup(func() {
		promptIn, promptOut, stdinIsTTY, promptReader, promptSource = oldIn, oldOut, oldTTY, oldReader, oldSrc
	})
	return &out
}

// errReader fails every read, standing in for a broken stdin.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestConfirmYN_pipedInputIsDeclined(t *testing.T) {
	// The regression: `yes | zen review 20` must not confirm a hard reset.
	for _, input := range []string{"y\n", "yes\n", "Y\n"} {
		out := setPromptIO(t, false, input)
		if ConfirmYN("Reset? [y/N]: ") {
			t.Fatalf("piped %q confirmed; want declined", input)
		}
		if out.Len() != 0 {
			t.Fatalf("non-interactive run wrote a prompt: %q", out.String())
		}
	}
}

func TestConfirmYN_interactiveAnswers(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"  yes  \n", true},
		{"YES\n", true},
		{"y", true}, // typed without a trailing newline
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"", false}, // immediate EOF
		{"maybe\n", false},
	}
	for _, tt := range tests {
		setPromptIO(t, true, tt.input)
		if got := ConfirmYN("Reset? [y/N]: "); got != tt.want {
			t.Fatalf("ConfirmYN(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestConfirmYN_interactiveWritesPrompt(t *testing.T) {
	out := setPromptIO(t, true, "n\n")
	ConfirmYN("Reset? [y/N]: ")
	if !strings.Contains(out.String(), "Reset? [y/N]: ") {
		t.Fatalf("prompt not written, got %q", out.String())
	}
}

func TestConfirmYN_readErrorIsDeclined(t *testing.T) {
	setPromptIO(t, true, "")
	promptIn = errReader{}
	promptReader, promptSource = nil, nil
	if ConfirmYN("Reset? [y/N]: ") {
		t.Fatal("read error confirmed; want declined")
	}
}

func TestInteractive_usesStdinCheck(t *testing.T) {
	setPromptIO(t, true, "")
	if !Interactive() {
		t.Fatal("Interactive() = false with a terminal stdin")
	}
	setPromptIO(t, false, "")
	if Interactive() {
		t.Fatal("Interactive() = true with a piped stdin")
	}
}

// withStdin points os.Stdin at f for the duration of the test so the real
// defaultStdinIsTTY is exercised rather than the stdinIsTTY stub.
func withStdin(t *testing.T, f *os.File) {
	t.Helper()
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old })
}

func TestDefaultStdinIsTTY_nonTerminalStdin(t *testing.T) {
	// The regression: /dev/null is a character device, so the old
	// os.ModeCharDevice check called it a terminal and its immediate EOF
	// was accepted as a submitted answer.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { devNull.Close() })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })

	regular, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	t.Cleanup(func() { regular.Close() })

	for _, tt := range []struct {
		name string
		file *os.File
	}{
		{os.DevNull, devNull},
		{"pipe", r},
		{"regular file", regular},
	} {
		withStdin(t, tt.file)
		if defaultStdinIsTTY() {
			t.Errorf("defaultStdinIsTTY() = true with %s as stdin; want false", tt.name)
		}
	}
}

func TestConfirmYN_devNullStdinIsDeclined(t *testing.T) {
	// End to end through the real TTY check: `zen ... < /dev/null` must not
	// confirm, and must not print a prompt nobody can answer.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { devNull.Close() })
	withStdin(t, devNull)

	var out bytes.Buffer
	oldOut, oldIn, oldReader, oldSrc := promptOut, promptIn, promptReader, promptSource
	promptOut, promptIn, promptReader, promptSource = &out, nil, nil, nil
	t.Cleanup(func() {
		promptOut, promptIn, promptReader, promptSource = oldOut, oldIn, oldReader, oldSrc
	})

	if ConfirmYN("Reset? [y/N]: ") {
		t.Fatal("/dev/null stdin confirmed; want declined")
	}
	if out.Len() != 0 {
		t.Fatalf("prompt written to a non-interactive run: %q", out.String())
	}
}
