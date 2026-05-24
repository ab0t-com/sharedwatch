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
	"sharedwatch/internal/watcher"
)

func TestOverviewEmptyDB(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.WatchPath = t.TempDir()
	cfg.DBPath = filepath.Join(t.TempDir(), "queue.db")
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ov, err := a.ComputeOverview(ctx, time.Hour)
	if err != nil {
		t.Fatalf("ComputeOverview: %v", err)
	}
	if ov.FormatVersion != 1 {
		t.Fatalf("format_version: want 1, got %d", ov.FormatVersion)
	}
	if ov.EventsInRange != 0 || ov.Pending != 0 {
		t.Fatalf("expected empty counts, got pending=%d events_in_range=%d", ov.Pending, ov.EventsInRange)
	}
	// `next` is always present, even for empty journals (core drills:
	// events_recent + events_by_type).
	if len(ov.Next) == 0 {
		t.Fatal("next hints should never be empty")
	}
	for _, h := range ov.Next {
		if !strings.HasPrefix(h.Command, "sharedwatch ") {
			t.Fatalf("Next hint %s: command should start with `sharedwatch `, got %q", h.Name, h.Command)
		}
	}
}

func TestOverviewMultiRoot(t *testing.T) {
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

	if err := os.WriteFile(filepath.Join(authDir, "login.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billingDir, "charge.go"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reconcile.RunNow(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	ov, err := a.ComputeOverview(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if len(ov.Roots) != 2 {
		t.Fatalf("roots: want 2, got %d", len(ov.Roots))
	}
	if ov.EventsInRange != 2 {
		t.Fatalf("events_in_range: want 2, got %d", ov.EventsInRange)
	}
	foundRootHint := false
	foundActorHint := false
	for _, h := range ov.Next {
		if strings.Contains(h.Command, "--root auth") {
			foundRootHint = true
		}
		if strings.Contains(h.Command, "actor") && strings.Contains(h.Command, "claude-X") {
			foundActorHint = true
		}
	}
	if !foundRootHint {
		t.Fatalf("Next hints missing one for --root auth: %+v", ov.Next)
	}
	if !foundActorHint {
		t.Fatalf("Next hints missing one for actor=claude-X: %+v", ov.Next)
	}
}

func TestOverviewFormatVersionIsFirstKey(t *testing.T) {
	// JSON key ordering is part of the contract; format_version must be the
	// first key so consumers can validate the schema before scanning fields.
	ov := Overview{FormatVersion: 1}
	b, err := json.Marshal(ov)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `{"format_version":1,`) {
		t.Fatalf("format_version must be first key, got %s", string(b))
	}
}

func TestOverviewCachedWithinTTL(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.WatchPath = t.TempDir()
	cfg.DBPath = filepath.Join(t.TempDir(), "queue.db")
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// First call: builds + caches.
	ov1, err := a.ComputeOverview(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Inject a new event AFTER the cached compute. If the cache is honoured,
	// the second ComputeOverview should NOT see this event (events_in_range
	// stays 0). If the cache is busted, the new event would increment.
	if _, err := a.Watcher.EmitSynthetic(ctx, "post-cache.md", events.TypeCreated, watcher.SnapshotSourceWatcher); err != nil {
		t.Fatal(err)
	}

	ov2, err := a.ComputeOverview(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ov2.EventsInRange != ov1.EventsInRange {
		t.Fatalf("cache miss inside TTL: ov1.events_in_range=%d ov2.events_in_range=%d", ov1.EventsInRange, ov2.EventsInRange)
	}

	// Non-default window bypasses the cache.
	ov3, err := a.ComputeOverview(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ov3.EventsInRange == 0 {
		t.Fatal("non-cached compute should see the new event")
	}
}
