package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sharedwatch/internal/config"
	"sharedwatch/internal/hints"
)

// handleStop implements `sharedwatch stop`.
//
// Reads the PID from `<data_dir>/sharedwatch.lock`, sends SIGTERM, polls
// for the lock file's removal (which the daemon does on graceful exit
// via fileLock.Release), and reports back. Optional --force escalates to
// SIGKILL after --timeout.
//
// This handler runs AFTER cfg resolution but BEFORE app.New is called —
// stopping the daemon doesn't need to open the DB. See main.go's pre-app
// switch for the routing.
//
// Exit codes:
//
//	0 = stopped cleanly, or no lock file (nothing to stop)
//	1 = daemon still running after timeout, and --force not set / SIGKILL failed
//	2 = usage error (handled by flag.ExitOnError)
func handleStop(_ context.Context, cfg config.Config, args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	timeout := fs.Duration("timeout", 10*time.Second, "max time to wait for graceful shutdown after SIGTERM")
	force := fs.Bool("force", false, "after --timeout, escalate to SIGKILL")
	_ = fs.Parse(args)

	lockPath := lockFilePath(cfg)

	data, err := os.ReadFile(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Printf("no running daemon (no lock file at %s)\n", lockPath)
		emitStopHint()
		return
	}
	if err != nil {
		fatal(fmt.Errorf("read lock file %s: %w", lockPath, err))
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		fatal(fmt.Errorf("malformed lock file %s: %q is not a pid", lockPath, pidStr))
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fatal(fmt.Errorf("locate pid %d: %w", pid, err))
	}

	// Send SIGTERM. On POSIX, FindProcess never fails so we have to
	// detect "process is gone" via the Signal call.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			fmt.Printf("daemon already gone (pid %d not found; lock file is stale and can be removed)\n", pid)
			return
		}
		fatal(fmt.Errorf("SIGTERM pid %d: %w", pid, err))
	}
	fmt.Printf("sent SIGTERM to pid %d, waiting up to %s for shutdown...\n", pid, timeout.String())

	// Poll for lock file removal. The daemon's fileLock.Release deletes
	// the file on graceful exit, so this is a robust readiness check.
	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(lockPath); errors.Is(err, os.ErrNotExist) {
			fmt.Println("daemon stopped cleanly")
			emitStopHint()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Timeout — escalate or report.
	if *force {
		if err := proc.Signal(syscall.SIGKILL); err != nil {
			fatal(fmt.Errorf("SIGKILL pid %d: %w", pid, err))
		}
		fmt.Printf("escalated to SIGKILL (pid %d); lock file may need manual removal\n", pid)
		// Still exit 1 because shutdown wasn't graceful — caller may want
		// to investigate why SIGTERM was ignored.
		os.Exit(1)
	}
	fmt.Printf("timeout: daemon still running after %s. Re-run with --force to escalate to SIGKILL, or `kill -9 %d` manually.\n", timeout.String(), pid)
	os.Exit(1)
}

// lockFilePath replicates the convention from internal/app/lock.go's
// acquireRunLock: the lock lives in the same directory as the DB file.
func lockFilePath(cfg config.Config) string {
	return filepath.Join(filepath.Dir(cfg.DBPath), "sharedwatch.lock")
}

func emitStopHint() {
	hints.RenderText(os.Stdout, hints.For("stop", hints.Context{
		Profile: resolveHintsProfile(false),
	}))
}
