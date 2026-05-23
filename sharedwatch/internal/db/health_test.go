package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
)

func TestHealth(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.InsertEvent(ctx, events.Event{ID: "e1", Type: events.TypeCreated, Path: "/x/a.md", RelPath: "a.md", Timestamp: time.Now().UTC(), Source: events.SourceTest, Status: events.StatusPending})
	_ = store.InsertDigest(ctx, digest.Digest{ID: "d1", CreatedAt: time.Now().UTC(), WindowStart: time.Now().UTC(), WindowEnd: time.Now().UTC(), Mode: "passive", EventCount: 1, Summary: "x", Status: digest.StatusPending})
	h, err := store.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.PendingEvents != 1 || h.UnreadDigests != 1 {
		t.Fatalf("unexpected health: %#v", h)
	}
}
