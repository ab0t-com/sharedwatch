package consumer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sharedwatch/internal/db"
	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
)

func TestConsumePendingProducesDigestAndProcessesEvents(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.InsertEvent(ctx, events.Event{ID: "e1", Type: events.TypeCreated, Path: "/x/a.md", RelPath: "a.md", Timestamp: now, Source: events.SourceTest, Status: events.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertEvent(ctx, events.Event{ID: "e2", Type: events.TypeModified, Path: "/x/b.md", RelPath: "b.md", Timestamp: now.Add(time.Second), Source: events.SourceTest, Status: events.StatusPending}); err != nil {
		t.Fatal(err)
	}

	svc := Service{Store: store}
	d, ok, err := svc.ConsumePending(ctx, "passive", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected digest to be created")
	}
	if d.EventCount != 2 {
		t.Fatalf("expected 2 events in digest, got %d", d.EventCount)
	}
	if d.Status != digest.StatusPending {
		t.Fatalf("expected pending digest, got %s", d.Status)
	}
	if !strings.Contains(d.Summary, "a.md") || !strings.Contains(d.Summary, "b.md") {
		t.Fatalf("summary missing event paths: %s", d.Summary)
	}

	// Second call has nothing to consume.
	if _, ok, err := svc.ConsumePending(ctx, "passive", 100); err != nil || ok {
		t.Fatalf("expected no work on second call ok=%v err=%v", ok, err)
	}

	pending, _ := store.PendingCount(ctx)
	if pending != 0 {
		t.Fatalf("expected 0 pending events after consume, got %d", pending)
	}
}
