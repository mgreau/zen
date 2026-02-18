package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mgreau/zen/internal/iterm"
	"github.com/mgreau/zen/internal/session"
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
	Use:   "delete <path>",
	Short: "Delete a feature worktree by path or name",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkDelete,
}

var workResumeCmd = &cobra.Command{
	Use:   "resume <name>",
	Short: "Resume a feature work session in a new iTerm2 tab",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkResume,
}

var (
	workNewNoITerm  bool
	workDeleteForce bool
)

func init() {
	workNewCmd.Flags().BoolVar(&workNewNoITerm, "no-iterm", false, "Create worktree only, don't open iTerm2 tab")
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
	wtCmd := exec.Command("git", "worktree", "add", worktreePath, "-b", gitBranch, "origin/main")
	wtCmd.Dir = originPath
	if out, err := wtCmd.CombinedOutput(); err != nil {
		wt.GitMu.Unlock()
		return fmt.Errorf("git worktree add: %w: %s", err, string(out))
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

	if workNewNoITerm {
		fmt.Println()
		fmt.Println(ui.BoldText("Open manually:"))
		if context != "" {
			fmt.Printf("  cd %s && %s %q\n", worktreePath, cfg.ClaudeBin, context)
		} else {
			fmt.Printf("  cd %s && %s\n", worktreePath, cfg.ClaudeBin)
		}
		return nil
	}

	// Open iTerm tab
	if context == "" {
		context = "/review-pr"
	}
	if err := iterm.OpenTabWithClaude(worktreePath, context, cfg.ClaudeBin); err != nil {
		return fmt.Errorf("opening iTerm tab: %w", err)
	}

	ui.LogSuccess("iTerm2 tab opened")
	fmt.Println()
	return nil
}

func runWorkDelete(cmd *cobra.Command, args []string) error {
	target := args[0]

	// Resolve to absolute path if relative
	if !filepath.IsAbs(target) {
		abs, err := filepath.Abs(target)
		if err == nil {
			target = abs
		}
	}

	// Find matching worktree
	wts, err := wt.ListAll(cfg)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	var match *wt.Worktree
	for _, w := range wts {
		if w.Path == target || w.Name == target {
			w := w
			match = &w
			break
		}
	}

	if match == nil {
		return fmt.Errorf("no worktree found matching %q", target)
	}

	home := homeDir()
	shortPath := ui.ShortenHome(match.Path, home)

	if !workDeleteForce {
		fmt.Printf("Delete worktree %s?\n", ui.CyanText(match.Name))
		fmt.Printf("  Path: %s\n", shortPath)
		fmt.Print("  Confirm [y/N]: ")

		var resp string
		fmt.Scanln(&resp)
		if resp != "y" && resp != "Y" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	basePath := cfg.RepoBasePath(match.Repo)
	originPath := filepath.Join(basePath, match.Repo)

	removeCmd := exec.Command("git", "worktree", "remove", match.Path, "--force")
	removeCmd.Dir = originPath
	if out, err := removeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, string(out))
	}

	ui.LogSuccess(fmt.Sprintf("Deleted worktree: %s", shortPath))
	return nil
}
