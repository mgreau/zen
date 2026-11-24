package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A real `daemon start` forks a long-lived process that inherits stdout and
// stderr. The command must still return as soon as the starter exits.
func TestRunCodexDaemonCmdDoesNotWaitForForkedDaemon(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nsleep 5 &\necho started\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := runCodexDaemonCmd(bin, []string{"app-server", "daemon", "start"}, ""); err != nil {
		t.Fatalf("runCodexDaemonCmd: %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("runCodexDaemonCmd took %v; it waited on the forked daemon", d)
	}
}

func TestRunCodexDaemonCmdReportsOutputOnFailure(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'unknown subcommand' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(logPath, []byte("earlier run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runCodexDaemonCmd(bin, []string{"app-server", "daemon", "start"}, logPath)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("err = %v, want it to carry the command's output", err)
	}
	if strings.Contains(err.Error(), "earlier run") {
		t.Errorf("err = %v, want only this run's output", err)
	}
	if data, _ := os.ReadFile(logPath); !strings.Contains(string(data), "unknown subcommand") {
		t.Errorf("log = %q, want the output kept on disk", data)
	}
}

// A management command that hangs is killed at the timeout, but a daemon it
// already forked is left alone: killing the whole group would take down a
// daemon that a slow first start had just brought up.
func TestRunCodexDaemonCmdTimeoutSparesForkedDaemon(t *testing.T) {
	orig := codexDaemonTimeout
	codexDaemonTimeout = 300 * time.Millisecond
	t.Cleanup(func() { codexDaemonTimeout = orig })

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "daemon.pid")
	bin := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nsleep 30 &\necho $! > " + pidFile + "\nwait\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := runCodexDaemonCmd(bin, []string{"app-server", "daemon", "start"}, ""); err == nil {
		t.Fatal("runCodexDaemonCmd succeeded, want a timeout error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("runCodexDaemonCmd took %v after a 300ms timeout", d)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Errorf("forked daemon %d was killed by the timeout: %v", pid, err)
	}
}

func TestLockFileGivesUpAtDeadline(t *testing.T) {
	orig := codexLockTimeout
	codexLockTimeout = 200 * time.Millisecond
	t.Cleanup(func() { codexLockTimeout = orig })

	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := lockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// flock locks belong to the open file description, so a second open in
	// this process contends like another zen process would.
	if _, err := lockFile(path); err == nil {
		t.Fatal("second lockFile succeeded while the lock was held")
	}
}

// A fresh install has no ~/.zen/state yet; the log's directory must be
// created rather than silently disabling remote control.
func TestEnsureCodexRemoteDaemonCreatesLogDir(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "state", "codex-daemon.log")
	if err := ensureCodexRemoteDaemon(bin, logPath); err != nil {
		t.Fatalf("ensureCodexRemoteDaemon: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "app-server daemon enable-remote-control\napp-server daemon start\n" {
		t.Errorf("log = %q, want both daemon commands in order", got)
	}
}

// Concurrent launches must not run daemon commands at the same time.
func TestEnsureCodexRemoteDaemonSerialisesLaunches(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "running")
	bin := filepath.Join(dir, "codex")
	// Fails if another invocation is mid-flight (marker present).
	script := "#!/bin/sh\nif ! mkdir " + marker + " 2>/dev/null; then echo overlap; exit 1; fi\nsleep 0.1\nrmdir " + marker + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "codex-daemon.log")

	errs := make(chan error, 4)
	for range 4 {
		go func() { errs <- ensureCodexRemoteDaemon(bin, logPath) }()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Errorf("ensureCodexRemoteDaemon: %v", err)
		}
	}
}
