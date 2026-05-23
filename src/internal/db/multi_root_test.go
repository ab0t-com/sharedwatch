package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/events"
)

// TestSchemaMigrationIsIdempotent ensures opening an existing DB twice with
// the SW-AGENT-3 binary does not double-add columns or indexes. Catches
// regressions in addColumnIfMissing or CREATE INDEX IF NOT EXISTS.
func TestSchemaMigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Re-open to re-trigger migrate(). Should be a no-op.
	s, err = Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("re-open should be idempotent: %v", err)
	}
	defer s.Close()

	// Confirm the new columns exist on all three tables.
	for _, tbl := range []string{"events", "digests", "snapshots"} {
		cols, err := s.tableColumns(ctx, tbl)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range cols {
			if c.Name == "watch_root" {
				found = true
				if c.Type != "TEXT" {
					t.Fatalf("%s.watch_root has type %s, want TEXT", tbl, c.Type)
				}
				if !c.NotNull {
					t.Fatalf("%s.watch_root should be NOT NULL", tbl)
				}
			}
		}
		if !found {
			t.Fatalf("%s missing watch_root column", tbl)
		}
	}
}

// TestCoalesceScopedToRoot is the SW-AGENT-3 cross-root correctness regression
// test. Two README.md events in different roots must stay distinct — neither
// the coalesce window nor the rename heuristic may merge them.
func TestCoalesceScopedToRoot(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := time.Now().UTC()
	window := 5 * time.Second

	mk := func(id, root string, dt time.Duration) events.Event {
		return events.Event{
			ID:        id,
			Type:      events.TypeModified,
			Path:      "/x/" + root + "/README.md",
			RelPath:   "README.md",
			Timestamp: base.Add(dt),
			Source:    events.SourceTest,
			Status:    events.StatusPending,
			WatchRoot: root,
		}
	}

	if err := s.InsertOrCoalesceEvent(ctx, mk("a", "auth", 0), window); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertOrCoalesceEvent(ctx, mk("b", "billing", 2*time.Second), window); err != nil {
		t.Fatal(err)
	}

	evs, err := s.QueryEvents(ctx, EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events across roots, got %d", len(evs))
	}
	roots := map[string]bool{}
	for _, e := range evs {
		roots[e.WatchRoot] = true
	}
	if !roots["auth"] || !roots["billing"] {
		t.Fatalf("expected both roots preserved, got %v", roots)
	}
}

// TestSnapshotsKeyedByRoot verifies that LatestSnapshot is scoped to
// (source, watch_root). Otherwise a populated root would get diffed against
// another root's snapshot and produce a phantom whole-tree create/delete
// cascade.
func TestSnapshotsKeyedByRoot(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	snapA := catalog.Snapshot{TakenAt: time.Now().UTC(), Files: map[string]catalog.FileState{
		"login.go": {RelPath: "login.go", Path: "/a/login.go", Size: 10},
	}}
	snapB := catalog.Snapshot{TakenAt: time.Now().UTC(), Files: map[string]catalog.FileState{
		"charge.go": {RelPath: "charge.go", Path: "/b/charge.go", Size: 20},
	}}

	if err := s.SaveSnapshot(ctx, "snp_a", "watcher", "auth", snapA); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot(ctx, "snp_b", "watcher", "billing", snapB); err != nil {
		t.Fatal(err)
	}

	gotA, ok, err := s.LatestSnapshot(ctx, "watcher", "auth")
	if err != nil || !ok {
		t.Fatalf("auth snapshot missing: ok=%v err=%v", ok, err)
	}
	if _, ok := gotA.Files["login.go"]; !ok {
		t.Fatalf("auth snapshot lost login.go: %+v", gotA.Files)
	}

	gotB, ok, _ := s.LatestSnapshot(ctx, "watcher", "billing")
	if !ok {
		t.Fatal("billing snapshot missing")
	}
	if _, ok := gotB.Files["charge.go"]; !ok {
		t.Fatalf("billing snapshot lost charge.go: %+v", gotB.Files)
	}

	// Cross-root read must NOT bleed: empty-root has no snapshot.
	if _, ok, _ := s.LatestSnapshot(ctx, "watcher", ""); ok {
		t.Fatal("empty-root snapshot lookup returned a non-empty result; cross-root bleed")
	}
}

// TestEventFilterByRoot exercises the WatchRoots IN-clause filter end-to-end.
func TestEventFilterByRoot(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	for i, r := range []string{"auth", "billing", "auth"} {
		if err := s.InsertEvent(ctx, events.Event{
			ID: "e" + string(rune('1'+i)), Type: events.TypeCreated, RelPath: "f", Path: "/x/f",
			Timestamp: now.Add(time.Duration(i) * time.Second), Source: events.SourceTest,
			Status: events.StatusPending, WatchRoot: r,
		}); err != nil {
			t.Fatal(err)
		}
	}

	authOnly, _ := s.QueryEvents(ctx, EventFilter{WatchRoots: []string{"auth"}})
	if len(authOnly) != 2 {
		t.Fatalf("auth filter: want 2, got %d", len(authOnly))
	}
	all, _ := s.QueryEvents(ctx, EventFilter{})
	if len(all) != 3 {
		t.Fatalf("no filter: want 3, got %d", len(all))
	}
	bothRoots, _ := s.QueryEvents(ctx, EventFilter{WatchRoots: []string{"auth", "billing"}})
	if len(bothRoots) != 3 {
		t.Fatalf("both-roots filter: want 3, got %d", len(bothRoots))
	}
}
