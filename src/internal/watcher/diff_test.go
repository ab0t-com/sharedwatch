package watcher

import (
	"testing"
	"time"

	"sharedwatch/internal/events"
)

func TestDiffSnapshotsCreateModifyDelete(t *testing.T) {
	base := time.Now().UTC()
	oldSnap := Snapshot{TakenAt: base, Files: map[string]FileState{
		"keep.md":   {RelPath: "keep.md", Path: "/tmp/keep.md", Size: 10, MTime: base},
		"delete.md": {RelPath: "delete.md", Path: "/tmp/delete.md", Size: 8, MTime: base},
	}}
	newSnap := Snapshot{TakenAt: base.Add(time.Minute), Files: map[string]FileState{
		"keep.md":   {RelPath: "keep.md", Path: "/tmp/keep.md", Size: 11, MTime: base.Add(time.Minute)},
		"create.md": {RelPath: "create.md", Path: "/tmp/create.md", Size: 7, MTime: base.Add(time.Minute)},
	}}
	evs := DiffSnapshots(oldSnap, newSnap, events.SourceWatcher)
	if len(evs) != 3 {
		t.Fatalf("expected 3 events, got %d", len(evs))
	}
	seen := map[events.Type]int{}
	for _, e := range evs {
		seen[e.Type]++
	}
	if seen[events.TypeCreated] != 1 || seen[events.TypeModified] != 1 || seen[events.TypeDeleted] != 1 {
		t.Fatalf("unexpected type counts: %#v", seen)
	}
}
