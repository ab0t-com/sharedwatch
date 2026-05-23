package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	WatchPath         string
	DataDir           string
	StorageType       string
	DBPath            string
	Recursive         bool
	CoalesceWindow    time.Duration
	PassiveInterval   time.Duration
	ActiveInterval    time.Duration
	ActiveTTL         time.Duration
	ReconcileInterval time.Duration
	MaxBatchSize      int
	IgnorePatterns    []string
	IncludePatterns   []string // positive filter applied before ignores; empty = match all
	RetentionDays     int
	HashEnabled       bool
	HashMaxSize       int64 // bytes; files larger than this aren't hashed even if HashEnabled
	ProducerID        string
	// ActorTTL determines when a registered actor (via `sharedwatch actor
	// heartbeat ...`) is considered stale. status --actors flags rows past
	// this threshold; the retention pass deletes rows past 2× this value.
	ActorTTL time.Duration
	// PayloadJSON is stamped onto every event emitted during the lifetime of
	// this Config (watcher diffs, reconcile cold-starts). Empty = no default
	// payload (the historical behaviour). Populated by the CLI when any of
	// --actor / --session / --task / --intent / --addressee / --ref / --tag is
	// set on the root flagset.
	PayloadJSON string
}

// defaultDataHome resolves the XDG_DATA_HOME spec: $XDG_DATA_HOME if set,
// otherwise $HOME/.local/share. Falls back to "./sharedwatch-data" if neither
// is available (e.g. system service with no HOME), so the binary never
// silently picks an unwritable path.
func defaultDataHome() string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "sharedwatch")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "sharedwatch")
	}
	return "sharedwatch-data"
}

func Default() Config {
	data := defaultDataHome()
	host, _ := os.Hostname()
	if host == "" {
		host = "host"
	}
	return Config{
		WatchPath:         filepath.Join(data, "watch"),
		DataDir:           data,
		StorageType:       "sqlite",
		DBPath:            filepath.Join(data, "queue.db"),
		Recursive:         true,
		CoalesceWindow:    5 * time.Second,
		PassiveInterval:   10 * time.Minute,
		ActiveInterval:    5 * time.Second,
		ActiveTTL:         30 * time.Minute,
		ReconcileInterval: 30 * time.Minute,
		MaxBatchSize:      100,
		IgnorePatterns:    []string{".git", ".DS_Store", "*.tmp", "*.swp"},
		IncludePatterns:   nil,
		RetentionDays:     30,
		HashEnabled:       false,
		HashMaxSize:       1 << 20, // 1 MB
		ProducerID:        fmt.Sprintf("%s:%d", host, os.Getpid()),
		ActorTTL:          5 * time.Minute,
	}
}
