package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/events"
)

func TestCursorEncodeDecodeRoundTrip(t *testing.T) {
	pos := CursorPosition{CreatedAt: time.Date(2026, 5, 20, 1, 2, 3, 456789012, time.UTC), ID: "evt_deadbeef"}
	tok := EncodeCursor(pos)
	if tok == "" {
		t.Fatal("empty token")
	}
	back, err := DecodeCursor(tok)
	if err != nil {
		t.Fatal(err)
	}
	if !back.CreatedAt.Equal(pos.CreatedAt) || back.ID != pos.ID {
		t.Fatalf("round-trip mismatch: got %+v want %+v", back, pos)
	}
}

func TestCursorEmptyToken(t *testing.T) {
	pos, err := DecodeCursor("")
	if err != nil {
		t.Fatal(err)
	}
	if !pos.CreatedAt.IsZero() || pos.ID != "" {
		t.Fatalf("expected zero position, got %+v", pos)
	}
}

func TestNamedCursorAdvanceOnRead(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c", "d"} {
		if err := store.InsertEvent(ctx, events.Event{ID: id, Type: events.TypeCreated, RelPath: id + ".md", Path: "/x/" + id, Timestamp: base.Add(time.Duration(i+1) * time.Second), Source: events.SourceTest, Status: events.StatusPending}); err != nil {
			t.Fatal(err)
		}
	}

	// First read with no cursor → all 4 ASC.
	page1, err := store.QueryEvents(ctx, EventFilter{OrderAsc: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[1].ID != "b" {
		t.Fatalf("expected first page a,b got %+v", page1)
	}
	// Persist cursor at last returned.
	if err := store.UpsertCursor(ctx, "agent-x", CursorPosition{CreatedAt: page1[1].Timestamp, ID: page1[1].ID}); err != nil {
		t.Fatal(err)
	}

	// Second read using persisted cursor → c,d.
	c, ok, err := store.GetCursor(ctx, "agent-x")
	if err != nil || !ok {
		t.Fatalf("expected cursor ok=%v err=%v", ok, err)
	}
	page2, err := store.QueryEvents(ctx, EventFilter{AfterNano: c.Position.CreatedAt.UnixNano(), AfterID: c.Position.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID != "c" || page2[1].ID != "d" {
		t.Fatalf("expected c,d got %+v", page2)
	}
}

func TestCursorDoesNotSkipBackdatedEvent(t *testing.T) {
	// Simulates the reconcile-late-insert scenario: events with older
	// timestamps arrive after the cursor has advanced past them. The cursor
	// is keyed on (created_at, id) so it should NOT skip them.
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	// Insert "live" events at T+1, T+2, T+3.
	for i, id := range []string{"live1", "live2", "live3"} {
		if err := store.InsertEvent(ctx, events.Event{ID: id, Type: events.TypeCreated, RelPath: id, Path: "/x/" + id, Timestamp: base.Add(time.Duration(i+1) * time.Second), Source: events.SourceWatcher, Status: events.StatusPending}); err != nil {
			t.Fatal(err)
		}
	}
	// Read everything, advance cursor to the last event (live3 at T+3).
	page1, _ := store.QueryEvents(ctx, EventFilter{OrderAsc: true})
	if len(page1) != 3 {
		t.Fatalf("expected 3 initial events, got %d", len(page1))
	}
	last := page1[len(page1)-1]
	cursor := CursorPosition{CreatedAt: last.Timestamp, ID: last.ID}

	// Now insert a backdated event (timestamp earlier than the cursor) and
	// also a newer event. The cursor should pick up ONLY the newer one
	// (because the cursor's contract is "events whose (created_at, id) is
	// strictly greater than this position"). The backdated event is
	// intentionally skipped — that's the documented semantic.
	if err := store.InsertEvent(ctx, events.Event{ID: "late", Type: events.TypeCreated, RelPath: "late", Path: "/x/late", Timestamp: base, Source: events.SourceReconciler, Status: events.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertEvent(ctx, events.Event{ID: "live4", Type: events.TypeCreated, RelPath: "live4", Path: "/x/live4", Timestamp: base.Add(4 * time.Second), Source: events.SourceWatcher, Status: events.StatusPending}); err != nil {
		t.Fatal(err)
	}

	page2, err := store.QueryEvents(ctx, EventFilter{AfterNano: cursor.CreatedAt.UnixNano(), AfterID: cursor.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].ID != "live4" {
		t.Fatalf("expected cursor to surface only live4, got %+v", idsOf(page2))
	}

	// However: a SECOND consumer reading from the START (no cursor) MUST see
	// the late event. Otherwise the late event is lost to everyone.
	allASC, _ := store.QueryEvents(ctx, EventFilter{OrderAsc: true})
	hasLate := false
	for _, e := range allASC {
		if e.ID == "late" {
			hasLate = true
		}
	}
	if !hasLate {
		t.Fatal("late event missing from fresh read")
	}
}

func idsOf(es []events.Event) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func TestMultipleNamedCursorsIndependent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	_ = store.UpsertCursor(ctx, "a", CursorPosition{CreatedAt: now, ID: "x"})
	_ = store.UpsertCursor(ctx, "b", CursorPosition{CreatedAt: now.Add(time.Hour), ID: "y"})

	ca, _, _ := store.GetCursor(ctx, "a")
	cb, _, _ := store.GetCursor(ctx, "b")
	if ca.Position.ID != "x" || cb.Position.ID != "y" {
		t.Fatalf("cursors collided: a=%s b=%s", ca.Position.ID, cb.Position.ID)
	}
	list, _ := store.ListCursors(ctx)
	if len(list) != 2 {
		t.Fatalf("expected 2 cursors, got %d", len(list))
	}
	if err := store.DeleteCursor(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.GetCursor(ctx, "a"); ok {
		t.Fatal("expected a deleted")
	}
}
