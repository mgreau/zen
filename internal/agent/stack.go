package agent

// Stack is an executable launcher that stands in for the agent binary when zen
// starts or resumes a session. It brings its own harness and plugins and
// passes every argument through to the harness.
type Stack struct {
	// Launcher is the resolved path of the stack executable.
	Launcher string
	// Dir is exported as AGENT_STACKS_DIR so the stack stages its plugins
	// outside the worktree. Staging inside the worktree would leave files that
	// zen's removal check treats as local changes.
	Dir string
}

// launchPrefix returns the command word(s) that start the agent: bin for a
// nil Stack, otherwise the stack launcher with its staging directory.
func (s *Stack) launchPrefix(bin string) string {
	if s == nil || s.Launcher == "" {
		return bin
	}
	return "AGENT_STACKS_DIR=" + shellQuote(s.Dir) + " " + shellQuote(s.Launcher)
}
