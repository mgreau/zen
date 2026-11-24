package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func paneLine(id, session, tag, cwd, pid string) string {
	return strings.Join([]string{id, session, tag, cwd, pid}, "\x1f")
}

func TestMatchWindows(t *testing.T) {
	root := t.TempDir()
	wtPath := filepath.Join(root, "repo-fix")
	other := filepath.Join(root, "repo-other")
	for _, d := range []string{filepath.Join(wtPath, "sub"), other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wtPath = canonical(wtPath)
	other = canonical(other)
	out := strings.Join([]string{
		paneLine("@1", "$0", wtPath, wtPath, "10"),
		paneLine("@1", "$0", wtPath, other, "11"), // a split moved elsewhere
		paneLine("@2", "$0", wtPath, filepath.Join(wtPath, "sub"), "20"),
		paneLine("@3", "$0", "", wtPath, "30"),    // untagged, even if in the worktree
		paneLine("@4", "$0", other, wtPath, "40"), // tagged for another worktree
		"garbage",
	}, "\n")

	got := matchWindows(out, wtPath)
	want := []Window{
		{ID: "@1", SessionID: "$0", PanePIDs: []int{10, 11}, Outside: []string{other}},
		{ID: "@2", SessionID: "$0", PanePIDs: []int{20}},
	}
	if len(got) != len(want) {
		t.Fatalf("matchWindows() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].SessionID != want[i].SessionID ||
			!slices.Equal(got[i].PanePIDs, want[i].PanePIDs) || !slices.Equal(got[i].Outside, want[i].Outside) {
			t.Errorf("window %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		dir, root string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a", "/a/b", false},
		{"", "/a", false},
	}
	for _, c := range cases {
		if got := within(c.dir, c.root); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.dir, c.root, got, c.want)
		}
	}
}

// startPrivateServer runs a private tmux server and points $TMUX at it.
func startPrivateServer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Socket paths are limited to ~100 bytes, too short for t.TempDir().
	dir, err := os.MkdirTemp("/tmp", "zt")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	if out, err := exec.Command("tmux", "-S", sock, "-f", "/dev/null", "new-session", "-d", "-s", "t").CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, out)
	}
	t.Setenv("TMUX", sock+",0,0")
	t.Setenv("TMUX_PANE", "")
	return sock
}

func TestOpenFindKillWindow(t *testing.T) {
	startPrivateServer(t)
	t.Setenv("SHELL", "/bin/sh")
	wtPath := canonical(t.TempDir())

	if err := OpenWindow(wtPath, "sleep 300"); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	wins, err := WindowsFor(wtPath)
	if err != nil {
		t.Fatalf("WindowsFor: %v", err)
	}
	if len(wins) != 1 || len(wins[0].PanePIDs) != 1 || wins[0].SessionID == "" {
		t.Fatalf("WindowsFor() = %+v, want one window with one pane", wins)
	}
	if other, _ := WindowsFor(t.TempDir()); len(other) != 0 {
		t.Errorf("WindowsFor(other) = %+v, want none", other)
	}

	if err := KillWindow(wins[0].ID); err != nil {
		t.Fatalf("KillWindow: %v", err)
	}
	if err := KillWindow(wins[0].ID); err != nil {
		t.Errorf("KillWindow of a closed window = %v, want nil", err)
	}
	if left, _ := WindowsFor(wtPath); len(left) != 0 {
		t.Errorf("after KillWindow, WindowsFor() = %+v, want none", left)
	}
}

func TestRunBackground(t *testing.T) {
	startPrivateServer(t)
	dir := filepath.Join(t.TempDir(), "it's #H ##{x}")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "done")
	out := filepath.Join(dir, "out.log")
	if err := RunBackground([]string{"sh", "-c", `touch "$1"; echo "#{session_name}"`, "sh", marker}, out); err != nil {
		t.Fatalf("RunBackground: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background command did not run at the literal path")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if got, _ := os.ReadFile(out); strings.TrimSpace(string(got)) != "#{session_name}" {
		t.Errorf("output = %q, want the literal #{session_name}", got)
	}
}
