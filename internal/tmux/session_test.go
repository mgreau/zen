package tmux

import (
	"os"
	"path/filepath"
	"strings"
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

	agentsDir := filepath.Join(dir, "git")
	if err := os.Mkdir(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmuxCmd(t, "new-session", "-d", "-s", "fresh")
	if err := setUp("fresh", "touch "+ran, "sleep 30", agentsDir); err != nil {
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
	if got, _ := tmuxOutput(t, "display-message", "-p", "-t", "fresh:"+AgentsWindowName, "#{pane_current_path}"); got != agentsDir {
		t.Errorf("Agents window directory = %q, want %q", got, agentsDir)
	}

	// A missing directory falls back to the session's, instead of no window.
	tmuxCmd(t, "new-session", "-d", "-s", "nodir")
	if err := setUp("nodir", "", "sleep 30", filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	if out, _ := tmuxOutput(t, "list-windows", "-t", "nodir", "-F", "#{window_name}"); out != "sh\n"+AgentsWindowName {
		t.Errorf("windows with a missing Agents directory:\n%s", out)
	}

	// A session made to run a program gets no keys typed into it.
	other := filepath.Join(dir, "typed-into-program")
	tmuxCmd(t, "new-session", "-d", "-s", "program", "sleep 30")
	if err := setUp("program", "touch "+other, "", ""); err != nil {
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

func TestInteractive(t *testing.T) {
	for info, want := range map[string]bool{
		"1\t0": true,  // a terminal attached, not grouped
		"2\t0": true,  // two terminals
		"0\t0": false, // created in the background (tmux new -d)
		"1\t1": false, // grouped with another session
		"":     false,
	} {
		if got := interactive(info); got != want {
			t.Errorf("interactive(%q) = %v, want %v", info, got, want)
		}
	}
}

// A session a script creates in the background gets nothing: the script
// may be about to type into it.
func TestSetUpSessionDetached(t *testing.T) {
	startServer(t)
	ran := filepath.Join(t.TempDir(), "typed")
	tmuxCmd(t, "new-session", "-d", "-s", "scripted")
	if err := SetUpSession("scripted", "touch "+ran, "sleep 30", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if exists(ran) {
		t.Error("typed into a session created in the background")
	}
	if out, _ := tmuxOutput(t, "list-windows", "-t", "scripted", "-F", "#{window_name}"); strings.Contains(out, AgentsWindowName) {
		t.Errorf("added an Agents window to a background session: %s", out)
	}
}
