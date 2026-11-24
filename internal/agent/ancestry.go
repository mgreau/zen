package agent

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// DescendsFrom reports whether pid is one of roots or a descendant of one.
func DescendsFrom(pid int, roots []int) bool {
	return descendsFrom(pid, roots, parentPID)
}

func descendsFrom(pid int, roots []int, parent func(int) int) bool {
	want := make(map[int]bool, len(roots))
	for _, r := range roots {
		want[r] = true
	}
	seen := map[int]bool{}
	for pid > 1 && !seen[pid] {
		if want[pid] {
			return true
		}
		seen[pid] = true
		pid = parent(pid)
	}
	return false
}

// parentPID returns the parent of pid, or 0 when it cannot be read.
func parentPID(pid int) int {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return 0
		}
		// The command name in field 2 may hold spaces and parentheses, so
		// parse from the last ')': state, then ppid.
		s := string(data)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			return 0
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 2 {
			return 0
		}
		ppid, _ := strconv.Atoi(f[1])
		return ppid
	}
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}
