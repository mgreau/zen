package tmux

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWindowArgs(t *testing.T) {
	got := windowArgs("/home/u/git/repo-mono/mono-fix-x", `claude "review it"`, "/bin/bash", false)
	want := []string{
		"new-window", "-P", "-F", "#{window_id}", "-n", "mono-fix-x", "-c", "/home/u/git/repo-mono/mono-fix-x",
		"--", "/bin/bash", "-l", "-c", `claude "review it"; exec "${SHELL:-/bin/sh}"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("windowArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

func TestWindowArgsDetached(t *testing.T) {
	got := windowArgs("/w/x", "claude", "/bin/sh", true)
	if len(got) < 2 || got[1] != "-d" {
		t.Errorf("windowArgs(detached) = %q, want -d after new-window", got)
	}
}

func TestRespawnArgs(t *testing.T) {
	got := respawnArgs("@4", "/w/x", "claude --resume 'id'", "/bin/zsh")
	want := []string{
		"respawn-window", "-k", "-t", "@4", "-c", "/w/x",
		"--", "/bin/zsh", "-l", "-c", `claude --resume 'id'; exec "${SHELL:-/bin/sh}"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("respawnArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

func TestParseAndFindWindow(t *testing.T) {
	out := "@1\t\tvim\t10\t1\t\t\n" +
		"@2\t/w/a\tbash\t20\t1\trunning\t1\n" +
		"malformed line\n" +
		"@3\t/w/b/\tbash\t30\t1\twaiting\t\n" +
		"@4\t/w/c\tvim\t40\t1\t\t\n" +
		"@5\t/w/e\tbash\t50\t2\t\t\n" +
		"@6\t/w/f\tzsh\t60\t1\t\t\n"
	windows := parseWindows(out)
	if len(windows) != 6 {
		t.Fatalf("parseWindows() = %d windows, want 6: %+v", len(windows), windows)
	}
	if !windows[1].Hooks || windows[2].Hooks {
		t.Errorf("Hooks parsed as %v, %v; want true, false", windows[1].Hooks, windows[2].Hooks)
	}
	args := map[string]string{
		"20": "/bin/bash -l -c claude --resume x; exec bash", // agent running
		"30": "/bin/bash",                                    // at the prompt
		"40": "/bin/bash",                                    // vim in the foreground
		"50": "/bin/bash",                                    // split: active pane idle
		"60": "/usr/bin/zsh",                                 // a shell other than zen's
	}
	processArgs = func(pid string) (string, error) { return args[pid], nil }
	t.Cleanup(func() { processArgs = defaultProcessArgs })

	w, ok := findWindow(windows, "/w/b")
	if !ok || w.ID != "@3" {
		t.Fatalf("findWindow(/w/b) = %+v, %v; want @3", w, ok)
	}
	if !w.idle() {
		t.Error("window at bash prompt: idle() = false, want true")
	}
	if a, _ := findWindow(windows, "/w/a"); a.idle() {
		t.Error("window still running the agent: idle() = true, want false")
	}
	if c, _ := findWindow(windows, "/w/c"); c.idle() {
		t.Error("window running vim: idle() = true, want false")
	}
	if e, _ := findWindow(windows, "/w/e"); e.idle() {
		t.Error("window split in two panes: idle() = true, want false")
	}
	if f, _ := findWindow(windows, "/w/f"); !f.idle() {
		t.Error("window at a zsh prompt: idle() = false, want true")
	}
	if _, ok := findWindow(windows, "/w/none"); ok {
		t.Error("findWindow() found a window for an unknown worktree")
	}
}

var defaultProcessArgs = processArgs

func TestStatusUpdates(t *testing.T) {
	processArgs = func(pid string) (string, error) {
		if pid == "busy" {
			return "/bin/sh -l -c codex; exec sh", nil
		}
		return "/bin/sh", nil
	}
	t.Cleanup(func() { processArgs = defaultProcessArgs })

	idle := func(w window) window { w.Command, w.PID, w.Panes = "sh", "idle", "1"; return w }
	windows := []window{
		{ID: "@1"}, // not zen's
		idle(window{ID: "@2", Worktree: "/w/a", Status: "running"}),                              // unchanged
		idle(window{ID: "@3", Worktree: "/w/b/", Status: "running"}),                             // changes
		idle(window{ID: "@4", Worktree: "/w/c"}),                                                 // not in the scan
		idle(window{ID: "@5", Worktree: "/w/d"}),                                                 // first status
		idle(window{ID: "@6", Worktree: "/w/h1", Status: "waiting", Hooks: true}),                // hooks know better
		idle(window{ID: "@7", Worktree: "/w/h2", Status: "waiting", Hooks: true}),                // agent exited
		idle(window{ID: "@8", Worktree: "/w/h3", Status: "stopped", Hooks: true}),                // agent restarted
		idle(window{ID: "@9", Worktree: "/w/h4", Hooks: true}),                                   // status cleared
		idle(window{ID: "@10", Worktree: "/w/h5", Status: "running", Hooks: true}),               // turn interrupted
		idle(window{ID: "@12", Worktree: "/w/h6", Status: "running", Hooks: true}),               // a long, quiet tool
		idle(window{ID: "@13", Worktree: "/w/h7", Status: "running", Hooks: true}),               // idle past StaleRunning
		idle(window{ID: "@14", Worktree: "/w/h8", Status: "running", Hooks: true}),               // a long subagent
		{ID: "@11", Worktree: "/w/x", Status: "running", Command: "sh", PID: "busy", Panes: "1"}, // other agent
	}
	got := statusUpdates(windows, map[string]Report{
		"/w/a": {Status: "running"}, "/w/b": {Status: "waiting"}, "/w/d": {Status: "stopped"},
		"/w/gone": {Status: "waiting"},
		"/w/h1":   {Status: "running"}, "/w/h2": {Status: "stopped"}, "/w/h3": {Status: "running"},
		"/w/h4": {Status: "running"}, "/w/h5": {Status: "waiting", Interrupted: true}, "/w/h6": {Status: "waiting"},
		"/w/h7": {Status: "waiting", Idle: StaleRunning},
		"/w/h8": {Status: "waiting", Idle: 2 * StaleRunning, ToolRunning: true},
		"/w/x":  {Status: "stopped"},
	})
	var gotIDs []string
	for _, w := range got {
		gotIDs = append(gotIDs, w.ID+"="+w.Status)
	}
	want := []string{"@3=waiting", "@5=stopped", "@7=stopped", "@8=running", "@9=running", "@10=waiting", "@13=waiting"}
	if !slices.Equal(gotIDs, want) {
		t.Errorf("statusUpdates() = %q, want %q", gotIDs, want)
	}
}

func TestOpenWindowOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	if _, err := OpenWindow(t.TempDir(), "true"); !errors.Is(err, ErrNotInTmux) {
		t.Errorf("OpenWindow() outside tmux = %v, want ErrNotInTmux", err)
	}
}

func TestUserShell(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := userShell(); got != "/bin/sh" {
		t.Errorf("userShell() with SHELL unset = %q, want /bin/sh", got)
	}
	t.Setenv("SHELL", "/usr/bin/zsh")
	if got := userShell(); got != "/usr/bin/zsh" {
		t.Errorf("userShell() = %q, want /usr/bin/zsh", got)
	}
}

// startServer starts a private tmux server and points $TMUX at it, as if zen
// ran in a pane of its only session.
func startServer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// A short socket path: Unix socket paths are limited to ~100 bytes.
	dir, err := os.MkdirTemp("", "zt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("TMUX_PANE", "")
	if out, err := exec.Command("tmux", "-S", sock, "-f", "/dev/null", "new-session", "-d", "-s", "zen-test").CombinedOutput(); err != nil {
		t.Fatalf("starting tmux server: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("tmux", "-S", sock, "kill-server").Run() })
	t.Setenv("TMUX", sock+",0,0")
}

func tmuxWindows(t *testing.T) []window {
	t.Helper()
	windows, err := listWindows()
	if err != nil {
		t.Fatal(err)
	}
	return windows
}

// waitFor polls cond until it holds, failing the test after a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// zenWindow returns the window zen opened for workDir.
func zenWindow(t *testing.T, workDir string) window {
	t.Helper()
	w, ok := findWindow(tmuxWindows(t), workDir)
	if !ok {
		t.Fatalf("no window for %s in %+v", workDir, tmuxWindows(t))
	}
	return w
}

func TestOpenWindowReusesWindow(t *testing.T) {
	startServer(t)
	workDir := t.TempDir()
	marker := func(name string) string { return filepath.Join(workDir, name) }
	open := func(command string, want Outcome) {
		t.Helper()
		got, err := OpenWindowDetached(workDir, command)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("OpenWindowDetached(%q) = outcome %d, want %d", command, got, want)
		}
	}

	// Detached, so the test needs no attached client to switch.
	open("touch first", NewWindow)
	waitFor(t, "first command", func() bool { return exists(marker("first")) })
	first := zenWindow(t, workDir)
	waitFor(t, "idle shell", func() bool { return zenWindow(t, workDir).idle() })

	// An idle window is respawned with the new command, not duplicated.
	open("touch second", Reused)
	waitFor(t, "respawned command", func() bool { return exists(marker("second")) })
	if got := zenWindow(t, workDir).ID; got != first.ID {
		t.Errorf("reopen used window %s, want reuse of %s", got, first.ID)
	}

	// A busy window is left alone.
	waitFor(t, "idle shell", func() bool { return zenWindow(t, workDir).idle() })
	open("sleep 30", Reused)
	waitFor(t, "sleep", func() bool { return exec.Command("pgrep", "-f", "^sleep 30$").Run() == nil })
	open("touch third", AlreadyRunning)
	time.Sleep(200 * time.Millisecond)
	if exists(marker("third")) {
		t.Error("reopening a busy window ran the command in it")
	}

	out, err := exec.Command("tmux", "show-options", "-w", "-v", "-t", first.ID, "allow-passthrough").Output()
	if err != nil || strings.TrimSpace(string(out)) != "all" {
		t.Errorf("allow-passthrough = %q, %v; want all", out, err)
	}

	var tagged int
	for _, w := range tmuxWindows(t) {
		if w.Worktree != "" {
			tagged++
		}
	}
	if tagged != 1 {
		t.Errorf("%d zen windows, want 1", tagged)
	}

	if err := SetStatuses(map[string]Report{workDir: {Status: "waiting"}}); err != nil {
		t.Fatal(err)
	}
	if got := zenWindow(t, workDir).Status; got != "waiting" {
		t.Errorf("status after SetStatuses = %q, want waiting", got)
	}
}

func tmuxCmd(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
}

func TestOpenWindowSplitWindowIsBusy(t *testing.T) {
	startServer(t)
	workDir := t.TempDir()
	if _, err := OpenWindowDetached(workDir, "true"); err != nil {
		t.Fatal(err)
	}
	w := zenWindow(t, workDir)
	waitFor(t, "idle shell", func() bool { return zenWindow(t, workDir).idle() })

	// A second pane, as a user splitting the window would add.
	tmuxCmd(t, "split-window", "-d", "-t", w.ID, "sleep 30")
	got, err := OpenWindowDetached(workDir, "touch ran")
	if err != nil {
		t.Fatal(err)
	}
	if got != AlreadyRunning {
		t.Errorf("open in a split window = outcome %d, want AlreadyRunning", got)
	}
	time.Sleep(200 * time.Millisecond)
	if exists(filepath.Join(workDir, "ran")) {
		t.Error("command ran in a split window")
	}
	if n := zenWindow(t, workDir).Panes; n != "2" {
		t.Errorf("window has %s panes after open, want 2 (nothing killed)", n)
	}
}

func TestOpenWindowReuseClearsStatus(t *testing.T) {
	startServer(t)
	workDir := t.TempDir()
	if _, err := OpenWindowDetached(workDir, "true"); err != nil {
		t.Fatal(err)
	}
	w := zenWindow(t, workDir)
	waitFor(t, "idle shell", func() bool { return zenWindow(t, workDir).idle() })
	tmuxCmd(t, "set-option", "-w", "-t", w.ID, StatusOption, "stopped")
	tmuxCmd(t, "set-option", "-w", "-t", w.ID, HooksOption, "1")

	if got, err := OpenWindowDetached(workDir, "sleep 30"); err != nil || got != Reused {
		t.Fatalf("reopen = %d, %v; want Reused", got, err)
	}
	if w := zenWindow(t, workDir); w.Status != "" || w.Hooks {
		t.Errorf("after reuse: status %q, hooks %v; want both cleared", w.Status, w.Hooks)
	}
}

func TestSetStatusesStoppedClearsHooks(t *testing.T) {
	startServer(t)
	workDir := t.TempDir()
	if _, err := OpenWindowDetached(workDir, "true"); err != nil {
		t.Fatal(err)
	}
	w := zenWindow(t, workDir)
	tmuxCmd(t, "set-option", "-w", "-t", w.ID, StatusOption, "waiting")
	tmuxCmd(t, "set-option", "-w", "-t", w.ID, HooksOption, "1")

	if err := SetStatuses(map[string]Report{workDir: {Status: "running"}}); err != nil {
		t.Fatal(err)
	}
	if got := zenWindow(t, workDir).Status; got != "waiting" {
		t.Errorf("daemon overwrote the hooks' status: %q, want waiting", got)
	}

	// An interrupted turn, which no hook reports, overrides a "running" the
	// hooks left behind.
	tmuxCmd(t, "set-option", "-w", "-t", w.ID, StatusOption, "running")
	if err := SetStatuses(map[string]Report{workDir: {Status: "waiting", Interrupted: true}}); err != nil {
		t.Fatal(err)
	}
	if got := zenWindow(t, workDir).Status; got != "waiting" {
		t.Errorf("after an interrupted turn: %q, want waiting", got)
	}

	waitFor(t, "idle shell", func() bool { return zenWindow(t, workDir).idle() })
	if err := SetStatuses(map[string]Report{workDir: {Status: "stopped"}}); err != nil {
		t.Fatal(err)
	}
	if w := zenWindow(t, workDir); w.Status != "stopped" || w.Hooks {
		t.Errorf("after stopped: status %q, hooks %v; want stopped, false", w.Status, w.Hooks)
	}
}

func TestSetStatusesWithoutServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("", "zt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("TMUX", filepath.Join(dir, "none")+",0,0")
	if err := SetStatuses(map[string]Report{"/w": {Status: "waiting"}}); err != nil {
		t.Errorf("SetStatuses() with no server = %v, want nil", err)
	}
}

// The pane's interactive shell comes from tmux, which sets $SHELL in each pane
// to its default-shell, so it may differ from zen's own $SHELL.
func TestOpenWindowIdleWithOtherShell(t *testing.T) {
	startServer(t) // zen's SHELL is /bin/sh
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	tmuxCmd(t, "set-option", "-g", "default-shell", bash)
	workDir := t.TempDir()
	if _, err := OpenWindowDetached(workDir, "true"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bash prompt", func() bool { return zenWindow(t, workDir).Command == "bash" })

	got, err := OpenWindowDetached(workDir, "touch ran")
	if err != nil {
		t.Fatal(err)
	}
	if got != Reused {
		t.Errorf("open at a bash prompt with SHELL=/bin/sh = outcome %d, want Reused", got)
	}
	waitFor(t, "command", func() bool { return exists(filepath.Join(workDir, "ran")) })
}

func tmuxOutput(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}
