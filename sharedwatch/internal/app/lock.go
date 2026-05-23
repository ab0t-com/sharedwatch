package app

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileLock holds an OS-level exclusive lock on a path so a second `run`
// against the same data dir fails fast instead of corrupting state by racing.
type fileLock struct {
	path string
	f    *os.File
}

func acquireRunLock(dbPath string) (*fileLock, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("lock dir: %w", err)
	}
	path := filepath.Join(dir, "sharedwatch.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another sharedwatch process is using %s (lock held)", path)
	}
	// Write our PID so a human can see who's holding the lock.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return &fileLock{path: path, f: f}, nil
}

func (l *fileLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	_ = os.Remove(l.path)
}
