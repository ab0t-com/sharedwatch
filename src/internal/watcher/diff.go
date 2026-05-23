package watcher

import (
	"time"

	"sharedwatch/internal/events"
)

func DiffSnapshots(oldSnap, newSnap Snapshot, source events.Source) []events.Event {
	var out []events.Event
	seenOld := map[string]bool{}

	for rel, oldFile := range oldSnap.Files {
		seenOld[rel] = true
		newFile, ok := newSnap.Files[rel]
		if !ok {
			out = append(out, events.Event{
				ID:        newID("evt"),
				Type:      events.TypeDeleted,
				Path:      oldFile.Path,
				RelPath:   rel,
				Timestamp: newSnap.TakenAt,
				Source:    source,
				Size:      oldFile.Size,
				MTime:     oldFile.MTime,
				Hash:      oldFile.Hash,
				Status:    events.StatusPending,
			})
			continue
		}
		if changed(oldFile, newFile) {
			out = append(out, events.Event{
				ID:        newID("evt"),
				Type:      events.TypeModified,
				Path:      newFile.Path,
				RelPath:   rel,
				Timestamp: maxTime(newSnap.TakenAt, newFile.MTime),
				Source:    source,
				Size:      newFile.Size,
				MTime:     newFile.MTime,
				Hash:      newFile.Hash,
				Status:    events.StatusPending,
			})
		}
	}
	for rel, newFile := range newSnap.Files {
		if seenOld[rel] {
			continue
		}
		out = append(out, events.Event{
			ID:        newID("evt"),
			Type:      events.TypeCreated,
			Path:      newFile.Path,
			RelPath:   rel,
			Timestamp: maxTime(newSnap.TakenAt, newFile.MTime),
			Source:    source,
			Size:      newFile.Size,
			MTime:     newFile.MTime,
			Hash:      newFile.Hash,
			Status:    events.StatusPending,
		})
	}
	return out
}

func changed(a, b FileState) bool {
	return a.Size != b.Size || !a.MTime.Equal(b.MTime) || a.Hash != b.Hash || a.Path != b.Path
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
