package agent

import "testing"

func TestStackCommands(t *testing.T) {
	plain := &Stack{Launcher: "/nix/store/x/bin/zen-review", Dir: "/zen/stacks/app-pr-1"}
	spaced := &Stack{Launcher: "/My Stacks/bin/launch", Dir: "/zen home/stacks/app-pr-1"}
	const prefix = "AGENT_STACKS_DIR='/zen/stacks/app-pr-1' '/nix/store/x/bin/zen-review'"

	tests := []struct {
		name string
		kind Kind
		st   *Stack
		got  func(Agent) string
		want string
	}{
		{"claude start", Claude, plain,
			func(a Agent) string { return a.StartCommand("review it", "opus") },
			prefix + " --model 'opus' 'review it'"},
		{"claude start no model no prompt", Claude, plain,
			func(a Agent) string { return a.StartCommand("", "") },
			prefix},
		{"claude resume", Claude, plain,
			func(a Agent) string { return a.ResumeCommand("abc-123", "opus") },
			prefix + " --model 'opus' --resume 'abc-123'"},
		{"codex start", Codex, plain,
			func(a Agent) string { return a.StartCommand("review it", "gpt-5-codex") },
			prefix + " -m 'gpt-5-codex' 'review it'"},
		{"codex resume", Codex, plain,
			func(a Agent) string { return a.ResumeCommand("abc-123", "gpt-5-codex") },
			prefix + " resume 'abc-123' -m 'gpt-5-codex'"},
		{"prompt with single quotes", Claude, plain,
			func(a Agent) string { return a.StartCommand("it's a 'test'", "") },
			prefix + ` 'it'\''s a '\''test'\'''`},
		{"stack path with spaces", Claude, spaced,
			func(a Agent) string { return a.StartCommand("p", "") },
			"AGENT_STACKS_DIR='/zen home/stacks/app-pr-1' '/My Stacks/bin/launch' 'p'"},
		{"stack path with a single quote", Codex, &Stack{Launcher: "/o'brien/stack", Dir: "/d"},
			func(a Agent) string { return a.ResumeCommand("s", "") },
			`AGENT_STACKS_DIR='/d' '/o'\''brien/stack' resume 's'`},
		{"nil stack claude start", Claude, nil,
			func(a Agent) string { return a.StartCommand("p", "m") },
			"claude --model 'm' 'p'"},
		{"nil stack claude resume", Claude, nil,
			func(a Agent) string { return a.ResumeCommand("s", "") },
			"claude --resume 's'"},
		{"nil stack codex start", Codex, nil,
			func(a Agent) string { return a.StartCommand("p", "m") },
			"codex -m 'm' 'p'"},
		{"nil stack codex resume", Codex, nil,
			func(a Agent) string { return a.ResumeCommand("s", "m") },
			"codex resume 's' -m 'm'"},
		{"empty stack launcher", Claude, &Stack{Dir: "/d"},
			func(a Agent) string { return a.StartCommand("p", "") },
			"claude 'p'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := New(tt.kind, "").WithStack(tt.st)
			if got := tt.got(ag); got != tt.want {
				t.Errorf("command = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWithStackKeepsBinAndLeavesOriginal(t *testing.T) {
	for _, kind := range AllKinds() {
		t.Run(string(kind), func(t *testing.T) {
			orig := New(kind, "/custom/real-bin")
			stacked := orig.WithStack(&Stack{Launcher: "/s/launch", Dir: "/d"})

			if got := stacked.Bin(); got != "/custom/real-bin" {
				t.Errorf("stacked Bin() = %q, want the real agent binary", got)
			}
			if got := orig.StartCommand("", ""); got != "/custom/real-bin" {
				t.Errorf("WithStack mutated the original agent: StartCommand() = %q", got)
			}
			if stacked.Kind() != kind {
				t.Errorf("stacked Kind() = %q, want %q", stacked.Kind(), kind)
			}
		})
	}
}
