package watcher

import (
	"testing"
	"time"

	"sharedwatch/internal/events"
)

func TestRenameKeyDistinguishesLargeSizes(t *testing.T) {
	mt := time.Now().UTC()
	a := events.Event{Size: 0x110000, MTime: mt}
	b := events.Event{Size: 0x110001, MTime: mt}
	if renameKey(a) == renameKey(b) {
		t.Fatalf("renameKey collides for distinct large sizes: %s", renameKey(a))
	}
}

func TestDetectRenames(t *testing.T) {
	mt := time.Now().UTC()
	evs := []events.Event{
		{ID: "d", Type: events.TypeDeleted, Path: "/x/old.md", RelPath: "old.md", Size: 10, MTime: mt},
		{ID: "c", Type: events.TypeCreated, Path: "/x/new.md", RelPath: "new.md", Size: 10, MTime: mt},
	}
	out := DetectRenames(evs)
	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}
	if out[0].Type != events.TypeRenamed {
		t.Fatalf("expected rename, got %s", out[0].Type)
	}
	if out[0].OldPath == nil || *out[0].OldPath != "old.md" {
		t.Fatalf("expected relative old path 'old.md', got %v", out[0].OldPath)
	}
}
