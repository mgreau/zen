package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPush(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	// The message is hostile: it must arrive as data, never run as shell.
	Configure(`printf '%s|%s|%s|%s|%s\n' "$ZEN_EVENT" "$ZEN_TITLE" "$ZEN_MESSAGE" "$ZEN_SUBTITLE" "$ZEN_RESUME" >> `+out, nil)
	t.Cleanup(func() { Configure("", nil) })

	n := note{event: EventWaiting, title: "Claude is waiting", message: "$(touch pwned) needs you", subtitle: "opus", resume: "zen work resume x"}
	if _, err := push(n); err != nil {
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
		if _, err := push(note{event: e}); err != nil {
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
	if _, err := push(note{event: EventWaiting}); err != nil {
		t.Errorf("push with no command = %v, want nil", err)
	}

	Configure("echo boom >&2; exit 3", nil)
	t.Cleanup(func() { Configure("", nil) })
	_, err := push(note{event: EventWaiting})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing command: err = %v, want its output in the error", err)
	}
}

// A child that outlives the shell must not hold push past its timeout.
func TestPushTimeoutWithChild(t *testing.T) {
	oldTimeout, oldDelay := pushTimeout, pushWaitDelay
	pushTimeout, pushWaitDelay = 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { pushTimeout, pushWaitDelay = oldTimeout, oldDelay })
	Configure("sleep 2 & wait", nil) // short, since only the shell is killed
	t.Cleanup(func() { Configure("", nil) })

	start := time.Now()
	if _, err := push(note{event: EventWaiting}); err == nil {
		t.Error("push past its timeout returned nil")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("push took %v, want about its timeout", d)
	}
}

// On a host with no desktop notifier, a push command is how notifications
// arrive: the missing notifier is not an error then.
func TestDeliverHeadless(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no notify-send, osascript or terminal-notifier
	n := note{event: EventWaiting, title: "t", message: "m"}

	Configure("", nil)
	if err := deliver(n); err == nil {
		t.Error("deliver with no notifier and no push command = nil, want the missing notifier")
	}
	Configure("true", nil)
	t.Cleanup(func() { Configure("", nil) })
	if err := deliver(n); err != nil {
		t.Errorf("deliver with a push command = %v, want nil", err)
	}

	// A push filtered out for this event delivered nothing: the missing
	// notifier is still worth reporting.
	Configure("true", []string{EventPRReview})
	if err := deliver(n); err == nil {
		t.Error("deliver with the event filtered out of the push = nil, want the missing notifier")
	}

	// A notifier that is there but fails, as notify-send does without a
	// D-Bus session, is not an error either once the push arrived.
	bin := t.TempDir()
	for _, name := range []string{"notify-send", "osascript"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	}
	t.Setenv("PATH", bin)
	Configure("true", nil)
	if err := deliver(n); err != nil {
		t.Errorf("deliver with a failing notifier and a push = %v, want nil", err)
	}
}
