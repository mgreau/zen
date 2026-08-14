package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/migrate"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/terminal"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

// resumeFlags holds the shared flags for resume subcommands.
var (
	resumeSession int
	resumeList    bool
	resumeNoITerm bool
	resumeModel   string
	resumeMigrate bool
)

// resumeWorktree handles the core resume logic for a matched worktree.
func resumeWorktree(wt worktree.Worktree, cmdName string, t terminal.Terminal) error {
	ag, err := resolveAgent()
	if err != nil {
		return err
	}

	// Find agent sessions
	sessions, err := ag.FindSessions(wt.Path)
	noSessions := err != nil || len(sessions) == 0

	// JSON output
	if jsonFlag {
		printJSON(struct {
			Worktree string            `json:"worktree"`
			Name     string            `json:"name"`
			Sessions []session.Session `json:"sessions"`
		}{
			Worktree: wt.Path,
			Name:     wt.Name,
			Sessions: sessions,
		})
		return nil
	}

	// List mode
	if resumeList {
		home := os.Getenv("HOME")
		fmt.Println()
		fmt.Printf("%s\n", ui.BoldText(fmt.Sprintf("%s Sessions for %s", ag.Kind(), ui.CyanText(wt.Name))))
		fmt.Println(ui.DimText(ui.ShortenHome(wt.Path, home)))
		fmt.Println("═══════════════════════════════════════════════════════════════")
		fmt.Println()

		if noSessions {
			fmt.Println("  No sessions yet.")
		} else {
			for i, s := range sessions {
				marker := ""
				if i == 0 {
					marker = " " + ui.GreenText("(most recent)")
				}
				fmt.Printf("  %s %s%s\n", ui.BoldText(fmt.Sprintf("[%d]", i+1)), ui.CyanText(s.ID), marker)
				fmt.Printf("      %s\n", ui.DimText(fmt.Sprintf("Modified: %s  Size: %s", s.ModHuman, s.SizeStr)))
			}
		}
		fmt.Println()
		ui.Hint(fmt.Sprintf("Resume with: %s --session N", cmdName))
		fmt.Println()
		return nil
	}

	// No existing sessions for this agent — offer to migrate the other
	// agent's work before falling back to a fresh session.
	if noSessions {
		migrated, err := maybeMigrateSession(wt, ag)
		if err != nil {
			return err
		}
		if migrated == nil {
			return openNewSession(wt, t, ag)
		}
		sessions = []session.Session{*migrated}
	}

	// Pick session
	targetIdx := 0
	if resumeSession > 0 {
		targetIdx = resumeSession - 1
		if targetIdx >= len(sessions) || targetIdx < 0 {
			return fmt.Errorf("session index %d out of range (1-%d)", resumeSession, len(sessions))
		}
	}

	s := sessions[targetIdx]
	home := os.Getenv("HOME")
	shortPath := ui.ShortenHome(wt.Path, home)

	resumeCmd := ag.ResumeCommand(s.ID, resumeModel)

	// No-iTerm mode
	if resumeNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Resume command:"))
		fmt.Printf("  cd %s && %s\n", wt.Path, resumeCmd)
		fmt.Println()
		fmt.Println(ui.DimText(fmt.Sprintf("Worktree: %s", shortPath)))
		fmt.Println(ui.DimText(fmt.Sprintf("Session:  %s (%s)", s.ModHuman, s.SizeStr)))
		return nil
	}

	// Open in terminal
	fmt.Println()
	fmt.Println(ui.BoldText(fmt.Sprintf("Resuming %s session in new %s tab", ag.Kind(), t.Name())))
	fmt.Printf("  Worktree: %s\n", ui.CyanText(wt.Name))
	fmt.Printf("  Path:     %s\n", ui.DimText(shortPath))
	fmt.Printf("  Session:  %s\n", ui.DimText(s.ID))
	fmt.Printf("  Modified: %s\n", ui.DimText(fmt.Sprintf("%s (%s)", s.ModHuman, s.SizeStr)))
	if resumeModel != "" {
		fmt.Printf("  Model:    %s\n", ui.CyanText(resumeModel))
	}
	fmt.Println()

	if err := t.OpenTab(wt.Path, resumeCmd); err != nil {
		return fmt.Errorf("opening %s tab: %w", t.Name(), err)
	}

	ui.LogSuccess(fmt.Sprintf("%s tab opened", t.Name()))
	return nil
}

// maybeMigrateSession offers to carry the other agent's most recent session
// over to ag when ag has none for the worktree. It returns the migrated
// session, or nil when there is nothing to migrate or the user declined.
// With --migrate the prompt is skipped.
//
// Directions differ by design: Codex ships its own Claude-session importer
// (driven via app-server JSON-RPC), while Claude Code has none, so zen
// translates the Codex rollout into a Claude session file itself.
func maybeMigrateSession(wt worktree.Worktree, ag agent.Agent) (*session.Session, error) {
	otherKind := agent.Claude
	if ag.Kind() == agent.Claude {
		otherKind = agent.Codex
	}
	other := cfg.NewAgent(string(otherKind))
	otherSessions, err := other.FindSessions(wt.Path)
	if err != nil || len(otherSessions) == 0 {
		return nil, nil
	}
	src := otherSessions[0]

	if !resumeMigrate {
		fmt.Printf("No %s session for this worktree, but %d %s session(s) exist.\n", ag.Kind(), len(otherSessions), other.Kind())
		fmt.Printf("Migrate the most recent one (%s, %s) to %s and resume it? [Y/n]: ", src.ModHuman, src.SizeStr, ag.Kind())
		var resp string
		fmt.Scanln(&resp)
		resp = strings.ToLower(strings.TrimSpace(resp))
		if resp == "n" || resp == "no" {
			return nil, nil
		}
	}

	ui.LogInfo(fmt.Sprintf("Migrating %s session %s to %s...", other.Kind(), src.ID, ag.Kind()))

	var newID string
	switch ag.Kind() {
	case agent.Codex:
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		newID, err = migrate.ClaudeToCodex(ctx, src.Path, ag.Bin())
	default:
		newID, err = migrate.CodexToClaude(src.Path, wt.Path)
	}
	if err != nil {
		return nil, fmt.Errorf("migrating %s session to %s: %w", other.Kind(), ag.Kind(), err)
	}
	ui.LogSuccess(fmt.Sprintf("Migrated to %s session %s", ag.Kind(), newID))

	// Re-discover so the resume flow gets real file metadata.
	if newSessions, err := ag.FindSessions(wt.Path); err == nil {
		for _, s := range newSessions {
			if s.ID == newID {
				return &s, nil
			}
		}
	}
	return &session.Session{ID: newID}, nil
}

// openNewSession starts a new agent session in a new terminal tab.
// For PR worktrees, it starts with a review prompt; for others, it starts plain.
func openNewSession(wt worktree.Worktree, t terminal.Terminal, ag agent.Agent) error {
	home := os.Getenv("HOME")
	shortPath := ui.ShortenHome(wt.Path, home)

	initialPrompt := ""
	action := "Starting new session"
	if wt.Type == worktree.TypePRReview {
		ensureReviewPrompt(ag)
		initialPrompt = ag.ReviewPrompt(wt.Path)
		action = "Starting PR review"
	}

	launchCmd := ag.StartCommand(initialPrompt, resumeModel)

	if resumeNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Start command:"))
		fmt.Printf("  cd %s && %s\n", wt.Path, launchCmd)
		fmt.Println()
		fmt.Println(ui.DimText(fmt.Sprintf("Worktree: %s", shortPath)))
		return nil
	}

	fmt.Println()
	fmt.Println(ui.BoldText(fmt.Sprintf("%s in new %s tab", action, t.Name())))
	fmt.Printf("  Worktree: %s\n", ui.CyanText(wt.Name))
	fmt.Printf("  Path:     %s\n", ui.DimText(shortPath))
	if resumeModel != "" {
		fmt.Printf("  Model:    %s\n", ui.CyanText(resumeModel))
	}
	fmt.Println()

	if err := t.OpenTab(wt.Path, launchCmd); err != nil {
		return fmt.Errorf("opening %s tab: %w", t.Name(), err)
	}

	ui.LogSuccess(fmt.Sprintf("%s tab opened", t.Name()))
	return nil
}

// findWorktreeByPR finds a PR review worktree by PR number.
func findWorktreeByPR(prNumber int) (*worktree.Worktree, error) {
	wts, err := worktree.ListAll(cfg)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	for _, wt := range wts {
		if wt.Type == worktree.TypePRReview && wt.PRNumber == prNumber {
			return &wt, nil
		}
	}
	return nil, &noWorktreeError{prNumber: prNumber}
}

// noWorktreeError is returned when no worktree exists for a PR.
type noWorktreeError struct {
	prNumber int
}

func (e *noWorktreeError) Error() string {
	return fmt.Sprintf("no PR review worktree for #%d", e.prNumber)
}

// findWorktreeByName finds a feature worktree by name/term search.
func findWorktreeByName(term string) (*worktree.Worktree, error) {
	wts, err := worktree.ListAll(cfg)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	termLower := strings.ToLower(term)
	var matches []worktree.Worktree
	for _, wt := range wts {
		if wt.Type != worktree.TypeFeature {
			continue
		}
		nameLower := strings.ToLower(wt.Name)
		branchLower := strings.ToLower(wt.Branch)
		if strings.Contains(nameLower, termLower) || (wt.Branch != "" && strings.Contains(branchLower, termLower)) {
			matches = append(matches, wt)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no feature worktree matching %q\n  Create with: zen work new <repo> %s", term, term)
	}

	if len(matches) > 1 && !jsonFlag && !resumeList {
		ui.LogWarn(fmt.Sprintf("Multiple worktrees match %q:", term))
		home := os.Getenv("HOME")
		for _, m := range matches {
			fmt.Printf("  - %s\n", ui.ShortenHome(m.Path, home))
		}
		fmt.Println()
		ui.LogInfo("Using first match. Be more specific to pick a different one.")
		fmt.Println()
	}

	return &matches[0], nil
}

// addResumeFlags adds the shared --session, --list, --no-iterm, --model flags to a cobra command.
func addResumeFlags(cmd *cobra.Command) {
	cmd.Flags().IntVarP(&resumeSession, "session", "s", 0, "Resume Nth session instead of most recent (1-based)")
	cmd.Flags().BoolVarP(&resumeList, "list", "l", false, "List available sessions without resuming")
	cmd.Flags().BoolVar(&resumeNoITerm, "no-terminal", false, "Print the resume command instead of opening terminal")
	cmd.Flags().StringVarP(&resumeModel, "model", "m", "", "Model to use (agent-specific, e.g. opus or gpt-5-codex)")
	cmd.Flags().BoolVar(&resumeMigrate, "migrate", false, "When the agent has no session here, migrate the other agent's most recent one without prompting")
	addAgentFlag(cmd)
}

// runReviewResume handles `zen review resume <pr-number>`.
func runReviewResume(cmd *cobra.Command, args []string) error {
	prNumber, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", args[0], err)
	}

	wt, err := findWorktreeByPR(prNumber)
	if err != nil {
		var nwErr *noWorktreeError
		if errors.As(err, &nwErr) {
			fmt.Printf("No worktree found for PR #%d. Create one? [Y/n]: ", prNumber)
			var resp string
			fmt.Scanln(&resp)
			resp = strings.ToLower(strings.TrimSpace(resp))
			if resp == "n" || resp == "no" {
				return nil
			}
			return runReview(cmd, args)
		}
		return err
	}

	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}
	return resumeWorktree(*wt, fmt.Sprintf("zen review resume %d", prNumber), term)
}

// runWorkResume handles `zen work resume <name>`.
func runWorkResume(cmd *cobra.Command, args []string) error {
	wt, err := findWorktreeByName(args[0])
	if err != nil {
		return err
	}

	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}
	return resumeWorktree(*wt, fmt.Sprintf("zen work resume %s", args[0]), term)
}
