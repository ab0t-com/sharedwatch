package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"sharedwatch/internal/config"
)

// handleConfigShow implements `sharedwatch config show`.
//
// It prints the resolved-effective Config (after layering yaml + env), the
// SHAREDWATCH_* env vars detected this invocation, the config files that
// were searched (and which one loaded), and the resolution-order legend.
//
// Sufficient for debugging "why isn't my SHAREDWATCH_X being read?"
// without per-key provenance tracking — see SW-AGENT-18 §2 "out of scope".
func handleConfigShow(_ context.Context, cfg config.Config, search config.SearchResult, args []string) {
	fs := flag.NewFlagSet("config show", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit as JSON envelope (format_version: 1)")
	_ = fs.Parse(args)

	envDetected := make(map[string]string)
	for _, name := range knownEnvVars {
		if v := os.Getenv(name); v != "" {
			envDetected[name] = v
		}
	}

	if *asJSON {
		// SW-AGENT-25 (F36-A): emit a presentation DTO with snake_case keys
		// and human-readable duration strings, matching every other JSON
		// endpoint. Previously dumped the raw config.Config struct, which
		// produced Go CapitalCase keys and nanosecond-int durations — the
		// only endpoint in the binary with that shape. The text-mode key
		// names in effectiveConfigRows() are the source of truth.
		envelope := struct {
			FormatVersion       int                 `json:"format_version"`
			Effective           effectiveConfigJSON `json:"effective"`
			Env                 map[string]string   `json:"env"`
			ConfigFilesSearched []searchEntryJSON   `json:"config_files_searched"`
			ResolutionOrder     []string            `json:"resolution_order"`
		}{
			FormatVersion:       1,
			Effective:           toEffectiveConfigJSON(cfg),
			Env:                 envDetected,
			ConfigFilesSearched: toSearchEntryJSON(search.Searched),
			ResolutionOrder:     []string{"flag", "env", "config", "default"},
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(envelope)
		return
	}

	// Text mode: column-aligned for legibility.
	fmt.Println("EFFECTIVE CONFIG")
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, kv := range effectiveConfigRows(cfg) {
		if kv[0] == "" {
			// Visual separator row — flush the tab-writer so the blank
			// line doesn't get column-aligned and look like a stray ":".
			_ = tw.Flush()
			fmt.Println()
			tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			continue
		}
		fmt.Fprintf(tw, "  %s:\t%s\n", kv[0], kv[1])
	}
	_ = tw.Flush()

	fmt.Println()
	fmt.Println("ENV VARS DETECTED")
	if len(envDetected) == 0 {
		fmt.Println("  (none of SHAREDWATCH_ACTOR / _ACTOR_KIND / _SESSION / _TASK / _ADDRESSEE / _FORMAT / _ROOT / _CURSOR_NAME / _HINTS are set)")
	} else {
		names := make([]string, 0, len(envDetected))
		for k := range envDetected {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Printf("  %s=%s\n", n, envDetected[n])
		}
	}

	fmt.Println()
	fmt.Println("CONFIG FILES SEARCHED")
	if len(search.Searched) == 0 {
		fmt.Println("  (none — built-in defaults only)")
	} else {
		for _, e := range search.Searched {
			mark := "✗"
			suffix := "  (not present)"
			if e.Exists {
				mark = "✓"
				if e.Loaded {
					suffix = "  (loaded)"
				} else {
					suffix = "  (shadowed by higher-precedence)"
				}
			}
			fmt.Printf("  %s %s%s\n", mark, e.Path, suffix)
		}
	}

	fmt.Println()
	fmt.Println("RESOLUTION ORDER")
	fmt.Println("  flag > env > config > built-in default")
}

// effectiveConfigRows returns the ordered list of [key, value-string]
// pairs for text display. Field ordering is deliberate — most-used at
// the top, agent defaults next, daemon knobs after.
func effectiveConfigRows(cfg config.Config) [][2]string {
	rows := [][2]string{
		{"watch_path", cfg.WatchPath},
		{"db_path", cfg.DBPath},
		{"data_dir", cfg.DataDir},
		{"watch_roots", formatWatchRoots(cfg.WatchRoots)},
		{"", ""}, // visual separator
		{"actor", defaultDashLocal(cfg.Actor)},
		{"actor_kind", defaultDashLocal(cfg.ActorKind)},
		{"session", defaultDashLocal(cfg.Session)},
		{"task", defaultDashLocal(cfg.Task)},
		{"addressee", defaultDashLocal(cfg.Addressee)},
		{"default_format", defaultDashLocal(cfg.DefaultFormat)},
		{"default_root", defaultDashLocal(cfg.DefaultRoot)},
		{"default_since", defaultDashLocal(cfg.DefaultSince)},
		{"hints", defaultDashLocal(cfg.Hints)},
		{"cursor_name", defaultDashLocal(cfg.CursorName)},
		{"", ""},
		{"coalesce_window", cfg.CoalesceWindow.String()},
		{"passive_interval", cfg.PassiveInterval.String()},
		{"active_interval", cfg.ActiveInterval.String()},
		{"reconcile_interval", cfg.ReconcileInterval.String()},
		{"max_batch_size", fmt.Sprintf("%d", cfg.MaxBatchSize)},
		{"retention_days", fmt.Sprintf("%d", cfg.RetentionDays)},
		{"actor_ttl", cfg.ActorTTL.String()},
		{"hash_enabled", fmt.Sprintf("%v", cfg.HashEnabled)},
		{"hash_max_size", fmt.Sprintf("%d", cfg.HashMaxSize)},
		{"producer_id", cfg.ProducerID},
		{"ignore_patterns", strings.Join(cfg.IgnorePatterns, ", ")},
		{"include_patterns", strings.Join(cfg.IncludePatterns, ", ")},
	}
	// Drop trailing empty rows for cleaner output if cfg lacks the separator targets.
	return rows
}

func formatWatchRoots(rs []config.WatchRoot) string {
	if len(rs) == 0 {
		return "(none — single-root mode)"
	}
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s=%s", r.Label, r.Path))
	}
	return strings.Join(parts, ", ")
}

func defaultDashLocal(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// effectiveConfigJSON is the snake_case, duration-string presentation DTO for
// `config show --json`. Field order mirrors effectiveConfigRows() so text and
// JSON stay in step. Keys deliberately match the conventions used by
// status/intent/lease/events JSON output (snake_case, human-readable
// durations) — see SW-AGENT-25 (F36-A).
type effectiveConfigJSON struct {
	WatchPath  string     `json:"watch_path"`
	DBPath     string     `json:"db_path"`
	DataDir    string     `json:"data_dir,omitempty"`
	WatchRoots []rootJSON `json:"watch_roots,omitempty"`

	Actor         string `json:"actor,omitempty"`
	ActorKind     string `json:"actor_kind,omitempty"`
	Session       string `json:"session,omitempty"`
	Task          string `json:"task,omitempty"`
	Addressee     string `json:"addressee,omitempty"`
	DefaultFormat string `json:"default_format,omitempty"`
	DefaultRoot   string `json:"default_root,omitempty"`
	DefaultSince  string `json:"default_since,omitempty"`
	Hints         string `json:"hints,omitempty"`
	CursorName    string `json:"cursor_name,omitempty"`

	CoalesceWindow    string   `json:"coalesce_window"`
	PassiveInterval   string   `json:"passive_interval"`
	ActiveInterval    string   `json:"active_interval"`
	ActiveTTL         string   `json:"active_ttl"`
	ReconcileInterval string   `json:"reconcile_interval"`
	MaxBatchSize      int      `json:"max_batch_size"`
	RetentionDays     int      `json:"retention_days"`
	ActorTTL          string   `json:"actor_ttl"`
	HashEnabled       bool     `json:"hash_enabled"`
	HashMaxSize       int64    `json:"hash_max_size"`
	ProducerID        string   `json:"producer_id,omitempty"`
	IgnorePatterns    []string `json:"ignore_patterns,omitempty"`
	IncludePatterns   []string `json:"include_patterns,omitempty"`
}

type rootJSON struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

func toEffectiveConfigJSON(cfg config.Config) effectiveConfigJSON {
	out := effectiveConfigJSON{
		WatchPath:         cfg.WatchPath,
		DBPath:            cfg.DBPath,
		DataDir:           cfg.DataDir,
		Actor:             cfg.Actor,
		ActorKind:         cfg.ActorKind,
		Session:           cfg.Session,
		Task:              cfg.Task,
		Addressee:         cfg.Addressee,
		DefaultFormat:     cfg.DefaultFormat,
		DefaultRoot:       cfg.DefaultRoot,
		DefaultSince:      cfg.DefaultSince,
		Hints:             cfg.Hints,
		CursorName:        cfg.CursorName,
		CoalesceWindow:    cfg.CoalesceWindow.String(),
		PassiveInterval:   cfg.PassiveInterval.String(),
		ActiveInterval:    cfg.ActiveInterval.String(),
		ActiveTTL:         cfg.ActiveTTL.String(),
		ReconcileInterval: cfg.ReconcileInterval.String(),
		MaxBatchSize:      cfg.MaxBatchSize,
		RetentionDays:     cfg.RetentionDays,
		ActorTTL:          cfg.ActorTTL.String(),
		HashEnabled:       cfg.HashEnabled,
		HashMaxSize:       cfg.HashMaxSize,
		ProducerID:        cfg.ProducerID,
		IgnorePatterns:    cfg.IgnorePatterns,
		IncludePatterns:   cfg.IncludePatterns,
	}
	for _, r := range cfg.WatchRoots {
		out.WatchRoots = append(out.WatchRoots, rootJSON{Label: r.Label, Path: r.Path})
	}
	return out
}

type searchEntryJSON struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Loaded bool   `json:"loaded"`
}

func toSearchEntryJSON(in []config.SearchEntry) []searchEntryJSON {
	out := make([]searchEntryJSON, 0, len(in))
	for _, e := range in {
		out = append(out, searchEntryJSON{Path: e.Path, Exists: e.Exists, Loaded: e.Loaded})
	}
	return out
}
