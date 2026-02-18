package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mgreau/zen/internal/iterm"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

// resumeFlags holds the shared flags for resume subcommands.
var (
	resumeSession int
	resumeList    bool
	resumeNoITerm bool
)

// resumeWorktree handles the core resume logic for a matched worktree.
func resumeWorktree(wt worktree.Worktree, cmdName string) error {
	// Find Claude sessions
	sessions, err := session.FindSessions(wt.Path)
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
		fmt.Printf("%s\n", ui.BoldText(fmt.Sprintf("Claude Sessions for %s", ui.CyanText(wt.Name))))
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

	// No existing sessions — start a new Claude session
	if noSessions {
		return openNewSession(wt)
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

	// No-iTerm mode
	if resumeNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Resume command:"))
		fmt.Printf("  cd %s && %s --resume %s\n", wt.Path, cfg.ClaudeBin, s.ID)
		fmt.Println()
		fmt.Println(ui.DimText(fmt.Sprintf("Worktree: %s", shortPath)))
		fmt.Println(ui.DimText(fmt.Sprintf("Session:  %s (%s)", s.ModHuman, s.SizeStr)))
		return nil
	}

	// Open in iTerm2
	fmt.Println()
	fmt.Println(ui.BoldText("Resuming Claude session in new iTerm2 tab"))
	fmt.Printf("  Worktree: %s\n", ui.CyanText(wt.Name))
	fmt.Printf("  Path:     %s\n", ui.DimText(shortPath))
	fmt.Printf("  Session:  %s\n", ui.DimText(s.ID))
	fmt.Printf("  Modified: %s\n", ui.DimText(fmt.Sprintf("%s (%s)", s.ModHuman, s.SizeStr)))
	fmt.Println()

	if err := iterm.OpenTabWithResume(wt.Path, s.ID, cfg.ClaudeBin); err != nil {
		return fmt.Errorf("opening iTerm tab: %w", err)
	}

	ui.LogSuccess("iTerm2 tab opened")
	return nil
}

// openNewSession starts a new Claude session in a new iTerm tab.
// For PR worktrees, it starts with /review-pr. For others, it starts plain claude.
func openNewSession(wt worktree.Worktree) error {
	home := os.Getenv("HOME")
	shortPath := ui.ShortenHome(wt.Path, home)

	initialPrompt := "/review-pr"
	action := "Starting PR review"
	if wt.Type != worktree.TypePRReview {
		initialPrompt = ""
		action = "Starting new session"
	}

	if resumeNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Start command:"))
		if initialPrompt != "" {
			fmt.Printf("  cd %s && %s %q\n", wt.Path, cfg.ClaudeBin, initialPrompt)
		} else {
			fmt.Printf("  cd %s && %s\n", wt.Path, cfg.ClaudeBin)
		}
		fmt.Println()
		fmt.Println(ui.DimText(fmt.Sprintf("Worktree: %s", shortPath)))
		return nil
	}

	fmt.Println()
	fmt.Println(ui.BoldText(fmt.Sprintf("%s in new iTerm2 tab", action)))
	fmt.Printf("  Worktree: %s\n", ui.CyanText(wt.Name))
	fmt.Printf("  Path:     %s\n", ui.DimText(shortPath))
	fmt.Println()

	var err error
	if initialPrompt != "" {
		err = iterm.OpenTabWithClaude(wt.Path, initialPrompt, cfg.ClaudeBin)
	} else {
		err = iterm.OpenTab(wt.Path, cfg.ClaudeBin)
	}
	if err != nil {
		return fmt.Errorf("opening iTerm tab: %w", err)
	}

	ui.LogSuccess("iTerm2 tab opened")
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
	return nil, fmt.Errorf("no PR review worktree for #%d\n  Create with: zen review %d", prNumber, prNumber)
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

// addResumeFlags adds the shared --session, --list, --no-iterm flags to a cobra command.
func addResumeFlags(cmd *cobra.Command) {
	cmd.Flags().IntVarP(&resumeSession, "session", "s", 0, "Resume Nth session instead of most recent (1-based)")
	cmd.Flags().BoolVarP(&resumeList, "list", "l", false, "List available sessions without resuming")
	cmd.Flags().BoolVar(&resumeNoITerm, "no-iterm", false, "Print the resume command instead of opening iTerm2")
}

// runReviewResume handles `zen review resume <pr-number>`.
func runReviewResume(cmd *cobra.Command, args []string) error {
	prNumber, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", args[0], err)
	}

	wt, err := findWorktreeByPR(prNumber)
	if err != nil {
		return err
	}

	return resumeWorktree(*wt, fmt.Sprintf("zen review resume %d", prNumber))
}

// runWorkResume handles `zen work resume <name>`.
func runWorkResume(cmd *cobra.Command, args []string) error {
	wt, err := findWorktreeByName(args[0])
	if err != nil {
		return err
	}

	return resumeWorktree(*wt, fmt.Sprintf("zen work resume %s", args[0]))
}
