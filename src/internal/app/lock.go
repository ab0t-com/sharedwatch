package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// fileLock holds an OS-level exclusive lock on a path so a second `run`
// against the same data dir fails fast instead of corrupting state by racing.
type fileLock struct {
	path string
	f    *os.File
}

// staleLockInfo records what we found when we acquired a lock that
// the previous daemon should have cleaned up but didn't. Surfaced
// to the Run loop so it can emit a `daemon.crashed` event.
// SW-AGENT-30 Phase 4.2.
type staleLockInfo struct {
	// PriorPID is the PID written into the previous lockfile, or 0
	// if the file was unreadable / empty.
	PriorPID int
	// PriorLockAge is how old the prior lockfile was at the moment
	// we noticed it (best-effort; zero if stat failed).
	PriorLockAge time.Duration
}

// acquireRunLock acquires the daemon's exclusive run lock. Returns:
//   - lock: the held lock (caller defers Release)
//   - stale: non-nil if a prior lockfile existed at acquire time —
//     means the previous daemon didn't clean up (crashed). The Run
//     loop uses this to emit `daemon.crashed` into the journal.
//   - err: non-nil if the lock couldn't be acquired (another daemon
//     is actually running)
func acquireRunLock(dbPath string) (*fileLock, *staleLockInfo, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("lock dir: %w", err)
	}
	path := filepath.Join(dir, "sharedwatch.lock")

	// Detect stale-lock condition: if the file already exists when we
	// open it, AND we successfully acquire Flock below, the previous
	// daemon crashed (clean release would have unlinked the file via
	// Release()). Record what we can; the Run loop turns this into a
	// daemon.crashed event after the lock is held.
	var stale *staleLockInfo
	if st, err := os.Stat(path); err == nil {
		// File exists. Best-effort read of prior PID + age.
		s := &staleLockInfo{PriorLockAge: time.Since(st.ModTime())}
		if data, err := os.ReadFile(path); err == nil {
			line := strings.TrimSpace(string(data))
			if pid, perr := strconv.Atoi(line); perr == nil {
				s.PriorPID = pid
			}
		}
		stale = s
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("another sharedwatch process is using %s (lock held)", path)
	}
	// Write our PID so a human can see who's holding the lock.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return &fileLock{path: path, f: f}, stale, nil
}

// suppress unused-import warning when stale-lock features are disabled
// (kept for future extensibility — io is referenced by helper used by
// the SW-AGENT-30 lifecycle emit path).
var _ = io.Discard

func (l *fileLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	_ = os.Remove(l.path)
}
