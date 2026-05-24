package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	// WatchPath is the legacy single-root path. When WatchRoots is empty AND
	// WatchPath is set, the daemon treats WatchPath as one unlabeled root
	// (watch_root = "" on emitted events). When WatchRoots is non-empty,
	// WatchPath is ignored (and a warning logged on startup if both are set).
	WatchPath string
	// WatchRoots is the multi-root configuration. When non-empty, every entry
	// is a separately-watched directory tagged with its Label in the
	// watch_root column on events/digests/snapshots. See SW-AGENT-3.
	WatchRoots []WatchRoot

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

	// Agent-default fields (SW-AGENT-18). Populated from `config.yaml` and/or
	// SHAREDWATCH_* env vars. The CLI's attrFlags struct overlays these at
	// invocation time so the resolution chain is:
	//     flag > env > config (these fields) > built-in default.
	// All optional; empty = no default applied.
	Actor         string
	ActorKind     string
	Session       string
	Task          string
	Addressee     string
	DefaultFormat string // honoured by every `--format` flag when set
	DefaultRoot   string // honoured by `--root` filter on read commands
	DefaultSince  string // honoured by `events list --since` when no cursor
	Hints         string // honoured by `--hints`; aligns with SHAREDWATCH_HINTS
	CursorName    string // honoured by `events list --cursor-name`
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
		// Default ignore patterns. Path-segment-matched (so `.git` excludes
		// `.git/objects/abc` as well as a literal file named `.git`). The
		// universally-noisy dev artifacts are included so the journal isn't
		// drowned in build output on a typical developer's machine. Users
		// who *want* any of these tracked override via `ignore_patterns:`
		// in config.yaml.
		IgnorePatterns: []string{
			".git", ".DS_Store", "*.tmp", "*.swp",
			"node_modules", "__pycache__", ".cache",
			".venv", "venv", "target", "dist", "build",
			"*.log",
		},
		IncludePatterns: nil,
		RetentionDays:   30,
		HashEnabled:     false,
		HashMaxSize:     1 << 20, // 1 MB
		ProducerID:      fmt.Sprintf("%s:%d", host, os.Getpid()),
		ActorTTL:        5 * time.Minute,
	}
}
