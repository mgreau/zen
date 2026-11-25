package cmd

import (
	"reflect"
	"testing"

	"github.com/mgreau/zen/internal/agentdeck/agentdecktest"
	"github.com/mgreau/zen/internal/config"
)

func TestForgetTerminalSession(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	calls := agentdecktest.Install(t, "/src/zen-pr-32")

	cfg = &config.Config{Terminal: "iterm"}
	forgetTerminalSession("/src/zen-pr-32")
	if got := calls(); got != nil {
		t.Fatalf("terminal iterm called agent-deck: %q", got)
	}

	cfg = &config.Config{Terminal: "agent-deck"}
	forgetTerminalSession("/src/zen-pr-32")
	if got := calls(); !reflect.DeepEqual(got, agentdecktest.Removed) {
		t.Errorf("agent-deck calls = %q, want %q", got, agentdecktest.Removed)
	}
}
