package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Prompt I/O, swapped in tests. A nil promptIn means os.Stdin, read at call
// time rather than bound at init so a reassigned os.Stdin is honoured.
var (
	promptIn     io.Reader
	promptOut    io.Writer = os.Stdout
	promptReader *bufio.Reader
	promptSource io.Reader
	stdinIsTTY   = defaultStdinIsTTY
)

// promptInput is the reader prompts consume, buffered across calls so typed
// input is not lost between two prompts.
func promptInput() *bufio.Reader {
	src := promptIn
	if src == nil {
		src = os.Stdin
	}
	if promptReader == nil || promptSource != src {
		promptReader = bufio.NewReader(src)
		promptSource = src
	}
	return promptReader
}

// defaultStdinIsTTY reports whether stdin is a character device. A pipe, a
// redirected file and /dev/null are all false, so `yes | zen ...` cannot
// answer a prompt on the user's behalf.
func defaultStdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Interactive reports whether there is a human on the other end of stdin who
// can answer a prompt. Callers guarding a destructive action must check this
// before asking: a non-interactive run has to decline, not read the pipe.
func Interactive() bool { return stdinIsTTY() }

// ConfirmYN writes prompt and returns true only for an explicit y/yes typed
// on an interactive terminal. Non-interactive stdin, EOF, a read error and
// every other answer are a declined confirmation.
func ConfirmYN(prompt string) bool {
	if !Interactive() {
		return false
	}
	fmt.Fprint(promptOut, prompt)
	line, err := promptInput().ReadString('\n')
	if err != nil && line == "" {
		// EOF with nothing typed, or a broken stdin: decline.
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
