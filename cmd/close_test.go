package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/tmux"
	wt "github.com/mgreau/zen/internal/worktree"
)

// fakeClose records what a close did through closeOps.
type fakeClose struct {
	dirty    error
	inTmux   bool
	windows  []tmux.Window
	winErr   error
	procs    []agent.Proc
	sessions []int
	parents  map[int]int
	current  string
	self     int
	cwdInWT  bool
	finished bool
	spawned  bool
}

func (f *fakeClose) ops() closeOps {
	return closeOps{
		checkRemovable: func(string, string) error { return f.dirty },
		inTmux:         func() bool { return f.inTmux },
		windowsFor:     func(string) ([]tmux.Window, error) { return f.windows, f.winErr },
		agentProcs:     func() []agent.Proc { return f.procs },
		sessionPIDs:    func(string) []int { return f.sessions },
		descendsFrom: func(pid int, roots []int) bool {
			for pid > 1 {
				if slices.Contains(roots, pid) {
					return true
				}
				pid = f.parents[pid]
			}
			return false
		},
		currentWindow: func() string { return f.current },
		selfPID:       func() int { return f.self },
		cwdIn:         func(string) bool { return f.cwdInWT },
		spawnFinish:   func(closePlan) (string, error) { f.spawned = true; return "/z/close.log", nil },
		finish:        func(closeOps, closePlan) error { f.finished = true; return nil },
	}
}

func TestRunClose(t *testing.T) {
	root := t.TempDir()
	wtPath := filepath.Join(root, "repo-fix")
	elsewhere := filepath.Join(root, "repo-other")
	for _, d := range []string{filepath.Join(wtPath, "sub"), elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plan := closePlan{name: "repo-fix", path: wtPath, origin: filepath.Join(root, "repo"), cleanSessions: true}
	win := tmux.Window{ID: "@3", SessionID: "$0", PanePIDs: []int{100}}
	inWin := map[int]int{101: 100, 102: 101}
	inWT := agent.Proc{PID: 101, Cwd: filepath.Join(wtPath, "sub")}

	tests := []struct {
		name        string
		fake        fakeClose
		wantErr     error
		wantErrText string
		wantFinish  bool
		wantSpawn   bool
	}{
		{
			name:    "dirty worktree stops before anything",
			fake:    fakeClose{dirty: wt.ErrWorktreeDirty, inTmux: true, windows: []tmux.Window{win}},
			wantErr: wt.ErrWorktreeDirty,
		},
		{
			name:        "main checkout is refused",
			fake:        fakeClose{self: 900},
			wantErrText: "main checkout",
		},
		{
			name:        "tmux that cannot be queried refuses",
			fake:        fakeClose{inTmux: true, winErr: errors.New("no server running"), self: 900},
			wantErrText: "no server running",
		},
		{
			name: "window with a pane elsewhere refuses",
			fake: fakeClose{inTmux: true, self: 900,
				windows: []tmux.Window{{ID: "@3", PanePIDs: []int{100}, Outside: []string{elsewhere}}}},
			wantErrText: "has a pane in",
		},
		{
			name: "cwd in the worktree goes to the background when the window cannot be told",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win}, parents: inWin,
				procs: []agent.Proc{inWT}, self: 900, cwdInWT: true},
			wantSpawn: true,
		},
		{
			name:        "no window to close and run from the worktree refuses",
			fake:        fakeClose{self: 900, cwdInWT: true},
			wantErrText: "outside the worktree",
		},
		{
			name: "cwd in the worktree from another known window finishes here",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win}, parents: inWin,
				procs: []agent.Proc{inWT}, current: "@1", self: 900, cwdInWT: true},
			wantFinish: true,
		},
		{
			name:       "outside tmux with no agent finishes here",
			fake:       fakeClose{self: 900},
			wantFinish: true,
		},
		{
			name:    "outside tmux with an agent refuses",
			fake:    fakeClose{procs: []agent.Proc{inWT}, self: 900},
			wantErr: wt.ErrWorktreeActive,
		},
		{
			name: "agent in the worktree outside zen's window refuses",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win}, parents: inWin, self: 900,
				procs: []agent.Proc{inWT, {PID: 300, Cwd: wtPath}}},
			wantErr: wt.ErrWorktreeActive,
		},
		{
			name: "window running an agent elsewhere refuses",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win}, parents: inWin, self: 900,
				procs: []agent.Proc{{PID: 102, Cwd: elsewhere}}},
			wantErrText: "also runs an agent",
		},
		{
			name: "session process outside the window refuses",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win}, parents: inWin, self: 900,
				procs: []agent.Proc{inWT}, sessions: []int{101, 400}},
			wantErr: wt.ErrWorktreeActive,
		},
		{
			name: "agents elsewhere outside the window are fine",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win}, parents: inWin, self: 900,
				current: "@1", procs: []agent.Proc{inWT, {PID: 500, Cwd: elsewhere}}, sessions: []int{101}},
			wantFinish: true,
		},
		{
			name: "run from the window being closed goes to the background",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win}, parents: inWin,
				procs: []agent.Proc{inWT}, current: "@3", self: 900},
			wantSpawn: true,
		},
		{
			name: "agent's shell in the window goes to the background without TMUX_PANE",
			fake: fakeClose{inTmux: true, windows: []tmux.Window{win},
				parents: map[int]int{101: 100, 102: 101, 900: 102}, procs: []agent.Proc{inWT}, self: 900},
			wantSpawn: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fake
			p := plan
			if strings.HasPrefix(tt.name, "main checkout") {
				p.origin = p.path
			}
			logPath, err := runClose(f.ops(), p)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("runClose() error = %v, want %v", err, tt.wantErr)
				}
			case tt.wantErrText != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("runClose() error = %v, want it to mention %q", err, tt.wantErrText)
				}
			case err != nil:
				t.Fatalf("runClose() error = %v", err)
			}
			if f.finished != tt.wantFinish {
				t.Errorf("finished = %v, want %v", f.finished, tt.wantFinish)
			}
			if f.spawned != tt.wantSpawn || (logPath != "") != tt.wantSpawn {
				t.Errorf("spawned = %v (log %q), want %v", f.spawned, logPath, tt.wantSpawn)
			}
		})
	}
}

func TestCheckCloseStopsSessionProcesses(t *testing.T) {
	wtPath := t.TempDir()
	f := fakeClose{inTmux: true, windows: []tmux.Window{{ID: "@3", PanePIDs: []int{100}}},
		parents: map[int]int{101: 100, 150: 100},
		procs:   []agent.Proc{{PID: 101, Cwd: wtPath}}, sessions: []int{101, 150}}
	_, stopping, err := checkClose(f.ops(), closePlan{name: "x", path: wtPath, origin: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopping, []int{101, 150}) {
		t.Errorf("stopping = %v, want the agent and the session process [101 150]", stopping)
	}
}

func TestFinishArgv(t *testing.T) {
	t.Cleanup(func() {
		finishPath, finishOrigin, finishName, finishClient, finishCleanSessions = "", "", "", "", false
	})
	plan := closePlan{name: "repo-fix", path: "/w/repo-fix #H", origin: "/w/repo", cleanSessions: true, client: "/dev/pts/3"}
	got := finishArgv("/bin/zen", plan)
	want := []string{
		"/bin/zen", "__close-finish",
		"--path", "/w/repo-fix #H", "--origin", "/w/repo", "--name", "repo-fix",
		"--clean-sessions", "--client", "/dev/pts/3",
	}
	if !slices.Equal(got, want) {
		t.Errorf("finishArgv() =\n  %q\nwant\n  %q", got, want)
	}
	if err := closeFinishCmd.ParseFlags(got[2:]); err != nil {
		t.Fatalf("__close-finish rejects its own argv: %v", err)
	}
	if finishPath != plan.path || finishClient != plan.client || !finishCleanSessions {
		t.Errorf("parsed path=%q client=%q clean=%v", finishPath, finishClient, finishCleanSessions)
	}
}

func TestWorktreeContaining(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "repo-a")
	inner := filepath.Join(outer, "nested-b")
	sibling := filepath.Join(root, "repo-ab")
	for _, d := range []string{filepath.Join(inner, "pkg"), sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wts := []wt.Worktree{{Name: "a", Path: outer}, {Name: "b", Path: inner}, {Name: "ab", Path: sibling}}
	cases := map[string]string{
		outer:                       "a",
		filepath.Join(inner, "pkg"): "b",
		sibling:                     "ab",
		root:                        "",
	}
	for dir, want := range cases {
		got := worktreeContaining(wts, dir)
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != want {
			t.Errorf("worktreeContaining(%s) = %q, want %q", strings.TrimPrefix(dir, root), name, want)
		}
	}
}
