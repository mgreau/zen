package agentdeck

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeRun replaces run with a scripted agent-deck. Each call pops the next
// reply; the args of every call are recorded.
type fakeRun struct {
	t       *testing.T
	replies []string
	calls   [][]string
}

func (f *fakeRun) install() {
	orig := run
	f.t.Cleanup(func() { run = orig })
	run = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, args)
		if len(f.replies) == 0 {
			f.t.Fatalf("unexpected agent-deck call: %v", args)
		}
		reply := f.replies[0]
		f.replies = f.replies[1:]
		if strings.Contains(reply, `"success": false`) {
			return []byte(reply), errors.New("exit status 1")
		}
		return []byte(reply), nil
	}
}

const (
	launched = `{"success": true, "id": "new-1"}`
	exists   = `{"success": false, "code": "ALREADY_EXISTS", "error": "session already exists", "existing_id": "old-1"}`
	removed  = `{"success": true, "id": "old-1"}`
	dead     = `{"id": "old-1", "status": "error"}`
	stopped  = `{"id": "old-1", "status": "stopped"}`
	live     = `{"id": "old-1", "status": "waiting"}`
)

func launchArgs(workDir, command string) []string {
	return []string{"launch", "-title", filepath.Base(workDir), "-group", Group, "-no-parent", "-cmd", command, "-json", workDir}
}

func TestOpenTabLaunches(t *testing.T) {
	f := &fakeRun{t: t, replies: []string{launched}}
	f.install()

	cmd := `claude --model opus '/review-pr'`
	if err := OpenTab("/src/zen-pr-32", cmd); err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	want := [][]string{launchArgs("/src/zen-pr-32", cmd)}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
}

func TestOpenTabReplacesDeadSession(t *testing.T) {
	for _, status := range []string{dead, stopped} {
		f := &fakeRun{t: t, replies: []string{exists, status, removed, launched}}
		f.install()

		cmd := `claude --resume abc`
		if err := OpenTab("/src/zen-pr-32", cmd); err != nil {
			t.Fatalf("OpenTab with %s: %v", status, err)
		}
		want := [][]string{
			launchArgs("/src/zen-pr-32", cmd),
			{"session", "show", "-json", "old-1"},
			{"session", "remove", "-force", "-json", "old-1"},
			launchArgs("/src/zen-pr-32", cmd),
		}
		if !reflect.DeepEqual(f.calls, want) {
			t.Errorf("with %s: calls = %q, want %q", status, f.calls, want)
		}
	}
}

func TestOpenTabLeavesLiveSessionAlone(t *testing.T) {
	for _, status := range []string{live, `{"id": "old-1", "status": "running"}`, `{"id": "old-1", "status": "starting"}`, `{"id": "old-1"}`} {
		f := &fakeRun{t: t, replies: []string{exists, status}}
		f.install()

		err := OpenTab("/src/zen-pr-32", "claude")
		if err == nil {
			t.Fatalf("OpenTab with %s succeeded over a live session", status)
		}
		for _, want := range []string{"live session", "zen-pr-32", "agent-deck session attach old-1"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
		if len(f.calls) != 2 {
			t.Errorf("with %s: made %d calls, want launch then show only: %q", status, len(f.calls), f.calls)
		}
	}
}

func TestOpenTabReportsFailedRemove(t *testing.T) {
	f := &fakeRun{t: t, replies: []string{exists, dead, `{"success": false, "error": "locked"}`}}
	f.install()

	err := OpenTab("/src/zen-pr-32", "claude")
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("OpenTab error = %v, want the remove failure", err)
	}
}

func TestOpenTabReportsLaunchFailure(t *testing.T) {
	f := &fakeRun{t: t, replies: []string{`{"success": false, "code": "NOT_FOUND", "error": "path does not exist"}`}}
	f.install()

	err := OpenTab("/nope", "claude")
	if err == nil || !strings.Contains(err.Error(), "path does not exist") {
		t.Fatalf("OpenTab error = %v, want agent-deck's message", err)
	}
}

func TestOpenTabRejectsUnreadableOutput(t *testing.T) {
	f := &fakeRun{t: t, replies: []string{"Session created"}}
	f.install()

	err := OpenTab("/src/x", "claude")
	if err == nil || !strings.Contains(err.Error(), "unreadable output") {
		t.Fatalf("OpenTab error = %v, want unreadable output", err)
	}
}

// TestOpenTabExecsBinary runs the real exec path against a fake agent-deck
// script, checking the argv it receives survives a command with quotes.
func TestOpenTabExecsBinary(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + argvFile + "'\necho '" + launched + "'\n"
	bin := filepath.Join(dir, "agent-deck")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := Bin
	Bin = bin
	t.Cleanup(func() { Bin = orig })

	cmd := `claude --model opus 'it'\''s /review-pr'`
	if err := OpenTab("/src/zen-pr-32", cmd); err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	got, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(launchArgs("/src/zen-pr-32", cmd), "\n") + "\n"
	if string(got) != want {
		t.Errorf("argv =\n%s\nwant\n%s", got, want)
	}
}

func TestOpenTabMissingBinary(t *testing.T) {
	orig := Bin
	Bin = filepath.Join(t.TempDir(), "agent-deck")
	t.Cleanup(func() { Bin = orig })

	err := OpenTab("/src/x", "claude")
	if err == nil {
		t.Fatal("OpenTab succeeded without a binary")
	}
}
