package agent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// RunningIn reports whether a recognised agent is live in worktreePath.
// Used to skip git fast-forwards that would race a review session.
//
// Two signals, either is enough:
//  1. A recorded session whose UUID appears on a process command line
//     (catches `claude --resume <uuid>` / `codex resume`; misses a fresh launch).
//  2. An agent process whose cwd is the worktree or a subdirectory (catches a
//     first-pass `claude /review-pr` whose session file has no UUID on argv).
func RunningIn(worktreePath string) bool {
	if runningBySession(worktreePath) {
		return true
	}
	return runningByCwd(worktreePath)
}

func runningBySession(worktreePath string) bool {
	for _, k := range AllKinds() {
		ag := New(k, "")
		sessions, err := ag.FindSessions(worktreePath)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if ag.IsProcessRunning(s.ID, worktreePath) {
				return true
			}
		}
	}
	return false
}

func runningByCwd(worktreePath string) bool {
	want, err := canonicalDir(worktreePath)
	if err != nil || want == "" {
		return false
	}
	for _, cwd := range listAgentCwds() {
		if cwdInWorktree(cwd, want) {
			return true
		}
	}
	return false
}

func canonicalDir(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs), nil
	}
	return filepath.Clean(eval), nil
}

// cwdInWorktree is true when cwd is the worktree root or a subdirectory.
// Parent directories and siblings do not match.
func cwdInWorktree(cwd, worktree string) bool {
	if cwd == "" || worktree == "" {
		return false
	}
	c, err := canonicalDir(cwd)
	if err != nil {
		c = filepath.Clean(cwd)
	}
	wt := filepath.Clean(worktree)
	if c == wt {
		return true
	}
	rel, err := filepath.Rel(wt, c)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// isAgentProcess reports whether a process is a zen-launched coding agent.
// Cursor, browsers, and other tools that happen to mention "claude" in a
// command line are excluded: we match known agent comm names, or node/python
// whose argv looks like Claude Code / Codex / Aider — not a bare substring.
func isAgentProcess(comm, cmdline string) bool {
	comm = strings.ToLower(strings.TrimSpace(comm))
	comm = strings.TrimSuffix(comm, "\n")
	comm = filepath.Base(comm)
	if comm == "" {
		return false
	}
	switch comm {
	case "claude", "codex", "aider":
		return true
	}

	cl := strings.ToLower(strings.ReplaceAll(cmdline, "\x00", " "))
	switch {
	case comm == "node" || comm == "nodejs":
		return agentNodeCmdline(cl)
	case comm == "python" || strings.HasPrefix(comm, "python"):
		return strings.Contains(cl, "aider")
	case comm == "uv" || comm == "pipx":
		return strings.Contains(cl, "aider")
	}
	return false
}

func agentNodeCmdline(cl string) bool {
	if strings.Contains(cl, "@anthropic-ai/claude") || strings.Contains(cl, "claude-code") {
		return true
	}
	if strings.Contains(cl, "@openai/codex") {
		return true
	}
	// pipx/npm shims: ".../bin/claude" or ".../bin/codex" as argv0 or an arg.
	fields := strings.Fields(cl)
	for _, f := range fields {
		base := filepath.Base(f)
		if base == "claude" || base == "codex" || base == "aider" {
			return true
		}
	}
	return false
}

type procCwd struct {
	pid int
	cwd string
}

var (
	procMu    sync.Mutex
	procCache []procCwd
	procAt    time.Time
)

const procCacheTTL = 2 * time.Second

func listAgentCwds() []string {
	return cwdsOf(listAgentProcs())
}

// Proc is a running coding-agent process.
type Proc struct {
	PID int
	Cwd string
}

// Procs returns the coding-agent processes running now and their working
// directories.
func Procs() []Proc {
	list := listAgentProcs()
	out := make([]Proc, 0, len(list))
	for _, p := range list {
		if p.pid > 0 {
			out = append(out, Proc{PID: p.pid, Cwd: p.cwd})
		}
	}
	return out
}

// InWorktree reports whether dir is worktreePath or below it.
func InWorktree(dir, worktreePath string) bool {
	want, err := canonicalDir(worktreePath)
	if err != nil {
		return false
	}
	return cwdInWorktree(dir, want)
}

// SessionPIDs returns the processes whose command line carries the ID of a
// session recorded for worktreePath, wherever they run. With the processes
// Procs finds in the worktree, these are what RunningIn counts.
func SessionPIDs(worktreePath string) []int {
	self := os.Getpid()
	seen := map[int]bool{}
	var pids []int
	for _, k := range AllKinds() {
		sessions, err := New(k, "").FindSessions(worktreePath)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if s.ID == "" {
				continue
			}
			out, _ := exec.Command("pgrep", "-f", s.ID).Output()
			for _, f := range strings.Fields(string(out)) {
				pid, err := strconv.Atoi(f)
				if err != nil || pid == self || seen[pid] {
					continue
				}
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

func listAgentProcs() []procCwd {
	procMu.Lock()
	defer procMu.Unlock()
	if time.Since(procAt) < procCacheTTL && procCache != nil {
		return procCache
	}
	var list []procCwd
	if runtime.GOOS == "linux" {
		list = listAgentCwdsLinux()
	} else {
		list = listAgentCwdsLsof()
	}
	if list == nil {
		list = []procCwd{}
	}
	procCache = list
	procAt = time.Now()
	return list
}

// ResetProcCache forgets the cached process scan, so the next check sees
// processes as they are now, such as after an agent was stopped.
func ResetProcCache() {
	procMu.Lock()
	defer procMu.Unlock()
	procCache = nil
	procAt = time.Time{}
}

func cwdsOf(list []procCwd) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		if p.cwd != "" {
			out = append(out, p.cwd)
		}
	}
	return out
}

func listAgentCwdsLinux() []procCwd {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []procCwd
	for _, e := range ents {
		name := e.Name()
		if !isAllDigits(name) {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join("/proc", name, "comm"))
		cmdline, _ := os.ReadFile(filepath.Join("/proc", name, "cmdline"))
		if !isAgentProcess(string(comm), string(cmdline)) {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", name, "cwd"))
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(name)
		out = append(out, procCwd{pid: pid, cwd: cwd})
	}
	return out
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func listAgentCwdsLsof() []procCwd {
	cmd := exec.Command("lsof", "-nP", "-a", "-d", "cwd", "-Fn",
		"-c", "claude", "-c", "codex", "-c", "aider",
		"-c", "node", "-c", "nodejs",
		"-c", "python", "-c", "python3",
		"-c", "uv", "-c", "pipx")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		// lsof exits 1 when nothing matches; missing binary also errors with no output.
		return nil
	}
	pidCwd := parseLsofCwd(out)
	var list []procCwd
	for pid, cwd := range pidCwd {
		comm, args := procCommArgs(pid)
		if !isAgentProcess(comm, args) {
			continue
		}
		list = append(list, procCwd{pid: pid, cwd: cwd})
	}
	return list
}

func parseLsofCwd(out []byte) map[int]string {
	m := make(map[int]string)
	pid := 0
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			n, err := strconv.Atoi(string(line[1:]))
			if err != nil {
				pid = 0
				continue
			}
			pid = n
		case 'n':
			if pid != 0 {
				m[pid] = string(line[1:])
			}
		}
	}
	return m
}

func procCommArgs(pid int) (comm, args string) {
	psPid := strconv.Itoa(pid)
	c, _ := exec.Command("ps", "-p", psPid, "-o", "comm=").Output()
	a, _ := exec.Command("ps", "-p", psPid, "-o", "args=").Output()
	return strings.TrimSpace(string(c)), strings.TrimSpace(string(a))
}
