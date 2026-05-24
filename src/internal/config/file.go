package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SearchResult records what the config-search step actually did, so
// `config show` can render which paths were considered and which one
// won. Sufficient for debugging; we don't track per-key provenance
// (intentional — see SW-AGENT-18 §2 "out of scope").
type SearchResult struct {
	// LoadedPath is the absolute path of the config file that won the
	// search, or "" when no file was found / loaded.
	LoadedPath string
	// Searched is the ordered list of paths the search considered.
	// Each entry's Loaded boolean is true only for the winner (at most one).
	Searched []SearchEntry
}

type SearchEntry struct {
	Path   string
	Exists bool
	Loaded bool
}

// SearchConfig returns the first existing config file from the search
// order, plus the full search trail for introspection.
//
// Order (lowest precedence first — but search returns the highest that exists):
//   - $XDG_CONFIG_HOME/sharedwatch/config.yaml (or ~/.config/sharedwatch/config.yaml)
//   - ./config.yaml
//   - explicit (when non-empty)
//
// The highest-precedence existing path becomes LoadedPath. When explicit
// is set, it is always tried first even if it doesn't exist (the caller
// surface its absence as an error); for the others, missing files are
// silently skipped — this preserves the long-standing "config is
// optional" promise.
func SearchConfig(explicit string) SearchResult {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	candidates = append(candidates, "config.yaml")
	if p := xdgConfigPath(); p != "" {
		candidates = append(candidates, p)
	}

	out := SearchResult{Searched: make([]SearchEntry, 0, len(candidates))}
	for _, c := range candidates {
		exists := false
		if _, err := os.Stat(c); err == nil {
			exists = true
		}
		entry := SearchEntry{Path: c, Exists: exists}
		// First existing wins (explicit is first in the list, so it wins
		// over project-local, which wins over XDG).
		if exists && out.LoadedPath == "" {
			out.LoadedPath = c
			entry.Loaded = true
		}
		out.Searched = append(out.Searched, entry)
	}
	return out
}

// xdgConfigPath returns the $XDG_CONFIG_HOME-derived config file path
// (or "" when no usable home dir is available). Mirrors defaultDataHome
// in config.go but for the config tree.
func xdgConfigPath() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "sharedwatch", "config.yaml")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "sharedwatch", "config.yaml")
	}
	return ""
}

func Load(path string, base Config) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return base, err
	}
	defer f.Close()

	cfg := base
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch key {
		case "watch_path":
			cfg.WatchPath = val
		case "data_dir":
			cfg.DataDir = val
		case "db_path":
			cfg.DBPath = val
		case "recursive":
			cfg.Recursive = strings.EqualFold(val, "true")
		case "coalesce_window":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.CoalesceWindow = d
			}
		case "passive_interval":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.PassiveInterval = d
			}
		case "active_interval":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.ActiveInterval = d
			}
		case "active_ttl":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.ActiveTTL = d
			}
		case "reconcile_interval":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.ReconcileInterval = d
			}
		case "max_batch_size":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.MaxBatchSize = n
			}
		case "ignore_patterns":
			cfg.IgnorePatterns = splitCSV(val)
		case "retention_days":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.RetentionDays = n
			}
		case "actor_ttl":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.ActorTTL = d
			}
		case "watch_roots":
			// Compact inline format: `watch_roots: auth=/path/a, billing=/path/b`.
			// The trailing-list YAML form (`watch_roots:\n  - label: auth\n    path: /path/a`)
			// is intentionally not supported by this hand-rolled parser; pass
			// roots via repeated --root flags instead if you need YAML lists.
			cfg.WatchRoots = parseWatchRootsInline(val)

		// Previously-silent fields (SW-AGENT-18 §2 audit). These existed on
		// Config but `Load()` ignored their YAML keys, so users setting
		// `hash_enabled: true` in config.yaml would silently get the default.
		case "include_patterns":
			cfg.IncludePatterns = splitCSV(val)
		case "hash_enabled":
			cfg.HashEnabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "on") || strings.EqualFold(val, "yes")
		case "hash_max_size":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				cfg.HashMaxSize = n
			}
		case "producer_id":
			cfg.ProducerID = val

		// New agent-default keys (SW-AGENT-18 §1). All optional. Empty values
		// produce no default — they're only applied when explicitly set.
		case "actor":
			cfg.Actor = val
		case "actor_kind":
			cfg.ActorKind = val
		case "session":
			cfg.Session = val
		case "task":
			cfg.Task = val
		case "addressee":
			cfg.Addressee = val
		case "default_format":
			cfg.DefaultFormat = val
		case "default_root":
			cfg.DefaultRoot = val
		case "default_since":
			cfg.DefaultSince = val
		case "hints":
			cfg.Hints = val
		case "cursor_name":
			cfg.CursorName = val

		// SW-AGENT-29: --on-digest hook keys.
		case "on_digest":
			cfg.OnDigest = val
		case "on_digest_timeout":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.OnDigestTimeout = d
			}
		}
	}
	return cfg, s.Err()
}

// parseWatchRootsInline parses a comma-separated list of `label=path` entries.
// Entries without `=` are treated as paths with auto-derived labels; label
// validation and collision detection happens in NormalizeRoots, not here.
func parseWatchRootsInline(v string) []WatchRoot {
	parts := strings.Split(v, ",")
	out := make([]WatchRoot, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if i := strings.Index(p, "="); i >= 0 {
			out = append(out, WatchRoot{Label: strings.TrimSpace(p[:i]), Path: strings.TrimSpace(p[i+1:])})
		} else {
			out = append(out, WatchRoot{Path: p})
		}
	}
	return out
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
