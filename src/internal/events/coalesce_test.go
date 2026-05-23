package events

import (
	"testing"
	"time"
)

func TestShouldCoalesceModifyBurst(t *testing.T) {
	base := time.Now().UTC()
	existing := Event{RelPath: "a.md", Status: StatusPending, Type: TypeModified, Timestamp: base}
	incoming := Event{RelPath: "a.md", Status: StatusPending, Type: TypeModified, Timestamp: base.Add(2 * time.Second)}
	if !ShouldCoalesce(existing, incoming, 5*time.Second) {
		t.Fatal("expected modify events to coalesce")
	}
}

func TestCreateThenModifyStaysCreate(t *testing.T) {
	base := time.Now().UTC()
	existing := Event{RelPath: "a.md", Status: StatusPending, Type: TypeCreated, Timestamp: base}
	incoming := Event{RelPath: "a.md", Status: StatusPending, Type: TypeModified, Timestamp: base.Add(time.Second), Size: 42}
	merged := Coalesce(existing, incoming)
	if merged.Type != TypeCreated {
		t.Fatalf("expected created type, got %s", merged.Type)
	}
	if merged.Size != 42 {
		t.Fatalf("expected latest size, got %d", merged.Size)
	}
}
