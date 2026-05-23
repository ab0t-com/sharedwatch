package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sharedwatch/internal/config"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
	"sharedwatch/internal/watcher"
)

// TestAttributionPropagatesIntoWatcherEvents verifies that when Config.PayloadJSON
// is set (as happens when CLI flags --actor / --session / ... are passed on the
// root flagset), every event emitted by the WATCHER's diff loop carries that
// attribution.
//
// This is the load-bearing behaviour for SW-AGENT-7 S1.3: it is what makes
// `sharedwatch --actor X run` produce attributed events for files dropped into
// the watched folder, not just for synthetic `test emit`s.
func TestAttributionPropagatesIntoWatcherEvents(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "watch")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.WatchPath = watchDir
	cfg.DBPath = filepath.Join(dir, "queue.db")
	cfg.PayloadJSON = events.BuildPayloadV1(events.PayloadV1{
		Actor:   "claude-X",
		Session: "sess-1",
		Task:    "t1",
	})

	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Drop a file into the watched folder so the watcher will see it on next scan.
	if err := os.WriteFile(filepath.Join(watchDir, "probe.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Watcher.ScanAndQueue(context.Background(), watcher.SnapshotSourceWatcher); err != nil {
		t.Fatalf("ScanAndQueue: %v", err)
	}

	evs, err := a.Store.QueryEvents(context.Background(), db.EventFilter{PayloadKey: "actor", PayloadValue: "claude-X"})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 event for actor=claude-X, got %d", len(evs))
	}
	if evs[0].RelPath != "probe.md" {
		t.Fatalf("expected rel_path=probe.md, got %q", evs[0].RelPath)
	}

	// Confirm the payload round-trips through PayloadV1 — actor + session + task
	// preserved, schema_version set.
	var p events.PayloadV1
	if err := json.Unmarshal([]byte(evs[0].PayloadJSON), &p); err != nil {
		t.Fatalf("payload_json should be valid v1 JSON: %v (raw=%q)", err, evs[0].PayloadJSON)
	}
	if p.SchemaVersion != 1 || p.Actor != "claude-X" || p.Session != "sess-1" || p.Task != "t1" {
		t.Fatalf("payload fields wrong: %+v", p)
	}
}

// TestAttributionPropagatesIntoReconcileEvents asserts the same plumbing on the
// reconciler's cold-start path. Without this, a recovery scan after a watcher
// gap would emit unattributed events even though the operator set --actor.
func TestAttributionPropagatesIntoReconcileEvents(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "watch")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant the file before the App is constructed — the very first reconcile
	// pass will cold-start against the populated directory.
	if err := os.WriteFile(filepath.Join(watchDir, "drift.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.WatchPath = watchDir
	cfg.DBPath = filepath.Join(dir, "queue.db")
	cfg.PayloadJSON = events.BuildPayloadV1(events.PayloadV1{Actor: "reconciler-actor"})

	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if _, err := a.Reconcile.RunNow(context.Background()); err != nil {
		t.Fatalf("Reconcile.RunNow: %v", err)
	}

	evs, err := a.Store.QueryEvents(context.Background(), db.EventFilter{PayloadKey: "actor", PayloadValue: "reconciler-actor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].RelPath != "drift.md" || evs[0].Source != events.SourceReconciler {
		t.Fatalf("reconciler attribution lost: %+v", evs)
	}
}

// TestEmptyAttributionPreservesLegacyBehaviour ensures that callers who don't
// set Config.PayloadJSON still see the historical "no payload" output (so this
// change is strictly additive).
func TestEmptyAttributionPreservesLegacyBehaviour(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "watch")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.WatchPath = watchDir
	cfg.DBPath = filepath.Join(dir, "queue.db")
	// PayloadJSON deliberately left "".

	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if err := os.WriteFile(filepath.Join(watchDir, "plain.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Watcher.ScanAndQueue(context.Background(), watcher.SnapshotSourceWatcher); err != nil {
		t.Fatal(err)
	}

	evs, err := a.Store.QueryEvents(context.Background(), db.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("expected at least one event")
	}
	for _, e := range evs {
		if e.PayloadJSON != "" && e.PayloadJSON != "{}" {
			t.Fatalf("legacy mode should emit empty payload, got %q on %s", e.PayloadJSON, e.RelPath)
		}
	}
}
