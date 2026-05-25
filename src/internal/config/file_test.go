package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "watch_path: /tmp/shared\nactive_interval: 7s\nmax_batch_size: 12\nignore_patterns: a,b,c\nretention_days: 9\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WatchPath != "/tmp/shared" {
		t.Fatalf("unexpected watch path: %s", cfg.WatchPath)
	}
	if cfg.ActiveInterval != 7*time.Second {
		t.Fatalf("unexpected active interval: %s", cfg.ActiveInterval)
	}
	if cfg.MaxBatchSize != 12 {
		t.Fatalf("unexpected batch size: %d", cfg.MaxBatchSize)
	}
	if len(cfg.IgnorePatterns) != 3 {
		t.Fatalf("unexpected ignore patterns: %#v", cfg.IgnorePatterns)
	}
	if cfg.RetentionDays != 9 {
		t.Fatalf("unexpected retention days: %d", cfg.RetentionDays)
	}
}

// TestLoad_NewKeys (SW-AGENT-18 §1) covers the 4 previously-silent fields
// (include_patterns, hash_enabled, hash_max_size, producer_id) plus the 10
// new agent-default keys.
func TestLoad_NewKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
include_patterns: src/**, docs/**, *.go
hash_enabled: true
hash_max_size: 524288
producer_id: my-agent:42
actor: claude-coord-1
actor_kind: ai_agent
session: sess-2026-05-24-abc
task: refactor-auth
addressee: claude-reviewer
default_format: jsonl
default_root: auth
default_since: 24h
hints: agent
cursor_name: claude-coord-1
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Default())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"IncludePatterns count", len(cfg.IncludePatterns), 3},
		{"HashEnabled", cfg.HashEnabled, true},
		{"HashMaxSize", cfg.HashMaxSize, int64(524288)},
		{"ProducerID", cfg.ProducerID, "my-agent:42"},
		{"Actor", cfg.Actor, "claude-coord-1"},
		{"ActorKind", cfg.ActorKind, "ai_agent"},
		{"Session", cfg.Session, "sess-2026-05-24-abc"},
		{"Task", cfg.Task, "refactor-auth"},
		{"Addressee", cfg.Addressee, "claude-reviewer"},
		{"DefaultFormat", cfg.DefaultFormat, "jsonl"},
		{"DefaultRoot", cfg.DefaultRoot, "auth"},
		{"DefaultSince", cfg.DefaultSince, "24h"},
		{"Hints", cfg.Hints, "agent"},
		{"CursorName", cfg.CursorName, "claude-coord-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %v, want %v", c.got, c.want)
			}
		})
	}
}

func TestLoad_EmptyKeysPreserveDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("# empty config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Default())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HashEnabled {
		t.Errorf("empty config should preserve default HashEnabled=false")
	}
	if cfg.Actor != "" {
		t.Errorf("empty config should leave Actor empty, got %q", cfg.Actor)
	}
}

func TestLoad_MissingFileIsNoError(t *testing.T) {
	_, err := Load("/no/such/file.yaml", Default())
	if err != nil {
		t.Errorf("Load(missing): want nil error, got %v", err)
	}
}

func TestSearchConfig_ExplicitWins(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.yaml")
	if err := os.WriteFile(explicit, []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := SearchConfig(explicit)
	if r.LoadedPath != explicit {
		t.Errorf("explicit should win when it exists: got %q", r.LoadedPath)
	}
}

func TestSearchConfig_NoneExist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	// cd into a temp dir so ./config.yaml doesn't accidentally exist
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	r := SearchConfig("")
	if r.LoadedPath != "" {
		t.Errorf("no file should be loaded when none exist: got %q", r.LoadedPath)
	}
	if len(r.Searched) == 0 {
		t.Errorf("search trail should be populated even when nothing loads")
	}
}

func TestSearchConfig_ProjectLocalWinsOverXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "sharedwatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	xdgFile := filepath.Join(xdg, "sharedwatch", "config.yaml")
	if err := os.WriteFile(xdgFile, []byte("# xdg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	proj := t.TempDir()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config.yaml", []byte("# local\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := SearchConfig("")
	// Project-local "config.yaml" (relative path) should win over the XDG
	// absolute path. Assertion: result is exactly "config.yaml" and does
	// not include the XDG sharedwatch subdir in its path.
	if r.LoadedPath != "config.yaml" || strings.Contains(r.LoadedPath, "/sharedwatch/") {
		t.Errorf("project-local should win over XDG, got %q (xdg was %q)", r.LoadedPath, xdgFile)
	}
}

func TestSearchConfig_XDGWhenNoLocal(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "sharedwatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	xdgFile := filepath.Join(xdg, "sharedwatch", "config.yaml")
	if err := os.WriteFile(xdgFile, []byte("# xdg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	r := SearchConfig("")
	if r.LoadedPath != xdgFile {
		t.Errorf("XDG should win when no project-local exists: got %q want %q", r.LoadedPath, xdgFile)
	}
}

// SW-AGENT-30 Phase 1.2 tests — emit_profile / emit_overrides / emit_thresholds
// loaded from YAML; inline-map parsers handle good / bad / mixed inputs.

func TestLoadEmitConfigKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Join([]string{
		"emit_profile: verbose",
		"emit_overrides: actor_heartbeats=true,reconcile_per_cycle=false",
		"emit_thresholds: events_failed=99,events_stuck=4,reconcile_drift=15",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmitProfile != "verbose" {
		t.Fatalf("EmitProfile=%q want %q", cfg.EmitProfile, "verbose")
	}
	if v, ok := cfg.EmitOverrides["actor_heartbeats"]; !ok || v != true {
		t.Fatalf("override actor_heartbeats=%v ok=%v want true,true", v, ok)
	}
	if v, ok := cfg.EmitOverrides["reconcile_per_cycle"]; !ok || v != false {
		t.Fatalf("override reconcile_per_cycle=%v ok=%v want false,true", v, ok)
	}
	if cfg.EmitThresholds["events_failed"] != 99 {
		t.Fatalf("threshold events_failed=%d want 99", cfg.EmitThresholds["events_failed"])
	}
	if cfg.EmitThresholds["events_stuck"] != 4 {
		t.Fatalf("threshold events_stuck=%d want 4", cfg.EmitThresholds["events_stuck"])
	}
	if cfg.EmitThresholds["reconcile_drift"] != 15 {
		t.Fatalf("threshold reconcile_drift=%d want 15", cfg.EmitThresholds["reconcile_drift"])
	}
}

func TestLoadEmitConfigDefaultsPreservedWhenAbsent(t *testing.T) {
	// A v0.0.x config that doesn't mention any emit_* key should preserve
	// Default()'s values exactly. Regression guard for backwards compat.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("watch_path: /tmp/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmitProfile != "standard" {
		t.Fatalf("expected default emit_profile=standard, got %q", cfg.EmitProfile)
	}
	if cfg.EmitOverrides == nil {
		t.Fatal("EmitOverrides should be non-nil after Default() even when YAML omits it")
	}
	if len(cfg.EmitOverrides) != 0 {
		t.Fatalf("expected empty overrides, got %v", cfg.EmitOverrides)
	}
	if cfg.EmitThresholds["events_failed"] != 50 {
		t.Fatalf("expected default events_failed=50, got %d", cfg.EmitThresholds["events_failed"])
	}
	if cfg.EmitThresholds["events_stuck"] != 10 {
		t.Fatalf("expected default events_stuck=10, got %d", cfg.EmitThresholds["events_stuck"])
	}
	if cfg.EmitThresholds["reconcile_drift"] != 25 {
		t.Fatalf("expected default reconcile_drift=25, got %d", cfg.EmitThresholds["reconcile_drift"])
	}
}

func TestParseInlineBoolMap(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]bool
	}{
		{"", nil},
		{"   ", nil},
		{"a=true", map[string]bool{"a": true}},
		{"a=true,b=false", map[string]bool{"a": true, "b": false}},
		{"a=1,b=0,c=yes,d=no,e=on,f=off", map[string]bool{"a": true, "b": false, "c": true, "d": false, "e": true, "f": false}},
		{" a = true , b = false ", map[string]bool{"a": true, "b": false}}, // whitespace tolerance
		{"a=TRUE,b=False", map[string]bool{"a": true, "b": false}},         // case-insensitive
		{"a=maybe,b=true", map[string]bool{"b": true}},                     // bad value dropped, others kept
		{"=true,b=true", map[string]bool{"b": true}},                       // empty key dropped
		{"a,b=true", map[string]bool{"b": true}},                           // no `=` dropped
		{",,,", map[string]bool{}},                                         // all empty entries → empty map (not nil)
	}
	for _, tc := range cases {
		got := ParseInlineBoolMap(tc.in)
		if !boolMapEqual(got, tc.want) {
			t.Errorf("ParseInlineBoolMap(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseInlineIntMap(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]int
	}{
		{"", nil},
		{"   ", nil},
		{"a=5", map[string]int{"a": 5}},
		{"a=5,b=10", map[string]int{"a": 5, "b": 10}},
		{"a=0", map[string]int{"a": 0}},                    // zero is valid (disable threshold)
		{"a=-1", map[string]int{}},                         // negative dropped
		{"a=abc", map[string]int{}},                        // non-numeric dropped
		{"a=5,b=-1,c=10", map[string]int{"a": 5, "c": 10}}, // negative dropped, others kept
		{" a = 5 , b = 10 ", map[string]int{"a": 5, "b": 10}},
		{"=5,b=10", map[string]int{"b": 10}},
	}
	for _, tc := range cases {
		got := ParseInlineIntMap(tc.in)
		if !intMapEqual(got, tc.want) {
			t.Errorf("ParseInlineIntMap(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func boolMapEqual(a, b map[string]bool) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func intMapEqual(a, b map[string]int) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
