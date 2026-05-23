package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
)

func TestStoreEventClaimAndDigest(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	e := events.Event{
		ID:        "evt_1",
		Type:      events.TypeCreated,
		Path:      "/tmp/a.md",
		RelPath:   "a.md",
		Timestamp: time.Now().UTC(),
		Source:    events.SourceTest,
		Status:    events.StatusPending,
	}
	if err := store.InsertEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimPendingEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed event, got %d", len(claimed))
	}
	if claimed[0].Status != events.StatusProcessing {
		t.Fatalf("expected processing status, got %s", claimed[0].Status)
	}
	if err := store.MarkEventsProcessed(ctx, []string{"evt_1"}); err != nil {
		t.Fatal(err)
	}
	d := digest.Digest{ID: "d1", CreatedAt: time.Now().UTC(), WindowStart: time.Now().UTC().Add(-time.Minute), WindowEnd: time.Now().UTC(), Mode: "passive", EventCount: 1, Summary: "test", Status: digest.StatusPending}
	if err := store.InsertDigest(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDigest(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "d1" {
		t.Fatalf("unexpected digest id: %s", got.ID)
	}
}
