package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/mgreau/zen/internal/agent"
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

The branch will be prefixed with mgreau/ per naming convention. The branch
name must be a single path segment: no "/", whitespace, or leading "-".
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
	workNewCmd.Flags().StringVarP(&workNewModel, "model", "m", "", "Model to use (agent-specific, e.g. opus or gpt-5-codex)")
	addAgentFlag(workNewCmd)
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

	ag, err := resolveAgent()
	if err != nil {
		return err
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
				HasSession: hasAgentSession(ag, f.Path),
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
		if hasAgentSession(ag, f.Path) {
			sessionIndicator = ui.GreenText("●")
		}

		fmt.Printf("%-12s %-45s %s\n", f.Repo, ui.Truncate(f.Name, 43), sessionIndicator)
		fmt.Printf("             %s\n", ui.DimText(ui.ShortenHome(f.Path, home)))
	}

	fmt.Println()
	ui.Hint(fmt.Sprintf("● = Active %s session", ag.Kind()))
	fmt.Println()
	return nil
}

// validateWorkBranch rejects names that would not map to one sibling
// worktree directory. The directory is "<repo>-<branch>", so a "/" would
// create a nested directory that discovery then lists under its last path
// element.
func validateWorkBranch(branch string) error {
	if branch == "" || strings.HasPrefix(branch, "-") || strings.Contains(branch, "/") || strings.ContainsFunc(branch, unicode.IsSpace) {
		return fmt.Errorf("invalid branch name %q: use a single path segment without \"/\", whitespace, or a leading \"-\"", branch)
	}
	return nil
}

func runWorkNew(cmd *cobra.Command, args []string) error {
	repo := args[0]
	branch := args[1]
	context := ""
	if len(args) == 3 {
		context = args[2]
	}

	// Resolve the agent first: a bad --agent must fail before anything is
	// created, or a retry hits "worktree already exists".
	ag, err := resolveAgent()
	if err != nil {
		return err
	}
	target, err := newFeatureTarget(repo, branch)
	if err != nil {
		return err
	}
	if err := target.create(); err != nil {
		return err
	}
	worktreePath := target.worktreePath

	if workNewModel != "" {
		fmt.Printf("  Model:  %s\n", ui.CyanText(workNewModel))
	}

	return launchInWorktree(ag, worktreePath, func(a agent.Agent) string {
		return a.StartCommand(context, workNewModel)
	}, workNewNoITerm)
}

// featureTarget is where a new feature worktree goes: the paths and branch
// name derived from a configured repo and a branch suffix.
type featureTarget struct {
	repo         string
	branch       string // the suffix the user typed
	originPath   string
	worktreeName string
	worktreePath string
	gitBranch    string // branch with branch_prefix applied
}

// newFeatureTarget validates repo and branch and computes the worktree
// location. It fails if the worktree already exists.
func newFeatureTarget(repo, branch string) (featureTarget, error) {
	if err := validateWorkBranch(branch); err != nil {
		return featureTarget{}, err
	}
	basePath := cfg.RepoBasePath(repo)
	if basePath == "" {
		return featureTarget{}, fmt.Errorf("unknown repo %q — check ~/.zen/config.yaml", repo)
	}
	t := featureTarget{
		repo:         repo,
		branch:       branch,
		originPath:   filepath.Join(basePath, repo),
		worktreeName: fmt.Sprintf("%s-%s", repo, branch),
		gitBranch:    branch,
	}
	t.worktreePath = filepath.Join(basePath, t.worktreeName)
	if prefix := cfg.GetBranchPrefix(); prefix != "" {
		t.gitBranch = fmt.Sprintf("%s/%s", prefix, branch)
	}
	if _, err := os.Stat(t.worktreePath); err == nil {
		return featureTarget{}, fmt.Errorf("worktree already exists: %s\n  Resume with: zen work resume %s", t.worktreePath, branch)
	}
	return t, nil
}

// create cuts the branch from origin/main and adds the worktree.
func (t featureTarget) create() error {
	ui.LogInfo(fmt.Sprintf("Fetching origin/main in %s...", t.repo))
	ui.LogInfo(fmt.Sprintf("Creating worktree %s (branch %s)...", t.worktreeName, t.gitBranch))
	if err := wt.CreateFromMain(t.originPath, t.worktreePath, t.worktreeName, t.gitBranch); err != nil {
		return err
	}
	fmt.Println()
	ui.LogSuccess(fmt.Sprintf("Created worktree: %s", ui.ShortenHome(t.worktreePath, homeDir())))
	fmt.Printf("  Branch: %s\n", ui.CyanText(t.gitBranch))
	return nil
}

// launchInWorktree runs the command that command builds for ag in a new
// terminal tab at worktreePath, or prints it when noTerminal is set. The
// agent is prepared for launch (e.g. Codex's remote-control daemon) only
// when a tab is actually opened.
func launchInWorktree(ag agent.Agent, worktreePath string, command func(agent.Agent) string, noTerminal bool) error {
	if noTerminal {
		fmt.Println()
		fmt.Println(ui.BoldText("Open manually:"))
		fmt.Printf("  cd %s && %s\n", agent.ShellQuote(worktreePath), command(ag))
		return nil
	}

	term, err := terminal.NewTerminal(cfg.GetTerminal())
	if err != nil {
		return err
	}

	launchCmd := command(prepareLaunch(ag, worktreePath))
	if err := term.OpenTab(worktreePath, launchCmd); err != nil {
		return fmt.Errorf("opening %s tab: %w", term.Name(), err)
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

	// Sessions of every agent die with the worktree, not just the configured
	// one: an imported worktree holds a Claude session even when the
	// configured agent is Codex.
	type agentSessions struct {
		ag agent.Agent
		n  int
	}
	var owners []agentSessions
	total := 0
	var size int64
	for _, kind := range agent.AllKinds() {
		a := cfg.NewAgent(string(kind))
		s, _ := a.FindSessions(match.Path)
		if len(s) == 0 {
			continue
		}
		owners = append(owners, agentSessions{a, len(s)})
		total += len(s)
		for _, one := range s {
			size += one.Size
		}
	}
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
	if total > 0 {
		fmt.Printf("  Sessions:  %d (%s)\n", total, humanSize(size))
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

	if err := wt.Remove(originPath, match.Path); err != nil {
		return err
	}
	ui.LogSuccess("Removed worktree")

	// Clean up agent session files
	for _, o := range owners {
		if removed, err := o.ag.CleanSessions(match.Path); err != nil {
			fmt.Printf("  %s clean %s session files: %v\n", ui.YellowText("Warning:"), o.ag.Kind(), err)
		} else if removed > 0 {
			ui.LogSuccess(fmt.Sprintf("Cleaned %d %s session file(s)", removed, o.ag.Kind()))
		}
	}

	fmt.Println()
	return nil
}
