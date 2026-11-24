package agent

import (
	"os"
	"testing"
)

func TestDescendsFrom(t *testing.T) {
	parents := map[int]int{40: 30, 30: 20, 20: 1, 50: 1, 60: 61, 61: 60}
	parent := func(pid int) int { return parents[pid] }
	cases := []struct {
		pid   int
		roots []int
		want  bool
	}{
		{40, []int{20}, true},
		{40, []int{40}, true},
		{50, []int{20}, false},
		{40, nil, false},
		{60, []int{20}, false}, // a cycle ends the walk
	}
	for _, c := range cases {
		if got := descendsFrom(c.pid, c.roots, parent); got != c.want {
			t.Errorf("descendsFrom(%d, %v) = %v, want %v", c.pid, c.roots, got, c.want)
		}
	}
}

func TestParentPIDOfSelf(t *testing.T) {
	if got := parentPID(os.Getpid()); got != os.Getppid() {
		t.Errorf("parentPID(self) = %d, want %d", got, os.Getppid())
	}
	if !DescendsFrom(os.Getpid(), []int{os.Getppid()}) {
		t.Error("DescendsFrom(self, parent) = false")
	}
}
