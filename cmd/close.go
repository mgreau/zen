package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/tmux"
	"github.com/mgreau/zen/internal/ui"
	wt "github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

const closeLong = `Finish a work item in one step: stop the agent, remove the worktree, and
close the tmux window zen opened for it.

With no argument, closes the worktree the current directory is in, so an agent
can run it from inside the session it is finishing. That needs the worktree's
zen tmux window; elsewhere, pass the worktree from outside it. Run from inside the window
being closed, the close finishes in the background; the outcome shows on the
tmux status line and in ~/.zen/close.log.

Nothing is stopped or closed when the worktree has local changes, when its
HEAD has commits no branch holds (such as commits on a detached review HEAD),
when an agent for it runs outside zen's tmux windows, or when one of those
windows has a pane elsewhere. These are checked again just before the
windows close. If removal still fails after that, the windows stay closed,
the worktree is kept, and zen says why. Only windows zen tagged when it
opened them are closed.

Outside tmux, close removes the worktree only when no agent is running, and
leaves the terminal tab to you.`

var workCloseCmd = &cobra.Command{
	Use:   "close [name]",
	Short: "Stop the agent, remove the worktree and close its tmux window",
	Long:  closeLong,
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkClose,
}

var reviewCloseCmd = &cobra.Command{
	Use:   "close [pr-number]",
	Short: "Stop the agent, remove the review worktree and close its tmux window",
	Long:  closeLong,
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReviewClose,
}

// closeFinishCmd finishes a close started from inside a window it closes.
// close starts it in its own session so it outlives that window.
var closeFinishCmd = &cobra.Command{
	Use:    "__close-finish",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runCloseFinish,
}

var (
	closeForce bool

	finishPath          string
	finishOrigin        string
	finishName          string
	finishClient        string
	finishCleanSessions bool
)

func init() {
	workCloseCmd.Flags().BoolVarP(&closeForce, "force", "f", false, "Skip confirmation")
	reviewCloseCmd.Flags().BoolVarP(&closeForce, "force", "f", false, "Skip confirmation")
	workCmd.AddCommand(workCloseCmd)
	reviewCmd.AddCommand(reviewCloseCmd)

	closeFinishCmd.Flags().StringVar(&finishPath, "path", "", "worktree path")
	closeFinishCmd.Flags().StringVar(&finishOrigin, "origin", "", "origin repository path")
	closeFinishCmd.Flags().StringVar(&finishName, "name", "", "worktree name")
	closeFinishCmd.Flags().StringVar(&finishClient, "client", "", "tmux client to show the outcome on")
	closeFinishCmd.Flags().BoolVar(&finishCleanSessions, "clean-sessions", false, "remove agent session files")
	rootCmd.AddCommand(closeFinishCmd)
	rootCmd.AddCommand(closeLaunchCmd)
}

func runWorkClose(cmd *cobra.Command, args []string) error {
	var (
		match *wt.Worktree
		err   error
	)
	if len(args) == 1 {
		match, err = findWorktreeExact(args[0])
	} else {
		match, err = findWorktreeAtCwd()
	}
	if err != nil {
		return err
	}
	return closeWorktree(match)
}

func runReviewClose(cmd *cobra.Command, args []string) error {
	var (
		match *wt.Worktree
		err   error
	)
	if len(args) == 1 {
		prNumber, perr := strconv.Atoi(args[0])
		if perr != nil {
			return fmt.Errorf("invalid PR number %q: %w", args[0], perr)
		}
		match, err = findWorktreeByPR(prNumber)
	} else {
		match, err = findWorktreeAtCwd()
		if err == nil && match.Type != wt.TypePRReview {
			err = fmt.Errorf("%s is not a PR review worktree; use zen work close", match.Name)
		}
	}
	if err != nil {
		return err
	}
	return closeWorktree(match)
}

// closePlan is what closing one worktree involves.
type closePlan struct {
	name          string
	path          string
	origin        string
	cleanSessions bool
	client        string // tmux client to show a background outcome on
}

// closeOps is the outside world a close acts on, replaceable in tests.
type closeOps struct {
	checkRemovable func(origin, path string) error
	inTmux         func() bool
	windowsFor     func(path string) ([]tmux.Window, error)
	agentProcs     func() []agent.Proc
	sessionPIDs    func(path string) []int
	descendsFrom   func(pid int, roots []int) bool
	currentWindow  func() string
	selfPID        func() int
	cwdIn          func(path string) bool
	spawnFinish    func(plan closePlan) (logPath string, err error)
	finish         func(ops closeOps, plan closePlan) error
}

var defaultCloseOps = closeOps{
	checkRemovable: wt.CheckRemovable,
	inTmux:         tmux.Running,
	windowsFor:     tmux.WindowsFor,
	agentProcs:     agent.Procs,
	sessionPIDs:    agent.SessionPIDs,
	descendsFrom:   agent.DescendsFrom,
	currentWindow:  tmux.CurrentWindow,
	selfPID:        os.Getpid,
	cwdIn:          cwdIn,
	spawnFinish:    spawnCloseFinish,
	finish:         finishClose,
}

func closeWorktree(match *wt.Worktree) error {
	shortPath := ui.ShortenHome(match.Path, homeDir())
	if !closeForce {
		if !ui.Interactive() {
			return fmt.Errorf("closing %s needs a confirmation; run this from a terminal or pass --force", match.Name)
		}
		fmt.Printf("Close %s?\n", ui.CyanText(match.Name))
		fmt.Printf("  Stops its agent, removes %s and closes its tmux window.\n", shortPath)
		if !ui.ConfirmYN("  Confirm [y/N]: ") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	plan := closePlan{
		name:          match.Name,
		path:          match.Path,
		origin:        filepath.Join(cfg.RepoBasePath(match.Repo), match.Repo),
		cleanSessions: match.Type == wt.TypeFeature,
		client:        tmux.ClientName(),
	}
	logPath, err := runClose(defaultCloseOps, plan)
	if err != nil {
		return err
	}
	if logPath != "" {
		ui.LogSuccess(fmt.Sprintf("Closing %s in the background; this window will close. Log: %s",
			match.Name, ui.ShortenHome(logPath, homeDir())))
	}
	return nil
}

// runClose checks that plan can go ahead, then finishes it here or, when this
// process runs in a window it would close, in a background process. It
// returns the background process's log path, or "" when it finished here.
func runClose(ops closeOps, plan closePlan) (string, error) {
	windows, _, err := checkClose(ops, plan)
	if err != nil {
		return "", err
	}
	if len(windows) > 0 && insideWindows(ops, plan.path, windows) {
		return ops.spawnFinish(plan)
	}
	return "", ops.finish(ops, plan)
}

// checkClose refuses plan when removal would fail or when closing its windows
// would stop the wrong agents. It returns the windows to close and the
// processes in them that removal waits for: agents in the worktree and
// processes using its sessions.
func checkClose(ops closeOps, plan closePlan) ([]tmux.Window, []int, error) {
	if resolvedPath(plan.path) == resolvedPath(plan.origin) {
		return nil, nil, fmt.Errorf("%s is the repository's main checkout, not a zen worktree", plan.path)
	}
	if err := ops.checkRemovable(plan.origin, plan.path); err != nil {
		return nil, nil, fmt.Errorf("%s: %w; nothing was closed", plan.name, err)
	}

	var windows []tmux.Window
	if ops.inTmux() {
		var err error
		if windows, err = ops.windowsFor(plan.path); err != nil {
			// Without the windows, an agent zen cannot see from here, such
			// as one in a sandbox, could lose its worktree while it runs.
			return nil, nil, fmt.Errorf("%s: %w; nothing was closed", plan.name, err)
		}
	}
	// With no window to close, zen cannot stop an agent it does not see, such
	// as one in a sandbox. Run from inside the worktree, that agent may be
	// the caller, so do not remove the worktree from under it.
	if len(windows) == 0 && ops.cwdIn(plan.path) {
		return nil, nil, fmt.Errorf("%s: run close from outside the worktree, or from its zen tmux window; nothing was closed", plan.name)
	}
	for _, w := range windows {
		if len(w.Outside) > 0 {
			return nil, nil, fmt.Errorf("%s: its tmux window %s has a pane in %s, which closing it would stop; close that pane first",
				plan.name, w.ID, ui.ShortenHome(w.Outside[0], homeDir()))
		}
	}
	roots := panePIDs(windows)
	agent.ResetProcCache()

	var stopping []int
	for _, p := range ops.agentProcs() {
		inWorktree := agent.InWorktree(p.Cwd, plan.path)
		inWindow := ops.descendsFrom(p.PID, roots)
		switch {
		case inWorktree && !inWindow && len(windows) == 0:
			return nil, nil, fmt.Errorf("%s: an agent (pid %d) is running in it and zen found no tmux window of its own to close; exit the agent first: %w",
				plan.name, p.PID, wt.ErrWorktreeActive)
		case inWorktree && !inWindow:
			return nil, nil, fmt.Errorf("%s: an agent (pid %d) is running outside zen's tmux window for this worktree; exit it first: %w",
				plan.name, p.PID, wt.ErrWorktreeActive)
		case !inWorktree && inWindow:
			return nil, nil, fmt.Errorf("%s: its tmux window also runs an agent in %s (pid %d); exit it first",
				plan.name, ui.ShortenHome(p.Cwd, homeDir()), p.PID)
		case inWorktree:
			stopping = append(stopping, p.PID)
		}
	}
	// Removal also counts processes carrying one of the worktree's session
	// IDs, wherever they run, so they must be ones the windows stop.
	for _, pid := range ops.sessionPIDs(plan.path) {
		if !ops.descendsFrom(pid, roots) {
			return nil, nil, fmt.Errorf("%s: process %d is using this worktree's agent session outside zen's tmux window; exit it first: %w",
				plan.name, pid, wt.ErrWorktreeActive)
		}
		if !slices.Contains(stopping, pid) {
			stopping = append(stopping, pid)
		}
	}
	return windows, stopping, nil
}

func panePIDs(windows []tmux.Window) []int {
	var pids []int
	for _, w := range windows {
		pids = append(pids, w.PanePIDs...)
	}
	return pids
}

// cwdIn reports whether this process runs in path.
func cwdIn(path string) bool {
	cwd, err := os.Getwd()
	return err == nil && agent.InWorktree(cwd, path)
}

// insideWindows reports whether this process runs in one of windows, where
// closing the window would stop it before the close finishes. When the
// current window is unknown and the process tree does not tell, running in
// the worktree counts as inside: zen is likely in one of its windows.
func insideWindows(ops closeOps, path string, windows []tmux.Window) bool {
	if ops.descendsFrom(ops.selfPID(), panePIDs(windows)) {
		return true
	}
	cur := ops.currentWindow()
	if cur == "" {
		return ops.cwdIn(path)
	}
	for _, w := range windows {
		if w.ID == cur {
			return true
		}
	}
	return false
}

func closeLogPath() string {
	return filepath.Join(config.Dir(), "close.log")
}

// finishArgv is the command line of the background finish.
func finishArgv(exe string, plan closePlan) []string {
	argv := []string{exe, "__close-finish",
		"--path", plan.path, "--origin", plan.origin, "--name", plan.name}
	if plan.cleanSessions {
		argv = append(argv, "--clean-sessions")
	}
	if plan.client != "" {
		argv = append(argv, "--client", plan.client)
	}
	if debugFlag {
		argv = append(argv, "--debug")
	}
	return argv
}

// closeJob is a background finish to start: its command line, environment,
// working directory and log.
type closeJob struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
	Log  string   `json:"log"`
	// Client is the tmux client told if the launch fails.
	Client string `json:"client,omitempty"`
}

// spawnCloseFinish starts the finish so that it outlives the window it was
// started from. The tmux server runs a launcher for it, which escapes any
// sandbox around this process, such as an agent's. The launcher gets the job
// through a file, so nothing passes through tmux's format expansion or the
// server's older environment. Starting the finish from here instead would
// leave it inside the window it closes, so a failed launch is an error.
func spawnCloseFinish(plan closePlan) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating zen binary: %w", err)
	}
	job := closeJob{
		Args: finishArgv(exe, plan),
		Env:  os.Environ(),
		// Not the worktree: a process there would block its removal.
		Dir:    plan.origin,
		Log:    closeLogPath(),
		Client: plan.client,
	}
	if err := os.MkdirAll(filepath.Dir(job.Log), 0o755); err != nil {
		return "", err
	}
	removeStaleJobs(filepath.Dir(job.Log))
	if err := launchThroughTmux(exe, job); err != nil {
		return "", fmt.Errorf("%s: could not start the close outside this window: %w; nothing was closed", plan.name, err)
	}
	return job.Log, nil
}

// staleJobAge is how old a job file the launcher never picked up must be
// before it is deleted. The launcher deletes the files it reads at once.
const staleJobAge = time.Hour

func removeStaleJobs(dir string) {
	paths, _ := filepath.Glob(filepath.Join(dir, "close-job-*.json"))
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > staleJobAge {
			_ = os.Remove(p)
		}
	}
}

func launchThroughTmux(exe string, job closeJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(job.Log), "close-job-*.json")
	if err != nil {
		return err
	}
	// CreateTemp makes the file 0600: it holds the caller's environment.
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := tmux.RunBackground([]string{exe, "__close-launch", f.Name()}, job.Log); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}

// closeLaunchCmd starts the job in the file it is given, then deletes the
// file. The tmux server runs it, with the server's environment, so it loads
// no config.
var closeLaunchCmd = &cobra.Command{
	Use:    "__close-launch <job-file>",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		os.Remove(args[0])
		var job closeJob
		if err := json.Unmarshal(data, &job); err != nil {
			return fmt.Errorf("reading close job: %w", err)
		}
		if len(job.Args) == 0 || job.Log == "" {
			return errors.New("close job has no command or log")
		}
		if err := startDetached(job); err != nil {
			tmux.ShowMessage(job.Client, fmt.Sprintf("zen: could not start the close: %v", err))
			return err
		}
		return nil
	},
}

// startDetached starts job in a new session, so the hangup sent when a
// window closes, or the exit of the tmux server, does not reach it.
func startDetached(job closeJob) error {
	logF, err := os.OpenFile(job.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logF.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devNull.Close()
	fmt.Fprintf(logF, "%s %s\n", time.Now().Format(time.RFC3339), strings.Join(job.Args[1:], " "))

	proc, err := os.StartProcess(job.Args[0], job.Args, &os.ProcAttr{
		Dir:   job.Dir,
		Env:   job.Env,
		Files: []*os.File{devNull, logF, logF},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return fmt.Errorf("starting background close: %w", err)
	}
	return proc.Release()
}

func runCloseFinish(cmd *cobra.Command, args []string) error {
	if finishPath == "" || finishOrigin == "" {
		return errors.New("--path and --origin are required")
	}
	plan := closePlan{
		name:          finishName,
		path:          finishPath,
		origin:        finishOrigin,
		cleanSessions: finishCleanSessions,
		client:        finishClient,
	}
	err := finishClose(defaultCloseOps, plan)
	if err != nil {
		// main prints err to the log.
		tmux.ShowMessage(plan.client, fmt.Sprintf("zen: could not close %s: %v (see %s)", plan.name, err, closeLogPath()))
	} else {
		tmux.ShowMessage(plan.client, fmt.Sprintf("zen: closed %s", plan.name))
	}
	return err
}

// Agents get agentHangupGrace to exit after their window closes, then are
// sent SIGTERM and get agentTermGrace more.
const (
	agentHangupGrace = 5 * time.Second
	agentTermGrace   = 10 * time.Second
)

// finishClose checks plan again, closes its windows, makes sure their agents
// exit, then removes the worktree and, for feature work, the agent's session
// files.
func finishClose(ops closeOps, plan closePlan) error {
	windows, stopping, err := checkClose(ops, plan)
	if err != nil {
		return err
	}
	var killErrs []error
	closed := 0
	for _, w := range windows {
		if err := tmux.KillWindow(w.ID); err != nil {
			killErrs = append(killErrs, err)
			continue
		}
		closed++
	}
	if closed > 0 {
		ui.LogSuccess(fmt.Sprintf("Closed %d tmux window(s)", closed))
	}
	if len(killErrs) > 0 {
		return fmt.Errorf("%s: closed %d of %d tmux windows; the worktree was kept: %w",
			plan.name, closed, len(windows), errors.Join(killErrs...))
	}
	if err := stopAgents(plan.path, stopping); err != nil {
		return fmt.Errorf("%s: %w; the worktree was kept", plan.name, err)
	}

	// Resolve symlinks while the directory exists: the agent filed its
	// sessions under the resolved path.
	sessionPath := resolvedPath(plan.path)
	if err := wt.RemoveKeepingCommits(plan.origin, plan.path); err != nil {
		return fmt.Errorf("%s: %w; the worktree was kept", plan.name, err)
	}
	ui.LogSuccess(fmt.Sprintf("Removed worktree %s", ui.ShortenHome(plan.path, homeDir())))

	if plan.cleanSessions {
		cleanAllSessions(sessionPath, plan.path)
	}
	if len(windows) == 0 {
		ui.LogInfo("No zen tmux window found for this worktree; close its terminal tab yourself.")
	}
	return nil
}

// cleanAllSessions removes every agent's session files for a removed
// worktree: the worktree may have been used with an agent other than the
// default. resolved is its path with symlinks resolved, tried first.
func cleanAllSessions(resolved, path string) {
	total := 0
	for _, k := range agent.AllKinds() {
		ag := agent.New(k, "")
		removed, err := ag.CleanSessions(resolved)
		if err == nil && removed == 0 && resolved != path {
			removed, err = ag.CleanSessions(path)
		}
		if err != nil {
			ui.LogWarn(fmt.Sprintf("clean %s session files: %v", k, err))
			continue
		}
		total += removed
	}
	if total > 0 {
		ui.LogSuccess(fmt.Sprintf("Cleaned %d session file(s)", total))
	}
}

// stopAgents waits for the processes that ran in the closed windows to exit,
// and sends SIGTERM to any that outlive the hangup, such as one started in
// its own session. A pid is signalled only while it is still an agent in the
// worktree or still uses one of its sessions, so a reused pid is left alone.
func stopAgents(path string, pids []int) error {
	if waitForAgentsToExit(path, agentHangupGrace) {
		return nil
	}
	agent.ResetProcCache()
	live := map[int]bool{}
	for _, p := range agent.Procs() {
		if agent.InWorktree(p.Cwd, path) {
			live[p.PID] = true
		}
	}
	for _, pid := range agent.SessionPIDs(path) {
		live[pid] = true
	}
	for _, pid := range pids {
		if live[pid] {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	if waitForAgentsToExit(path, agentTermGrace) {
		return nil
	}
	return fmt.Errorf("agent still running in %s after its window closed: %w", path, wt.ErrWorktreeActive)
}

func waitForAgentsToExit(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		agent.ResetProcCache()
		if !agent.RunningIn(path) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// findWorktreeExact finds a worktree by exact name, then by path.
func findWorktreeExact(target string) (*wt.Worktree, error) {
	wts, err := wt.ListAll(cfg)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	for i := range wts {
		if wts[i].Name == target {
			return &wts[i], nil
		}
	}
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		if abs, err := filepath.Abs(absTarget); err == nil {
			absTarget = abs
		}
	}
	absTarget = resolvedPath(absTarget)
	for i := range wts {
		if resolvedPath(wts[i].Path) == absTarget {
			return &wts[i], nil
		}
	}
	return nil, fmt.Errorf("no worktree found matching %q", target)
}

// findWorktreeAtCwd finds the worktree the current directory is in.
func findWorktreeAtCwd() (*wt.Worktree, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("current directory: %w", err)
	}
	wts, err := wt.ListAll(cfg)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	if match := worktreeContaining(wts, cwd); match != nil {
		return match, nil
	}
	return nil, fmt.Errorf("%s is not inside a zen worktree; pass the worktree to close", ui.ShortenHome(cwd, homeDir()))
}

// worktreeContaining returns the worktree holding dir, preferring the
// deepest one when worktrees nest.
func worktreeContaining(wts []wt.Worktree, dir string) *wt.Worktree {
	dir = resolvedPath(dir)
	var best *wt.Worktree
	bestLen := -1
	for i := range wts {
		root := resolvedPath(wts[i].Path)
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(root) > bestLen {
			best, bestLen = &wts[i], len(root)
		}
	}
	return best
}

func resolvedPath(p string) string {
	if eval, err := filepath.EvalSymlinks(p); err == nil {
		p = eval
	}
	return filepath.Clean(p)
}
