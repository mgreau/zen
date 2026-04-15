package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/terminal"
	"github.com/mgreau/zen/internal/ui"
	wt "github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var workCmd = &cobra.Command{
	Use:   "work",
	Short: "Show feature work in progress",
	RunE:  runWork,
}

var workNewCmd = &cobra.Command{
	Use:   "new <repo> <branch> [context]",
	Short: "Create a new feature worktree and open in iTerm2",
	Long: `Create a new feature worktree from origin/main and open it in a new iTerm2 tab.

The branch will be prefixed with mgreau/ per naming convention.
Optionally provide a context string to use as the initial Claude prompt.`,
	Args: cobra.RangeArgs(2, 3),
	RunE: runWorkNew,
}

var workDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a feature worktree by name or path",
	Long: `Delete a feature worktree and its Claude session files.

Accepts a worktree name (e.g., mono-factory-v2-agentic) or full path.
Shows a summary of what will be removed before confirming.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkDelete,
}

var workResumeCmd = &cobra.Command{
	Use:   "resume <name>",
	Short: "Resume a feature work session in a new iTerm2 tab",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkResume,
}

var (
	workNewNoITerm  bool
	workNewModel    string
	workDeleteForce bool
)

func init() {
	workNewCmd.Flags().BoolVar(&workNewNoITerm, "no-terminal", false, "Create worktree only, don't open terminal tab")
	workNewCmd.Flags().StringVarP(&workNewModel, "model", "m", "", "Claude model to use (e.g., sonnet, opus, haiku)")
	workDeleteCmd.Flags().BoolVarP(&workDeleteForce, "force", "f", false, "Skip confirmation")
	addResumeFlags(workResumeCmd)
	workCmd.AddCommand(workNewCmd)
	workCmd.AddCommand(workDeleteCmd)
	workCmd.AddCommand(workResumeCmd)
	rootCmd.AddCommand(workCmd)
}

// WorkEntry holds enriched feature work data for JSON output.
type WorkEntry struct {
	wt.Worktree
	HasSession bool `json:"has_active_session"`
}

func runWork(cmd *cobra.Command, args []string) error {
	wts, err := wt.ListAll(cfg)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	var features []wt.Worktree
	for _, w := range wts {
		if w.Type == wt.TypeFeature {
			features = append(features, w)
		}
	}

	if jsonFlag {
		var entries []WorkEntry
		for _, f := range features {
			entries = append(entries, WorkEntry{
				Worktree:   f,
				HasSession: session.HasActiveSession(f.Path),
			})
		}
		printJSON(entries)
		return nil
	}

	// Human-readable output
	fmt.Println()
	fmt.Println(ui.BoldText("Feature Work"))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	if len(features) == 0 {
		fmt.Println("No feature worktrees found.")
		return nil
	}

	fmt.Printf("%-12s %-45s %s\n", "Repo", "Name", "Session")
	fmt.Printf("%-12s %-45s %s\n", "────────────", "─────────────────────────────────────────────", "───────")

	home := homeDir()
	for _, f := range features {
		sessionIndicator := ""
		if session.HasActiveSession(f.Path) {
			sessionIndicator = ui.GreenText("●")
		}

		fmt.Printf("%-12s %-45s %s\n", f.Repo, ui.Truncate(f.Name, 43), sessionIndicator)
		fmt.Printf("             %s\n", ui.DimText(ui.ShortenHome(f.Path, home)))
	}

	fmt.Println()
	ui.Hint("● = Active Claude session")
	fmt.Println()
	return nil
}

func runWorkNew(cmd *cobra.Command, args []string) error {
	repo := args[0]
	branch := args[1]
	context := ""
	if len(args) == 3 {
		context = args[2]
	}

	// Validate repo exists in config
	basePath := cfg.RepoBasePath(repo)
	if basePath == "" {
		return fmt.Errorf("unknown repo %q — check ~/.zen/config.yaml", repo)
	}

	// Construct paths
	originPath := filepath.Join(basePath, repo)
	worktreeName := fmt.Sprintf("%s-%s", repo, branch)
	worktreePath := filepath.Join(basePath, worktreeName)
	gitBranch := fmt.Sprintf("mgreau/%s", branch)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s\n  Resume with: zen work resume %s", worktreePath, branch)
	}

	// Create worktree under lock
	wt.GitMu.Lock()

	ui.LogInfo(fmt.Sprintf("Fetching origin/main in %s...", repo))
	fetchCmd := exec.Command("git", "fetch", "origin", "main")
	fetchCmd.Dir = originPath
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		wt.GitMu.Unlock()
		return fmt.Errorf("git fetch: %w: %s", err, string(out))
	}

	ui.LogInfo(fmt.Sprintf("Creating worktree %s (branch %s)...", worktreeName, gitBranch))
	// Use --no-checkout + separate checkout to avoid "Could not write new index file"
	// on large repos (13K+ files). The two-step approach handles the index write reliably.
	wtCmd := exec.Command("git", "worktree", "add", "--no-checkout", worktreePath, "-b", gitBranch, "origin/main")
	wtCmd.Dir = originPath
	if out, err := wtCmd.CombinedOutput(); err != nil {
		wt.CleanupFailedAdd(originPath, worktreePath, gitBranch)
		wt.GitMu.Unlock()
		return fmt.Errorf("git worktree add: %w: %s", err, string(out))
	}

	checkoutCmd := exec.Command("git", "checkout")
	checkoutCmd.Dir = worktreePath
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		wt.CleanupFailedAdd(originPath, worktreePath, gitBranch)
		wt.GitMu.Unlock()
		return fmt.Errorf("git checkout in worktree: %w: %s", err, string(out))
	}

	// Clean stale index.lock
	lockFile := filepath.Join(originPath, ".git", "worktrees", worktreeName, "index.lock")
	os.Remove(lockFile)

	wt.GitMu.Unlock()

	home := homeDir()
	shortPath := ui.ShortenHome(worktreePath, home)

	fmt.Println()
	ui.LogSuccess(fmt.Sprintf("Created worktree: %s", shortPath))
	fmt.Printf("  Branch: %s\n", ui.CyanText(gitBranch))

	if workNewModel != "" {
		fmt.Printf("  Model:  %s\n", ui.CyanText(workNewModel))
	}

	if workNewNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Open manually:"))
		modelFlag := ""
		if workNewModel != "" {
			modelFlag = fmt.Sprintf(" --model %s", workNewModel)
		}
		if context != "" {
			fmt.Printf("  cd %s && %s%s %q\n", worktreePath, cfg.ClaudeBin, modelFlag, context)
		} else {
			fmt.Printf("  cd %s && %s%s\n", worktreePath, cfg.ClaudeBin, modelFlag)
		}
		return nil
	}

	// Open terminal tab
	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}

	if context != "" {
		if err := term.OpenTabWithClaude(worktreePath, context, cfg.ClaudeBin, workNewModel); err != nil {
			return fmt.Errorf("opening %s tab: %w", term.Name(), err)
		}
	} else {
		cmd := cfg.ClaudeBin
		if workNewModel != "" {
			cmd += fmt.Sprintf(" --model %s", workNewModel)
		}
		if err := term.OpenTab(worktreePath, cmd); err != nil {
			return fmt.Errorf("opening %s tab: %w", term.Name(), err)
		}
	}

	ui.LogSuccess(fmt.Sprintf("%s tab opened", term.Name()))
	fmt.Println()
	return nil
}

func runWorkDelete(cmd *cobra.Command, args []string) error {
	target := args[0]

	// Find matching worktree by name first, then by path
	wts, err := wt.ListAll(cfg)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	var match *wt.Worktree
	for _, w := range wts {
		if w.Name == target {
			w := w
			match = &w
			break
		}
	}
	if match == nil {
		// Try absolute path match
		absTarget := target
		if !filepath.IsAbs(absTarget) {
			if abs, err := filepath.Abs(absTarget); err == nil {
				absTarget = abs
			}
		}
		for _, w := range wts {
			if w.Path == absTarget {
				w := w
				match = &w
				break
			}
		}
	}

	if match == nil {
		return fmt.Errorf("no worktree found matching %q", target)
	}

	home := homeDir()
	shortPath := ui.ShortenHome(match.Path, home)

	// Gather info for summary
	sessions, _ := session.FindSessions(match.Path)
	age := ""
	if days, err := wt.AgeDays(match.Path); err == nil {
		if days == 0 {
			if hours, herr := wt.AgeHours(match.Path); herr == nil {
				age = fmt.Sprintf("%dh", hours)
			}
		} else {
			age = fmt.Sprintf("%dd", days)
		}
	}

	// Show summary
	fmt.Println()
	fmt.Printf("  Worktree:  %s\n", ui.CyanText(match.Name))
	fmt.Printf("  Branch:    %s\n", match.Branch)
	fmt.Printf("  Path:      %s\n", shortPath)
	if age != "" {
		fmt.Printf("  Age:       %s\n", age)
	}
	if len(sessions) > 0 {
		fmt.Printf("  Sessions:  %d (%s)\n", len(sessions), sessions[0].SizeStr)
	}
	fmt.Println()

	if !workDeleteForce {
		fmt.Print("  Delete? [y/N]: ")
		var resp string
		fmt.Scanln(&resp)
		if resp != "y" && resp != "Y" {
			fmt.Println("  Cancelled.")
			return nil
		}
		fmt.Println()
	}

	// Remove git worktree
	basePath := cfg.RepoBasePath(match.Repo)
	originPath := filepath.Join(basePath, match.Repo)

	removeCmd := exec.Command("git", "worktree", "remove", match.Path, "--force")
	removeCmd.Dir = originPath
	if out, err := removeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, string(out))
	}
	ui.LogSuccess("Removed worktree")

	// Clean up Claude session files
	if len(sessions) > 0 {
		sessionDir := session.ProjectDir(match.Path)
		if sessionDir != "" {
			if err := os.RemoveAll(sessionDir); err != nil {
				fmt.Printf("  %s clean session files: %v\n", ui.YellowText("Warning:"), err)
			} else {
				ui.LogSuccess(fmt.Sprintf("Cleaned %d session file(s)", len(sessions)))
			}
		}
	}

	fmt.Println()
	return nil
}
