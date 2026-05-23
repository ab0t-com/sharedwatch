package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/events"
)

func TestRequeueFailedEvents(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for _, id := range []string{"a", "b", "c"} {
		if err := store.InsertEvent(ctx, events.Event{ID: id, Type: events.TypeCreated, Path: "/x/" + id, RelPath: id, Timestamp: now, Source: events.SourceTest, Status: events.StatusPending}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkEventsFailed(ctx, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	// First requeue: 2 events flip back.
	n, err := store.RequeueFailedEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 requeued, got %d", n)
	}
	pending, _ := store.PendingCount(ctx)
	if pending != 3 {
		t.Fatalf("expected all 3 events pending after requeue, got %d", pending)
	}
	// Second requeue: no failed events left.
	n2, err := store.RequeueFailedEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 on second requeue, got %d", n2)
	}
}
