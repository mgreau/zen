package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// codexDaemonTimeout bounds each daemon management call so a wedged Codex
// install can never hang a launch, and codexWaitDelay how long a killed call
// may take to exit. Variables so tests can shorten them.
var (
	codexDaemonTimeout = 30 * time.Second
	codexWaitDelay     = time.Second
)

// codexDaemonCmds are the management commands each launch runs, in order.
var codexDaemonCmds = [][]string{
	{"app-server", "daemon", "enable-remote-control"},
	{"app-server", "daemon", "start"},
}

// codexLockTimeout bounds the wait for another launch's daemon setup. It must
// exceed the longest that setup can hold the lock — every command running to
// its timeout — or a launch would give up on a daemon that is coming up fine.
// A variable so tests can shorten it.
var codexLockTimeout = time.Duration(len(codexDaemonCmds))*(codexDaemonTimeout+codexWaitDelay) + 10*time.Second

// NeedsLaunchPrep reports whether PrepareLaunch has work to do (and may take a
// while), so callers can say so before calling it.
func (a *codexAgent) NeedsLaunchPrep() bool { return a.remoteControl }

// PrepareLaunch brings up the remote-control daemon when remote control is on.
// The returned agent builds commands that attach to it. If the daemon cannot
// be brought up, the returned agent launches locally instead — a broken daemon
// never stops a session from starting — and the error says why, for the
// caller to log.
func (a *codexAgent) PrepareLaunch(workDir string) (Agent, error) {
	if !a.remoteControl {
		return a, nil
	}
	local := *a
	local.remoteControl = false

	// Resolve the binary once so a daemon zen starts and the TUI (otherwise
	// resolved from the tab's login-shell PATH) come from the same install.
	// This cannot help a daemon that is already running: `daemon start` leaves
	// it alone, so after a Codex upgrade it keeps the old build until
	// `codex app-server daemon restart` (see docs/configuration.md).
	abs, err := exec.LookPath(expandHome(a.bin))
	if err != nil {
		return &local, fmt.Errorf("codex_bin %q is not an executable path (remote control needs one): %w", a.bin, err)
	}
	if abs, err = filepath.Abs(abs); err != nil {
		return &local, err
	}
	if err := ensureCodexRemoteDaemon(abs, a.daemonLog); err != nil {
		return &local, err
	}
	prepared := *a
	prepared.launchBin = shellQuote(abs)
	prepared.workDir = workDir
	return &prepared, nil
}

// cmdBin is the executable as written into launch lines: the resolved, quoted
// binary for a prepared remote launch, otherwise the configured bin verbatim.
func (a *codexAgent) cmdBin() string {
	if a.launchBin != "" {
		return a.launchBin
	}
	return a.bin
}

// remoteFlags returns the flags that attach a launch to the daemon, or "" for
// an agent PrepareLaunch has not readied (including print-only commands, for
// which zen never starts a daemon). The daemon is shared by every session and
// runs from "/", so -C pins each thread to its worktree; the path is quoted
// here rather than left to the shell to expand.
func (a *codexAgent) remoteFlags() string {
	if !a.remoteControl || a.launchBin == "" {
		return ""
	}
	flags := " --remote unix://"
	if a.workDir != "" {
		flags += " -C " + shellQuote(a.workDir)
	}
	return flags
}

// expandHome expands a leading "~/" the way the launching shell would.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(os.Getenv("HOME"), p[2:])
	}
	return p
}

// ensureCodexRemoteDaemon makes sure Codex's local app-server daemon is running
// with remote control enabled. It is a variable so tests can stub it out
// instead of starting a real daemon.
//
// Both subcommands are idempotent: enable-remote-control applies to future
// starts and to a running managed daemon, and `daemon start` starts one only
// if it is not already running. They run from zen rather than from the
// launched shell line so that:
//   - the daemon's cwd is "/", never a worktree — session detection matches
//     codex processes by cwd, and a daemon parked in the first worktree would
//     make that worktree look busy for the daemon's whole lifetime;
//   - it is in its own session (setsid), so closing the tab that launched the
//     first session cannot SIGHUP the daemon out from under the others;
//   - the launch line needs no shell grouping syntax (fish 3 has no `{ }`).
//
// Concurrent launches (e.g. `zen watch` starting two Slack sessions at once)
// are serialised with a lock file next to the log, so they never race to
// start the daemon.
var ensureCodexRemoteDaemon = func(bin, logPath string) error {
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(logPath), err)
		}
		unlock, err := lockFile(logPath + ".lock")
		if err != nil {
			return err
		}
		defer unlock()
	}
	for _, args := range codexDaemonCmds {
		if err := runCodexDaemonCmd(bin, args, logPath); err != nil {
			return err
		}
	}
	return nil
}

// lockFile takes an exclusive flock on path, creating it if needed, giving up
// after codexLockTimeout. It is a
// separate file from the log on purpose: the daemon inherits the log's file
// descriptor, and a lock on that descriptor would be held for the daemon's
// lifetime. Go opens files close-on-exec, so this one never leaks.
func lockFile(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	deadline := time.Now().Add(codexLockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		retry := err == syscall.EWOULDBLOCK || err == syscall.EINTR
		if !retry || time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// runCodexDaemonCmd runs one daemon management command with its output
// appended to logPath (discarded when logPath is empty). Output goes to a
// file, never a pipe: a daemon forked by `daemon start` inherits its parent's
// stdout/stderr, and a pipe it held open would keep Wait from returning. The
// file stays on disk, so whatever the daemon writes there remains readable.
func runCodexDaemonCmd(bin string, args []string, logPath string) error {
	if logPath == "" {
		logPath = os.DevNull
	}
	out, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", logPath, err)
	}
	defer out.Close()
	offset, _ := out.Seek(0, io.SeekEnd)

	ctx, cancel := context.WithTimeout(context.Background(), codexDaemonTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = "/"
	cmd.Stdout = out
	cmd.Stderr = out
	// Setsid keeps the daemon out of zen's session and process group, so
	// neither a closed tab nor Ctrl-C on zen reaches it. A timeout kills only
	// the management command itself (CommandContext's default): killing its
	// whole group would also kill a daemon that a slow start had just forked.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = codexWaitDelay
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w%s", bin, strings.Join(args, " "), err, outputSince(logPath, offset))
	}
	return nil
}

// outputSince returns what a failed command appended to logPath, formatted
// for an error message.
func outputSince(logPath string, offset int64) string {
	if logPath == os.DevNull {
		return ""
	}
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	msg, _ := io.ReadAll(io.LimitReader(f, 4096))
	if s := strings.TrimSpace(string(msg)); s != "" {
		return ": " + s
	}
	return ""
}
