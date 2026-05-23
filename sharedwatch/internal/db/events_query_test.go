package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/events"
)

func seedEvents(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	cases := []events.Event{
		{ID: "e1", Type: events.TypeCreated, RelPath: "auth/login.go", Path: "/x/auth/login.go", Timestamp: base.Add(1 * time.Second), Source: events.SourceWatcher, Status: events.StatusProcessed},
		{ID: "e2", Type: events.TypeModified, RelPath: "auth/login.go", Path: "/x/auth/login.go", Timestamp: base.Add(2 * time.Second), Source: events.SourceWatcher, Status: events.StatusPending},
		{ID: "e3", Type: events.TypeCreated, RelPath: "billing/charge.go", Path: "/x/billing/charge.go", Timestamp: base.Add(3 * time.Second), Source: events.SourceReconciler, Status: events.StatusFailed},
		{ID: "e4", Type: events.TypeDeleted, RelPath: "auth/old.go", Path: "/x/auth/old.go", Timestamp: base.Add(4 * time.Second), Source: events.SourceWatcher, Status: events.StatusPending},
	}
	for _, e := range cases {
		if err := store.InsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueryEventsFilters(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedEvents(t, store)

	t.Run("no filter returns all", func(t *testing.T) {
		out, err := store.QueryEvents(ctx, EventFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 4 {
			t.Fatalf("expected 4, got %d", len(out))
		}
	})
	t.Run("type filter", func(t *testing.T) {
		out, _ := store.QueryEvents(ctx, EventFilter{Types: []string{"file.modified"}})
		if len(out) != 1 || out[0].ID != "e2" {
			t.Fatalf("unexpected: %+v", out)
		}
	})
	t.Run("source filter", func(t *testing.T) {
		out, _ := store.QueryEvents(ctx, EventFilter{Sources: []string{"reconciler"}})
		if len(out) != 1 || out[0].ID != "e3" {
			t.Fatalf("unexpected: %+v", out)
		}
	})
	t.Run("status multi-filter", func(t *testing.T) {
		out, _ := store.QueryEvents(ctx, EventFilter{Statuses: []string{"pending", "failed"}})
		if len(out) != 3 {
			t.Fatalf("expected 3, got %d", len(out))
		}
	})
	t.Run("path-glob shallow", func(t *testing.T) {
		out, _ := store.QueryEvents(ctx, EventFilter{PathGlob: "auth/*.go"})
		if len(out) != 3 {
			t.Fatalf("expected 3 (auth/login twice + auth/old), got %d", len(out))
		}
	})
	t.Run("path-glob deep", func(t *testing.T) {
		out, _ := store.QueryEvents(ctx, EventFilter{PathGlob: "auth/**"})
		if len(out) != 3 {
			t.Fatalf("expected 3, got %d", len(out))
		}
	})
	t.Run("since bound", func(t *testing.T) {
		out, _ := store.QueryEvents(ctx, EventFilter{Since: time.Date(2026, 5, 20, 0, 0, 3, 0, time.UTC)})
		if len(out) != 2 {
			t.Fatalf("expected 2 (e3, e4), got %d", len(out))
		}
	})
	t.Run("limit + order", func(t *testing.T) {
		out, _ := store.QueryEvents(ctx, EventFilter{Limit: 2, OrderAsc: true})
		if len(out) != 2 {
			t.Fatalf("expected 2, got %d", len(out))
		}
		if out[0].ID != "e1" || out[1].ID != "e2" {
			t.Fatalf("expected ASC e1,e2 got %s,%s", out[0].ID, out[1].ID)
		}
	})
}
