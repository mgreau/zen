package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mgreau/zen/internal/agent"
)

// StackMode selects which stack keys apply to a session.
type StackMode int

const (
	// ReviewStack is a PR review session: review_stack is consulted before stack.
	ReviewStack StackMode = iota
	// WorkStack is a feature work session: only stack is consulted.
	WorkStack
)

// StackRef is a resolved agent stack: the configured value, where it came
// from, and the executable it names.
type StackRef struct {
	Value    string // as configured or passed, e.g. "~/stacks/review"
	Source   string // "--stack", or the config key that supplied Value
	Launcher string // absolute path of the executable
}

// StacksDir is where a worktree's stack stages its plugins, outside the
// worktree so they never count as local changes there.
func StacksDir(worktreePath string) string {
	return filepath.Join(Dir(), "stacks", filepath.Base(worktreePath))
}

// ResolveStack picks the stack for a session and checks that it can run. flag
// is the --stack value, which beats every config key. It returns nil, nil when
// nothing is configured.
//
// Review: flag, repos.<repo>.review_stack, repos.<repo>.stack, review_stack,
// stack. Work: flag, repos.<repo>.stack, stack.
func (c *Config) ResolveStack(mode StackMode, repo, flag string) (*StackRef, error) {
	rc := c.Repos[repo]
	type candidate struct{ value, source string }
	cands := []candidate{{flag, "--stack"}}
	if mode == ReviewStack {
		cands = append(cands, candidate{rc.ReviewStack, fmt.Sprintf("repos.%s.review_stack", repo)})
	}
	cands = append(cands, candidate{rc.Stack, fmt.Sprintf("repos.%s.stack", repo)})
	if mode == ReviewStack {
		cands = append(cands, candidate{c.ReviewStack, "review_stack"})
	}
	cands = append(cands, candidate{c.Stack, "stack"})

	for _, cand := range cands {
		value := strings.TrimSpace(cand.value)
		if value == "" {
			continue
		}
		launcher, err := findStackExecutable(value)
		if err != nil {
			return nil, fmt.Errorf("stack %q (from %s): %w", value, cand.source, err)
		}
		return &StackRef{Value: value, Source: cand.source, Launcher: launcher}, nil
	}
	return nil, nil
}

// Apply returns ag launching through the stack, staging plugins under
// StacksDir(worktreePath). A nil ref returns ag unchanged.
func (r *StackRef) Apply(ag agent.Agent, worktreePath string) agent.Agent {
	if r == nil {
		return ag
	}
	return ag.WithStack(&agent.Stack{Launcher: r.Launcher, Dir: StacksDir(worktreePath)})
}

// findStackExecutable resolves value to an absolute executable path. A value
// containing a path separator or starting with "~" is a path (with "~"
// expanded); anything else is looked up on PATH.
func findStackExecutable(value string) (string, error) {
	if !strings.ContainsRune(value, filepath.Separator) && !strings.HasPrefix(value, "~") {
		found, err := exec.LookPath(value)
		if err != nil {
			return "", fmt.Errorf("not found on PATH")
		}
		return filepath.Abs(found)
	}

	path := value
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = filepath.Join(os.Getenv("HOME"), strings.TrimPrefix(path, "~"))
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s does not exist", path)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not an executable", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s is not executable", path)
	}
	return path, nil
}
