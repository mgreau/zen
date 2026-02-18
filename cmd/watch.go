package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"chainguard.dev/driftlessaf/workqueue"
	"chainguard.dev/driftlessaf/workqueue/dispatcher"
	"chainguard.dev/driftlessaf/workqueue/inmem"
	"github.com/chainguard-dev/clog"
	"github.com/mgreau/zen/internal/config"
	ghpkg "github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/notify"
	"github.com/mgreau/zen/internal/reconciler"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch <action>",
	Short: "Background daemon (start|stop|status)",
	Long: `Background daemon for monitoring GitHub review requests.

Actions:
  start              Start the background daemon
  stop               Stop the background daemon
  status             Show daemon status
  logs               Tail daemon log output
  logs search <term> Search logs for a PR number, worktree, or keyword`,
	Args: cobra.RangeArgs(1, 3),
	RunE: runWatch,
}

func init() {
	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
	action := args[0]
	switch action {
	case "start":
		return watchStart()
	case "stop":
		return watchStop()
	case "status":
		return watchStatus()
	case "logs":
		if len(args) >= 3 && args[1] == "search" {
			return watchLogSearch(args[2])
		}
		if len(args) >= 2 && args[1] == "search" {
			return fmt.Errorf("usage: zen watch logs search <term>")
		}
		return watchLogs()
	case "daemon":
		return watchDaemon()
	default:
		return fmt.Errorf("unknown action: %s (use start, stop, status, or logs)", action)
	}
}

func pidFile() string {
	return filepath.Join(config.StateDir(), "watch.pid")
}

func logFile() string {
	return filepath.Join(config.StateDir(), "watch.log")
}

func lastCheckFile() string {
	return filepath.Join(config.StateDir(), "last_check.json")
}

func watchIsRunning() (bool, int) {
	data, err := os.ReadFile(pidFile())
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}
	if err := syscall.Kill(pid, 0); err != nil {
		os.Remove(pidFile())
		return false, 0
	}
	return true, pid
}

func watchStart() error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}

	running, pid := watchIsRunning()
	if running {
		ui.LogWarn(fmt.Sprintf("Watch daemon already running (PID: %d)", pid))
		return nil
	}

	binPath, err := os.Executable()
	if err != nil {
		return err
	}

	logF, err := os.OpenFile(logFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	attr := &os.ProcAttr{
		Dir:   os.Getenv("HOME"),
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, logF, logF},
	}

	proc, err := os.StartProcess(binPath, []string{binPath, "watch", "daemon"}, attr)
	if err != nil {
		logF.Close()
		return fmt.Errorf("starting daemon: %w", err)
	}
	logF.Close()

	if err := os.WriteFile(pidFile(), []byte(strconv.Itoa(proc.Pid)), 0o644); err != nil {
		return err
	}
	proc.Release()

	ui.LogSuccess(fmt.Sprintf("Watch daemon started (PID: %d)", proc.Pid))
	ui.LogInfo("Log file: " + logFile())
	return nil
}

func watchStop() error {
	running, pid := watchIsRunning()
	if !running {
		ui.LogWarn("Watch daemon is not running")
		return nil
	}
	syscall.Kill(pid, syscall.SIGTERM)
	os.Remove(pidFile())
	ui.LogSuccess(fmt.Sprintf("Watch daemon stopped (PID: %d)", pid))
	return nil
}

func watchLogs() error {
	lf := logFile()
	if _, err := os.Stat(lf); os.IsNotExist(err) {
		ui.LogWarn("No log file found. Start the daemon with 'zen watch start'.")
		return nil
	}
	cmd := exec.Command("tail", "-f", lf)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func watchLogSearch(term string) error {
	// Search both current and rotated log
	files := []string{logFile(), logFile() + ".1"}

	found := false
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		cmd := exec.Command("grep", "-n", "-i", term, f)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			found = true
		}
	}

	if !found {
		fmt.Printf("No matches for %q in daemon logs.\n", term)
	}
	return nil
}

func watchStatus() error {
	fmt.Println()
	fmt.Println(ui.BoldText("Watch Daemon Status"))
	ui.Separator()

	running, pid := watchIsRunning()
	if running {
		fmt.Printf("Status: %s\n", ui.GreenText("Running"))
		fmt.Printf("PID: %d\n", pid)
	} else {
		fmt.Printf("Status: %s\n", ui.DimText("Not running"))
	}
	fmt.Println()

	data, err := os.ReadFile(lastCheckFile())
	if err == nil {
		var state struct {
			Timestamp string `json:"timestamp"`
			PRCount   int    `json:"pr_count"`
		}
		if json.Unmarshal(data, &state) == nil {
			fmt.Println("Last check:")
			fmt.Printf("  Time: %s\n", state.Timestamp)
			fmt.Printf("  PRs found: %d\n", state.PRCount)
		}
	}
	fmt.Println()

	if len(cfg.Authors) > 0 {
		fmt.Printf("Auto-spawn authors: %s\n", strings.Join(cfg.Authors, " "))
	} else {
		fmt.Println("Auto-spawn: disabled (no authors configured)")
	}
	fmt.Println()
	return nil
}

func watchDaemon() error {
	config.EnsureDirs()

	os.WriteFile(pidFile(), []byte(strconv.Itoa(os.Getpid())), 0o644)

	pollInterval := 5 * time.Minute
	if cfg.PollInterval != "" {
		if d, err := time.ParseDuration(cfg.PollInterval); err == nil {
			pollInterval = d
		}
	}

	watchCfg := cfg.Watch
	dispatchInterval := watchCfg.DispatchIntervalDuration()
	cleanupInterval := watchCfg.CleanupIntervalDuration()
	concurrency := watchCfg.GetConcurrency()
	maxRetries := watchCfg.GetMaxRetries()

	fmt.Printf("[%s] Watch daemon started (poll=%s, dispatch=%s, cleanup=%s, concurrency=%d, maxRetries=%d)\n",
		time.Now().Format(time.RFC3339), pollInterval, dispatchInterval, cleanupInterval, concurrency, maxRetries)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		cancel()
	}()

	// Create tagged contexts so dispatcher logs identify which queue they belong to
	setupCtx := clog.WithLogger(ctx, clog.FromContext(ctx).With("queue", "setup"))
	cleanupCtx := clog.WithLogger(ctx, clog.FromContext(ctx).With("queue", "cleanup"))

	// Create workqueues and reconcilers
	setupQueue := inmem.NewWorkQueue(10)
	cleanupQueue := inmem.NewWorkQueue(10)
	setupRec := reconciler.NewSetupReconciler(cfg)
	cleanupRec := reconciler.NewCleanupReconciler(cfg)

	seenPRs := loadSeenPRs()

	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()
	dispatchTicker := time.NewTicker(dispatchInterval)
	defer dispatchTicker.Stop()
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer cleanupTicker.Stop()

	// Log rotation ticker — check once per hour
	rotateTicker := time.NewTicker(1 * time.Hour)
	defer rotateTicker.Stop()

	// Initial poll
	pollOnce(ctx, seenPRs, setupQueue, setupRec)

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] Watch daemon stopping\n", time.Now().Format(time.RFC3339))
			os.Remove(pidFile())
			return nil

		case <-rotateTicker.C:
			rotateLogIfNeeded()

		case <-pollTicker.C:
			reloadConfig(setupRec, cleanupRec, pollTicker)
			pollOnce(ctx, seenPRs, setupQueue, setupRec)

		case <-dispatchTicker.C:
			if err := dispatcher.HandleAsync(setupCtx, setupQueue, concurrency, concurrency, setupRec.Reconcile, maxRetries)(); err != nil {
				fmt.Printf("[%s] Setup dispatch error: %v\n", time.Now().Format(time.RFC3339), err)
			}
			if err := dispatcher.HandleAsync(cleanupCtx, cleanupQueue, 1, 1, cleanupRec.Reconcile, 3)(); err != nil {
				fmt.Printf("[%s] Cleanup dispatch error: %v\n", time.Now().Format(time.RFC3339), err)
			}

		case <-cleanupTicker.C:
			reconciler.ScanMergedPRs(ctx, cfg, cleanupQueue, cfg.Watch.GetCleanupAfterDays())
		}
	}
}

// reloadConfig re-reads ~/.zen/config.yaml and updates the global cfg
// and reconcilers. If the poll interval changed, the ticker is reset.
func reloadConfig(setupRec *reconciler.SetupReconciler, cleanupRec *reconciler.CleanupReconciler, pollTicker *time.Ticker) {
	newCfg, err := config.Load()
	if err != nil {
		fmt.Printf("[%s] Config reload failed: %v\n", time.Now().Format(time.RFC3339), err)
		return
	}

	// Detect poll interval change
	oldInterval := 5 * time.Minute
	if cfg.PollInterval != "" {
		if d, err := time.ParseDuration(cfg.PollInterval); err == nil {
			oldInterval = d
		}
	}
	newInterval := 5 * time.Minute
	if newCfg.PollInterval != "" {
		if d, err := time.ParseDuration(newCfg.PollInterval); err == nil {
			newInterval = d
		}
	}

	if oldInterval != newInterval {
		pollTicker.Reset(newInterval)
		fmt.Printf("[%s] Config reloaded: poll_interval changed %s → %s\n",
			time.Now().Format(time.RFC3339), oldInterval, newInterval)
	}

	cfg = newCfg
	setupRec.SetConfig(newCfg)
	cleanupRec.SetConfig(newCfg)
}

type checkState struct {
	Timestamp string   `json:"timestamp"`
	PRCount   int      `json:"pr_count"`
	SeenPRs   []string `json:"seen_prs"`
}

func loadSeenPRs() map[string]bool {
	data, err := os.ReadFile(lastCheckFile())
	if err != nil {
		return make(map[string]bool)
	}
	var state checkState
	if err := json.Unmarshal(data, &state); err != nil {
		return make(map[string]bool)
	}
	m := make(map[string]bool)
	for _, pr := range state.SeenPRs {
		m[pr] = true
	}
	return m
}

func saveState(seenPRs map[string]bool, prCount int) {
	prs := make([]string, 0, len(seenPRs))
	for pr := range seenPRs {
		prs = append(prs, pr)
	}
	state := checkState{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		PRCount:   prCount,
		SeenPRs:   prs,
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(lastCheckFile(), data, 0o644)
}

func pollOnce(ctx context.Context, seenPRs map[string]bool, queue workqueue.Interface, rec *reconciler.SetupReconciler) {
	reviews, err := ghpkg.GetReviewRequests(ctx, "chainguard-dev/mono")
	if err != nil {
		fmt.Printf("[%s] Error fetching reviews: %v\n", time.Now().Format(time.RFC3339), err)
		return
	}

	for _, pr := range reviews {
		prKey := fmt.Sprintf("%d", pr.Number)
		if seenPRs[prKey] {
			continue
		}

		fmt.Printf("[%s] New PR review request: #%d - %s (by %s)\n",
			time.Now().Format(time.RFC3339), pr.Number, pr.Title, pr.Author.Login)

		notify.PRReview(pr.Number, pr.Title, pr.Author.Login, pr.Repository.Name)

		if cfg.IsAuthor(pr.Author.Login) {
			key := reconciler.MakePRKey(pr.Repository.Name, pr.Number)
			rec.StorePRData(key, pr)
			if err := queue.Queue(ctx, key, workqueue.Options{Priority: 1}); err != nil {
				fmt.Printf("[%s] Error queuing PR #%d: %v\n", time.Now().Format(time.RFC3339), pr.Number, err)
			} else {
				fmt.Printf("[%s] Queued PR #%d for setup (author: %s)\n",
					time.Now().Format(time.RFC3339), pr.Number, pr.Author.Login)
			}
		}

		seenPRs[prKey] = true
	}

	saveState(seenPRs, len(reviews))
}

const maxLogSize = 10 * 1024 * 1024 // 10 MB

// rotateLogIfNeeded checks the log file size and rotates if it exceeds maxLogSize.
// Keeps one previous log as watch.log.1. Since the daemon's stdout/stderr point
// to the log file, we reopen and replace them after rotation.
func rotateLogIfNeeded() {
	lf := logFile()
	info, err := os.Stat(lf)
	if err != nil || info.Size() < maxLogSize {
		return
	}

	// Rotate: watch.log → watch.log.1 (overwrite previous backup)
	backup := lf + ".1"
	os.Remove(backup)
	if err := os.Rename(lf, backup); err != nil {
		fmt.Printf("[%s] Log rotation: rename failed: %v\n", time.Now().Format(time.RFC3339), err)
		return
	}

	// Reopen a fresh log file and redirect stdout/stderr
	f, err := os.OpenFile(lf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Printf("[%s] Log rotation: reopen failed: %v\n", time.Now().Format(time.RFC3339), err)
		return
	}

	// Redirect stdout and stderr to the new log file
	os.Stdout = f
	os.Stderr = f

	fmt.Printf("[%s] Log rotated (previous log saved as watch.log.1)\n", time.Now().Format(time.RFC3339))
}
