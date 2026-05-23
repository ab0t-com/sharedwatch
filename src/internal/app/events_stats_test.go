package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sharedwatch/internal/config"
	"sharedwatch/internal/events"
)

func TestEventsStatsRequiresRoot(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.WatchPath = t.TempDir()
	cfg.DBPath = filepath.Join(t.TempDir(), "queue.db")
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if _, err := a.ComputeEventsStats(ctx, "", time.Hour); err == nil {
		t.Fatal("expected error when --root is empty, got nil")
	}
}

func TestEventsStatsMultiRoot(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	authDir := filepath.Join(tmp, "auth")
	billingDir := filepath.Join(tmp, "billing")

	cfg := config.Default()
	cfg.DBPath = filepath.Join(tmp, "queue.db")
	cfg.WatchRoots = []config.WatchRoot{
		{Label: "auth", Path: authDir},
		{Label: "billing", Path: billingDir},
	}
	cfg.PayloadJSON = events.BuildPayloadV1(events.PayloadV1{Actor: "claude-X"})

	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "login.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "oauth.go"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billingDir, "charge.go"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reconcile.RunNow(ctx); err != nil {
		t.Fatal(err)
	}

	stats, err := a.ComputeEventsStats(ctx, "auth", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FormatVersion != 1 {
		t.Fatalf("format_version: want 1, got %d", stats.FormatVersion)
	}
	if stats.Root != "auth" {
		t.Fatalf("root: want auth, got %q", stats.Root)
	}
	// Scope must hold: billing's charge.go must NOT appear.
	for _, p := range stats.TopPaths {
		if strings.Contains(p.Path, "charge.go") {
			t.Fatalf("billing leaked into auth stats: %+v", stats.TopPaths)
		}
	}
	if stats.ByType["file.created"] != 2 {
		t.Fatalf("by_type[file.created] should be 2 for auth root, got %d", stats.ByType["file.created"])
	}
	if stats.ByActor["claude-X"] != 2 {
		t.Fatalf("by_actor[claude-X] should be 2, got %d", stats.ByActor["claude-X"])
	}
	if len(stats.Drill) == 0 {
		t.Fatal("drill map should never be empty")
	}
	if v, ok := stats.Drill["by_path"]; !ok || !strings.Contains(v, "--root auth") {
		t.Fatalf("by_path drill should reference --root auth: %q", v)
	}
}

func TestEventsStatsFormatVersionIsFirstKey(t *testing.T) {
	s := EventsStats{FormatVersion: 1, ByType: map[string]int{}, ByActor: map[string]int{}, Drill: map[string]string{}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `{"format_version":1,`) {
		t.Fatalf("format_version must be first key, got %s", string(b))
	}
}
