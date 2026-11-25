package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

func TestStackModeFor(t *testing.T) {
	if got := stackModeFor(worktree.TypePRReview); got != config.ReviewStack {
		t.Errorf("pr-review worktree mode = %v, want review", got)
	}
	if got := stackModeFor(worktree.TypeFeature); got != config.WorkStack {
		t.Errorf("feature worktree mode = %v, want work", got)
	}
}

func TestResolveStackUsesFlagAndConfig(t *testing.T) {
	dir := t.TempDir()
	flagStack := filepath.Join(dir, "flag-stack")
	cfgStack := filepath.Join(dir, "cfg-stack")
	for _, p := range []string{flagStack, cfgStack} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldCfg, oldFlag := cfg, stackFlag
	t.Cleanup(func() { cfg, stackFlag = oldCfg, oldFlag })
	cfg = &config.Config{Stack: cfgStack}

	stackFlag = ""
	ref, err := resolveStack(config.WorkStack, "app")
	if err != nil || ref == nil || ref.Launcher != cfgStack {
		t.Fatalf("without flag: %+v, %v; want config stack", ref, err)
	}

	stackFlag = flagStack
	ref, err = resolveStack(config.WorkStack, "app")
	if err != nil || ref == nil || ref.Launcher != flagStack {
		t.Fatalf("with flag: %+v, %v; want flag stack", ref, err)
	}

	stackFlag = filepath.Join(dir, "missing")
	if _, err = resolveStack(config.WorkStack, "app"); err == nil || !strings.Contains(err.Error(), "--stack") {
		t.Fatalf("missing flag stack: error = %v, want one naming --stack", err)
	}
}

func TestStackFlagRegisteredOnLaunchCommands(t *testing.T) {
	for _, c := range []*cobra.Command{reviewCmd, reviewResumeCmd, workNewCmd, workResumeCmd} {
		if c.Flags().Lookup("stack") == nil {
			t.Errorf("%q has no --stack flag", c.CommandPath())
		}
	}
}
