package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/events"
)

func TestQueryByProducerAndPayload(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.InsertEvent(ctx, events.Event{ID: "p1", Type: events.TypeCreated, RelPath: "a", Path: "/x/a", Timestamp: now, Source: events.SourceTest, Status: events.StatusPending, ProducerID: "agent-A", PayloadJSON: `{"thread":"auth"}`}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertEvent(ctx, events.Event{ID: "p2", Type: events.TypeCreated, RelPath: "b", Path: "/x/b", Timestamp: now.Add(time.Second), Source: events.SourceTest, Status: events.StatusPending, ProducerID: "agent-B", PayloadJSON: `{"thread":"billing"}`}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertEvent(ctx, events.Event{ID: "p3", Type: events.TypeCreated, RelPath: "c", Path: "/x/c", Timestamp: now.Add(2 * time.Second), Source: events.SourceTest, Status: events.StatusPending, ProducerID: "agent-A", PayloadJSON: `{"thread":"billing"}`}); err != nil {
		t.Fatal(err)
	}

	t.Run("producer filter", func(t *testing.T) {
		out, err := store.QueryEvents(ctx, EventFilter{ProducerIDs: []string{"agent-A"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 2 {
			t.Fatalf("expected 2 agent-A events, got %d", len(out))
		}
	})
	t.Run("payload filter", func(t *testing.T) {
		out, err := store.QueryEvents(ctx, EventFilter{PayloadKey: "thread", PayloadValue: "auth"})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].ID != "p1" {
			t.Fatalf("expected only p1, got %+v", idsOf(out))
		}
	})
	t.Run("producer + payload combined", func(t *testing.T) {
		out, err := store.QueryEvents(ctx, EventFilter{ProducerIDs: []string{"agent-A"}, PayloadKey: "thread", PayloadValue: "billing"})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].ID != "p3" {
			t.Fatalf("expected only p3, got %+v", idsOf(out))
		}
	})
}

func TestProducerIDPersisted(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	e := events.Event{ID: "x", Type: events.TypeCreated, RelPath: "x", Path: "/x", Timestamp: time.Now().UTC(), Source: events.SourceTest, Status: events.StatusPending, ProducerID: "agent-Z"}
	if err := store.InsertEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	out, _ := store.QueryEvents(ctx, EventFilter{})
	if len(out) != 1 || out[0].ProducerID != "agent-Z" {
		t.Fatalf("expected ProducerID round-trip, got %+v", out)
	}
}
