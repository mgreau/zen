package tmux

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFreshShell(t *testing.T) {
	if p, ok := freshShell("1\t%3\t"); !ok || p != "%3" {
		t.Errorf("lone shell = %q, %v", p, ok)
	}
	if _, ok := freshShell("1\t%3\tvim notes.md"); ok {
		t.Error("session running a program counted as a fresh shell")
	}
	if _, ok := freshShell("2\t%3\t"); ok {
		t.Error("session with two windows counted as fresh")
	}
}

func TestSetUpSession(t *testing.T) {
	startServer(t)
	dir := t.TempDir()
	ran := filepath.Join(dir, "inbox-ran")

	tmuxCmd(t, "new-session", "-d", "-s", "fresh")
	if err := SetUpSession("fresh", "touch "+ran, "sleep 30"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the typed inbox command", func() bool { return exists(ran) })

	out, err := tmuxOutput(t, "list-windows", "-t", "fresh", "-F", "#{window_index} #{window_name} #{window_active}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "0 sh 1\n1 " + AgentsWindowName + " 0"; out != want {
		t.Errorf("windows:\n%s\nwant\n%s (Agents in the background)", out, want)
	}

	// A session made to run a program gets no keys typed into it.
	other := filepath.Join(dir, "typed-into-program")
	tmuxCmd(t, "new-session", "-d", "-s", "program", "sleep 30")
	if err := SetUpSession("program", "touch "+other, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if exists(other) {
		t.Error("typed the inbox command into a session running a program")
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatal(err)
	}
}
