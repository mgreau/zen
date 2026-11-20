package session

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// LastTurnInterrupted reads the end of a session file, starting with
// interruptTail bytes: the interrupt marker is followed only by a few
// bookkeeping entries. If they fill that, as a large file-history snapshot
// can, it reads further back, up to maxInterruptTail.
const (
	interruptTail    = 256 * 1024
	maxInterruptTail = 16 * 1024 * 1024
)

// LastTurnInterrupted reports whether the last message in a Claude session
// file is the marker Claude records when the user interrupts a turn with
// Esc: "[Request interrupted by user]", or "... for tool use]". No hook
// reports an interrupt, so this is how zen tells an interrupted turn, which
// waits on the user, from a tool that has been quiet for a while.
func LastTurnInterrupted(path string) bool { return ReadTail(path).Interrupted }

// ToolRunning reports whether a Claude session has a tool call whose result
// isn't in yet: Claude is busy running it, or a subagent (which writes to
// files of its own), however quiet the main session file has been.
func ToolRunning(path string) bool { return ReadTail(path).ToolRunning }

// Tail is what the end of a Claude session file says about its state.
type Tail struct {
	Interrupted bool // the last message is the user's interrupt (Esc)
	ToolRunning bool // a tool call has no result yet
}

// ReadTail reads the end of the Claude session file at path, once, for
// both LastTurnInterrupted and ToolRunning.
func ReadTail(path string) Tail {
	f, err := os.Open(path)
	if err != nil {
		return Tail{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Tail{}
	}
	for tail := int64(interruptTail); ; tail *= 2 {
		start := max(0, info.Size()-tail)
		data, err := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
		if err != nil {
			return Tail{}
		}
		lines := bytes.Split(data, []byte("\n"))
		if start > 0 {
			lines = lines[1:] // the first line is cut
		}
		if t, found := readTail(lines); found || start == 0 || tail >= maxInterruptTail {
			return t
		}
	}
}

// entry is the part of a session file entry zen reads.
type entry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is a content block of a message.
type block struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
}

func blocks(content json.RawMessage) []block {
	var bs []block
	if json.Unmarshal(content, &bs) != nil {
		return nil
	}
	return bs
}

// readTail reads lines, the end of a session file, and reports whether it
// found a user or assistant message in them.
func readTail(lines [][]byte) (Tail, bool) {
	var entries []entry
	for _, l := range lines {
		var e entry
		if json.Unmarshal(l, &e) == nil && (e.Type == "user" || e.Type == "assistant") {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return Tail{}, false
	}

	// Tool calls in parallel finish in any order: one is running while any
	// call has no result. A call's result always follows it, so a call in
	// the tail has its result in the tail too, if it has one.
	pending := map[string]bool{}
	for _, e := range entries {
		for _, b := range blocks(e.Message.Content) {
			switch {
			case e.Type == "assistant" && b.Type == "tool_use" && b.ID != "":
				pending[b.ID] = true
			case e.Type == "user" && b.Type == "tool_result":
				delete(pending, b.ToolUseID)
			}
		}
	}

	return Tail{Interrupted: endsInInterrupt(entries), ToolRunning: len(pending) > 0}, true
}

// endsInInterrupt reports whether the last message is the interrupt marker.
// A local slash command run since, such as /status, leaves Claude waiting:
// its records (a meta caveat, <command-name>, <local-command-stdout>) are
// skipped. A prompt command, such as /review-pr, sets Claude working: its
// <command-name> record without local output counts as the user's message.
func endsInInterrupt(entries []entry) bool {
	localOutput := false
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type == "assistant" {
			return false
		}
		text := messageText(e.Message.Content)
		switch {
		case strings.HasPrefix(text, "<local-command-"):
			localOutput = true
			continue
		case strings.HasPrefix(text, "<command-"), e.IsMeta:
			if localOutput {
				continue
			}
			return false
		}
		return strings.HasPrefix(text, "[Request interrupted by user")
	}
	return false
}

// messageText returns a user message's text: the string content, or its
// first text block.
func messageText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	for _, b := range blocks(content) {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}
