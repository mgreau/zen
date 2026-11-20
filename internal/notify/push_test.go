package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPush(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	// The message is hostile: it must arrive as data, never run as shell.
	Configure(`printf '%s|%s|%s|%s|%s\n' "$ZEN_EVENT" "$ZEN_TITLE" "$ZEN_MESSAGE" "$ZEN_SUBTITLE" "$ZEN_RESUME" >> `+out, nil)
	t.Cleanup(func() { Configure("", nil) })

	n := note{event: EventWaiting, title: "Claude is waiting", message: "$(touch pwned) needs you", subtitle: "opus", resume: "zen work resume x"}
	if err := push(n); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "session-waiting|Claude is waiting|$(touch pwned) needs you|opus|zen work resume x\n"; string(got) != want {
		t.Errorf("command saw %q, want %q", got, want)
	}
	if _, err := os.Stat("pwned"); err == nil {
		os.Remove("pwned")
		t.Error("message was run as shell")
	}
}

func TestPushEvents(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	Configure(`echo "$ZEN_EVENT" >> `+out, []string{EventWaiting})
	t.Cleanup(func() { Configure("", nil) })

	for _, e := range []string{EventPRReview, EventWaiting, EventDigest} {
		if err := push(note{event: e}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(out)
	if string(got) != EventWaiting+"\n" {
		t.Errorf("command ran for %q, want only %s", got, EventWaiting)
	}
}

func TestPushDisabledAndFailing(t *testing.T) {
	Configure("", nil)
	if err := push(note{event: EventWaiting}); err != nil {
		t.Errorf("push with no command = %v, want nil", err)
	}

	Configure("echo boom >&2; exit 3", nil)
	t.Cleanup(func() { Configure("", nil) })
	err := push(note{event: EventWaiting})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing command: err = %v, want its output in the error", err)
	}
}
