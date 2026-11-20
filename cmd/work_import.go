package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/migrate"
	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/ui"
	wt "github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var workImportCmd = &cobra.Command{
	Use:   "import [repo] <branch>",
	Short: "Turn an existing Claude session into a feature worktree",
	Long: `Move an existing Claude Code session into a new feature worktree and
resume it there in a new terminal tab.

Use it when exploratory work in a regular Claude session turns into real work.
zen creates the worktree (as zen work new does), copies the conversation into
it under a new session id, and resumes it with a short hand-off message
telling Claude where it now lives. The original session is left untouched.

The session is the most recent one for the current directory, or the one
named with --session (from any directory). The repo is inferred from the
directory the session ran in when it is a checkout of a configured repo.

Uncommitted changes in the session's directory stay there unless you pass
--carry-changes, which copies tracked edits (as a patch) and untracked files
into the new worktree. The source checkout is never modified.

From inside the Claude session itself, run /zen-import instead.`,
	Example: `  zen work import fix-token-refresh                 # newest session here, repo inferred
  zen work import app fix-token-refresh --carry-changes
  zen work import --session 9a80287f-474e-4acc-952c-ee0fac6fba03 fix-token-refresh`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runWorkImport,
}

var (
	workImportSession    string
	workImportCarry      bool
	workImportNoTerminal bool
	workImportModel      string
	workImportYes        bool
)

func init() {
	workImportCmd.Flags().StringVarP(&workImportSession, "session", "s", "", "Claude session id to import (default: most recent in the current directory)")
	workImportCmd.Flags().BoolVar(&workImportCarry, "carry-changes", false, "Copy uncommitted changes from the session's directory into the new worktree")
	workImportCmd.Flags().BoolVar(&workImportNoTerminal, "no-terminal", false, "Create worktree and session only, print the resume command")
	workImportCmd.Flags().StringVarP(&workImportModel, "model", "m", "", "Model to resume with")
	workImportCmd.Flags().BoolVarP(&workImportYes, "yes", "y", false, "Skip confirmation")
	workCmd.AddCommand(workImportCmd)
}

// importSource is the session being imported and the checkout it ran in.
type importSource struct {
	path  string // session file
	id    string
	info  migrate.ClaudeSessionInfo
	dir   string // where the session ran
	top   string // checkout root containing dir, "" if not in a git checkout
	repo  string // git common dir of that checkout, "" if not in one
	stamp string // "modified <time>, <size>" for display
}

func runWorkImport(cmd *cobra.Command, args []string) error {
	if jsonFlag {
		return fmt.Errorf("zen work import does not support --json")
	}
	src, err := findImportSource(workImportSession)
	if err != nil {
		return err
	}

	repo, branch := "", args[len(args)-1]
	if len(args) == 2 {
		repo = args[0]
	} else if repo, err = inferRepo(src); err != nil {
		return err
	}

	target, err := newFeatureTarget(repo, branch)
	if err != nil {
		return err
	}

	// Decide what happens to uncommitted work before creating anything, so a
	// --carry-changes that cannot work fails with nothing to clean up.
	var changes wt.LocalChanges
	targetRepo := resolvedGitDir(target.originPath)
	if workImportCarry && targetRepo == "" {
		return fmt.Errorf("--carry-changes: cannot read the git repository of %s's main clone at %s", repo, target.originPath)
	}
	sameRepo := src.repo != "" && src.repo == targetRepo
	if workImportCarry && !sameRepo {
		return fmt.Errorf("--carry-changes: the session ran in %s, which is not a checkout of %s", src.dir, repo)
	}
	// A checkout of another repo is scanned too, for the warning: its edits
	// can never be carried, but the user and Claude should know they stay.
	if src.top != "" {
		if changes, err = wt.FindLocalChanges(src.top); err != nil {
			if workImportCarry {
				return err
			}
			// Without --carry-changes the scan only feeds a warning.
			ui.LogWarn(fmt.Sprintf("Could not check %s for uncommitted changes: %v", src.top, err))
			changes = wt.LocalChanges{}
		}
	}

	home := homeDir()
	fmt.Println()
	fmt.Println(ui.BoldText("Import Claude session into a feature worktree"))
	title := src.info.Title
	if title == "" {
		title = ui.DimText("(untitled)")
	}
	fmt.Printf("  Session:  %s %s\n", ui.CyanText(src.id), title)
	fmt.Printf("            %s\n", ui.DimText(src.stamp))
	fmt.Printf("  From:     %s\n", ui.ShortenHome(src.dir, home))
	fmt.Printf("  Into:     %s (branch %s, from origin/main)\n", ui.ShortenHome(target.worktreePath, home), ui.CyanText(target.gitBranch))
	switch {
	case changes.Empty():
	case workImportCarry:
		fmt.Printf("  Changes:  %s\n", describeChanges(changes)+", copied into the new worktree")
	case sameRepo:
		fmt.Printf("  Changes:  %s\n", ui.YellowText(describeChanges(changes)+", left behind (pass --carry-changes to copy)"))
	default:
		fmt.Printf("  Changes:  %s\n", ui.YellowText(describeChanges(changes)+" in that other checkout, left behind"))
	}
	fmt.Println()

	if !workImportYes {
		fmt.Print("  Continue? [Y/n]: ")
		var resp string
		// An empty line accepts; end of input (no terminal, < /dev/null)
		// is not consent.
		if _, err := fmt.Scanln(&resp); errors.Is(err, io.EOF) {
			fmt.Println()
			return fmt.Errorf("no answer on stdin; pass --yes to import without confirming")
		}
		if r := strings.ToLower(strings.TrimSpace(resp)); r == "n" || r == "no" {
			fmt.Println("  Cancelled.")
			return nil
		}
	}

	if err := target.create(); err != nil {
		return err
	}

	// Inside a checkout of the same repo, every path in it maps onto the new
	// worktree. Anywhere else, only the session's own directory does:
	// grafting unrelated paths under the worktree would invent directories.
	fromDir, subtree := src.dir, false
	if sameRepo {
		fromDir, subtree = src.top, true
	}

	// Copy the session before carrying changes: until changes land, the new
	// worktree is clean, so zen work delete can still remove it.
	newID, err := migrate.RelocateClaudeSession(src.path, fromDir, target.worktreePath, subtree, target.gitBranch)
	if err != nil {
		return fmt.Errorf("copying session into %s (worktree was created; remove it with zen work delete %s): %w", target.worktreePath, target.worktreeName, err)
	}
	ui.LogSuccess(fmt.Sprintf("Copied session to %s", newID))

	// Decided before carrying changes, against the same tree the session's
	// recorded cwd was just mapped onto, so the two agree.
	oldLocation := fmt.Sprintf("paths earlier in the conversation that point into %s refer to the old checkout", fromDir)
	if sameRepo {
		// src.top is symlink-resolved by git; resolve src.dir to match, and
		// never let the hint climb out of the worktree.
		dir := src.dir
		if r, rerr := filepath.EvalSymlinks(dir); rerr == nil {
			dir = r
		}
		rel, err := filepath.Rel(src.top, dir)
		inside := err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		if st, serr := os.Stat(filepath.Join(target.worktreePath, rel)); inside && serr == nil && st.IsDir() {
			oldLocation += fmt.Sprintf(". The session was working in the %s subdirectory and you now start at the worktree root, so cd there before running commands with relative paths", rel)
		}
	}
	if !sameRepo {
		oldLocation = fmt.Sprintf("the session previously ran in %s, outside this repository", src.dir)
	}

	carryNote := ""
	if !changes.Empty() {
		if workImportCarry {
			carryNote = carryInto(src.top, target.worktreePath)
		} else {
			carryNote = " Uncommitted changes in the old location were NOT copied here."
		}
	}

	// The new branch starts at origin/main (just fetched by create), so any
	// commits the old checkout has beyond it are not here.
	if sameRepo {
		if n, err := wt.CommitsNotIn(src.top, "origin/main"); err == nil && n > 0 {
			// Squash or rebase merges also land here, so this is a "may".
			ui.LogWarn(fmt.Sprintf("%s has %d commit(s) that are not on origin/main; unless they were merged under another hash, they are not in the new worktree", src.top, n))
			carryNote += fmt.Sprintf(" The old checkout had %d commit(s) not on origin/main that were not brought over, so code from earlier in the conversation may be missing here.", n)
		}
	}

	ag := cfg.NewAgent(string(agent.Claude))
	handoff := fmt.Sprintf("[zen] This conversation was moved from %s into its own worktree at %s, on new branch %s cut from origin/main.%s "+
		"From now on, read and edit files only in this worktree: %s. "+
		"Briefly recap where we left off and the next step, then wait for me.",
		src.dir, target.worktreePath, target.gitBranch, carryNote, oldLocation)
	resume := func(a agent.Agent) string {
		if pr, ok := a.(agent.PromptResumer); ok {
			return pr.ResumeWithPrompt(newID, workImportModel, handoff)
		}
		return a.ResumeCommand(newID, workImportModel) + " " + agent.ShellQuote(handoff)
	}

	if err := launchInWorktree(ag, target.worktreePath, resume, workImportNoTerminal); err != nil {
		// The worktree and session exist now; show how to reach them rather
		// than leaving a retry to fail with "worktree already exists".
		_ = launchInWorktree(ag, target.worktreePath, resume, true)
		return fmt.Errorf("%w\n  The worktree and session %s are ready; run the command above to continue", err, newID)
	}
	ui.Hint(fmt.Sprintf("The original session (%s) is unchanged; close it when you're ready.", src.id))
	later := fmt.Sprintf("zen work resume %s", target.branch)
	if cfg.AgentKind("") != string(agent.Claude) {
		later += " --agent claude" // the imported conversation is a Claude session
	}
	ui.Hint("Later: " + later)
	fmt.Println()
	return nil
}

// findImportSource locates the session to import: the one named by id, or
// the newest session for the current directory.
func findImportSource(id string) (importSource, error) {
	var src importSource
	if id != "" {
		path, err := migrate.FindClaudeSessionByID(id)
		if err != nil {
			return src, err
		}
		src.path = path
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return src, err
		}
		// Claude's directory naming is lossy (my_app and my-app share one),
		// so keep only sessions whose recorded cwd is really this directory.
		sessions, _ := session.FindSessions(cwd)
		// Sessions are newest first, so the first match is the one to use.
		var here []string
		for _, s := range sessions {
			if recorded, err := session.SessionCwd(s.Path); err == nil && sameDir(recorded, cwd) {
				here = append(here, s.Path)
				break
			}
		}
		if len(here) == 0 {
			return src, fmt.Errorf("no Claude sessions for %s\n  Run from the directory the session ran in, or pass --session <id> (shown by /status in Claude)", cwd)
		}
		src.path = here[0]
		src.dir = cwd
	}
	src.id = strings.TrimSuffix(filepath.Base(src.path), ".jsonl")

	st, err := os.Stat(src.path)
	if err != nil {
		return src, err
	}
	src.stamp = fmt.Sprintf("modified %s, %s", st.ModTime().Format("2006-01-02 15:04"), humanSize(st.Size()))

	if src.info, err = migrate.ReadClaudeSessionInfo(src.path); err != nil {
		return src, fmt.Errorf("reading session %s: %w", src.id, err)
	}
	if src.info.Cwd != "" {
		src.dir = src.info.Cwd
	}
	if src.dir == "" {
		return src, fmt.Errorf("session %s records no working directory", src.id)
	}

	// The checkout is optional: a session may have run outside any repo, or
	// in a directory that has since been removed.
	if top, err := wt.TopLevel(src.dir); err == nil {
		src.top = top
		src.repo, _ = wt.CommonDir(top)
	}
	return src, nil
}

// sameDir reports whether two paths name the same directory, comparing
// symlink-resolved forms when they resolve.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// inferRepo maps the session's checkout to a configured repo: the one whose
// main clone shares its git common directory (so a session in any worktree
// of the repo counts).
func inferRepo(src importSource) (string, error) {
	names := cfg.RepoNames()
	sort.Strings(names)
	if src.repo != "" {
		for _, name := range names {
			if resolvedGitDir(filepath.Join(cfg.RepoBasePath(name), name)) == src.repo {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("cannot infer the repo: the session ran in %s, which is not a checkout of a configured repo\n  Name it: zen work import <repo> <branch>  (configured: %s)", src.dir, strings.Join(names, ", "))
}

// resolvedGitDir is the git common directory of a main clone, computed the
// same way as for the session's checkout (so a .git gitfile, as left by
// --separate-git-dir, still matches), or "" when it cannot be resolved.
func resolvedGitDir(originPath string) string {
	p, err := wt.CommonDir(originPath)
	if err != nil {
		return ""
	}
	return p
}

// carryInto copies uncommitted work into the new worktree, reports the
// outcome, and returns the sentence the hand-off message uses to describe it.
func carryInto(srcTop, worktreePath string) string {
	res, err := wt.CarryChanges(srcTop, worktreePath)
	if res.Patched {
		if len(res.Conflicts) > 0 {
			ui.LogWarn(fmt.Sprintf("Tracked changes applied with conflicts in %s; resolve them in the new worktree:", strings.Join(res.Conflicts, ", ")))
			fmt.Println(ui.DimText(res.ConflictOutput))
		} else {
			ui.LogSuccess("Applied tracked changes")
		}
	}
	if len(res.Copied) > 0 {
		ui.LogSuccess(fmt.Sprintf("Copied %d untracked file(s)", len(res.Copied)))
	}
	for _, p := range res.Skipped {
		ui.LogWarn(fmt.Sprintf("Skipped untracked %s: already exists in the new worktree, or is not a plain file", p))
	}
	if len(res.NotApplied) > 0 {
		ui.LogWarn(fmt.Sprintf("Could not carry edits to %s (likely files from commits not on origin/main):", strings.Join(res.NotApplied, ", ")))
		fmt.Println(ui.DimText(res.NotAppliedOutput))
	}
	notApplied := ""
	if len(res.NotApplied) > 0 {
		notApplied = fmt.Sprintf(" Edits to %s could not be carried and exist only in the old checkout.", strings.Join(res.NotApplied, ", "))
	}
	if len(res.Failed) > 0 {
		notApplied += fmt.Sprintf(" Untracked %s could not be copied because of errors; they exist only in the old checkout.", strings.Join(res.Failed, ", "))
	}
	if len(res.Skipped) > 0 {
		notApplied += fmt.Sprintf(" Untracked %s were not copied (a file already exists here, or it is not a plain file); the old checkout's versions are not here.", strings.Join(res.Skipped, ", "))
	}
	switch {
	case !res.Patched && len(res.Copied) == 0 && err == nil:
		// The scan saw changes, but there was nothing to carry (for example a
		// mode-only change that produces an empty patch).
		return notApplied
	case err != nil:
		ui.LogWarn(fmt.Sprintf("Could not copy all uncommitted changes: %v", err))
		return " Copying uncommitted changes from the old location failed partway, so some may be missing here." + notApplied
	case len(res.Conflicts) > 0:
		return fmt.Sprintf(" Uncommitted changes from the old location were copied here, but %s have merge conflicts to resolve first.", strings.Join(res.Conflicts, ", ")) + notApplied
	default:
		return " Uncommitted changes from the old location were copied here." + notApplied
	}
}

func describeChanges(c wt.LocalChanges) string {
	var parts []string
	if c.Tracked {
		parts = append(parts, "edits to tracked files")
	}
	if n := len(c.Untracked); n > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked file(s)", n))
	}
	return "uncommitted " + strings.Join(parts, " and ")
}
