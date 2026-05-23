package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/events"
)

// TestCoalesceScopedToActor is the regression test for SW-AGENT-11 (and the
// finding from dogfood scenario 17 in test_dogfood.md): two different actors
// modifying the same path inside the coalesce window must produce two distinct
// events, not one merged event with whichever actor's attribution happened to
// win.
func TestCoalesceScopedToActor(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Now().UTC()
	window := 5 * time.Second

	mk := func(id, actor string, dt time.Duration) events.Event {
		payload := ""
		if actor != "" {
			payload = events.BuildPayloadV1(events.PayloadV1{Actor: actor})
		}
		return events.Event{
			ID:          id,
			Type:        events.TypeModified,
			Path:        "/x/auth/login.go",
			RelPath:     "auth/login.go",
			Timestamp:   base.Add(dt),
			Source:      events.SourceTest,
			Status:      events.StatusPending,
			PayloadJSON: payload,
		}
	}

	t.Run("same actor same path inside window collapses to one", func(t *testing.T) {
		store2, _ := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
		defer store2.Close()
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a1", "claude-A", 0), window); err != nil {
			t.Fatal(err)
		}
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a2", "claude-A", 2*time.Second), window); err != nil {
			t.Fatal(err)
		}
		evs, _ := store2.QueryEvents(ctx, EventFilter{})
		if len(evs) != 1 {
			t.Fatalf("expected 1 coalesced event, got %d", len(evs))
		}
	})

	t.Run("different actors same path inside window stay distinct", func(t *testing.T) {
		store2, _ := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
		defer store2.Close()
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a1", "claude-A", 0), window); err != nil {
			t.Fatal(err)
		}
		if err := store2.InsertOrCoalesceEvent(ctx, mk("b1", "claude-B", 2*time.Second), window); err != nil {
			t.Fatal(err)
		}
		evs, _ := store2.QueryEvents(ctx, EventFilter{})
		if len(evs) != 2 {
			t.Fatalf("expected 2 distinct events across actors, got %d", len(evs))
		}
		// Both actors should be preserved.
		actors := map[string]bool{}
		for _, e := range evs {
			actors[events.ExtractActor(e.PayloadJSON)] = true
		}
		if !actors["claude-A"] || !actors["claude-B"] {
			t.Fatalf("expected both actors preserved, got %v", actors)
		}
	})

	t.Run("empty actor coalesces with empty actor (legacy single-tenant)", func(t *testing.T) {
		store2, _ := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
		defer store2.Close()
		if err := store2.InsertOrCoalesceEvent(ctx, mk("u1", "", 0), window); err != nil {
			t.Fatal(err)
		}
		if err := store2.InsertOrCoalesceEvent(ctx, mk("u2", "", 2*time.Second), window); err != nil {
			t.Fatal(err)
		}
		evs, _ := store2.QueryEvents(ctx, EventFilter{})
		if len(evs) != 1 {
			t.Fatalf("expected single-tenant legacy coalesce, got %d events", len(evs))
		}
	})

	t.Run("empty actor and named actor stay distinct", func(t *testing.T) {
		// If a single-tenant 'run' (no --actor) and a tagged synthetic event
		// both fire on the same path, they must NOT merge — otherwise the
		// tagged event's attribution would silently disappear.
		store2, _ := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
		defer store2.Close()
		if err := store2.InsertOrCoalesceEvent(ctx, mk("u1", "", 0), window); err != nil {
			t.Fatal(err)
		}
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a1", "claude-A", 2*time.Second), window); err != nil {
			t.Fatal(err)
		}
		evs, _ := store2.QueryEvents(ctx, EventFilter{})
		if len(evs) != 2 {
			t.Fatalf("expected 2 events (empty vs named actor), got %d", len(evs))
		}
	})

	t.Run("same actor outside window stays distinct", func(t *testing.T) {
		store2, _ := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
		defer store2.Close()
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a1", "claude-A", 0), window); err != nil {
			t.Fatal(err)
		}
		// 8s > 5s window
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a2", "claude-A", 8*time.Second), window); err != nil {
			t.Fatal(err)
		}
		evs, _ := store2.QueryEvents(ctx, EventFilter{})
		if len(evs) != 2 {
			t.Fatalf("expected 2 events outside window, got %d", len(evs))
		}
	})

	t.Run("three actors interleaved on same path all preserved", func(t *testing.T) {
		store2, _ := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
		defer store2.Close()
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a1", "claude-A", 0), window); err != nil {
			t.Fatal(err)
		}
		if err := store2.InsertOrCoalesceEvent(ctx, mk("b1", "claude-B", 1*time.Second), window); err != nil {
			t.Fatal(err)
		}
		if err := store2.InsertOrCoalesceEvent(ctx, mk("c1", "claude-C", 2*time.Second), window); err != nil {
			t.Fatal(err)
		}
		// A again — should coalesce into a1.
		if err := store2.InsertOrCoalesceEvent(ctx, mk("a2", "claude-A", 3*time.Second), window); err != nil {
			t.Fatal(err)
		}
		evs, _ := store2.QueryEvents(ctx, EventFilter{})
		if len(evs) != 3 {
			t.Fatalf("expected 3 events (A coalesced, B and C distinct), got %d", len(evs))
		}
	})

	// Silence unused-variable warning when the inner subtests use their own
	// scoped stores. base + mk + window are exercised through the closure.
	_ = base
}
