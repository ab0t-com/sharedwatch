package watcher

import (
	"strconv"

	"sharedwatch/internal/events"
)

func DetectRenames(evs []events.Event) []events.Event {
	created := map[string]int{}
	deleted := map[string]int{}
	usedCreated := map[int]bool{}
	usedDeleted := map[int]bool{}
	for i, e := range evs {
		switch e.Type {
		case events.TypeCreated:
			key := renameKey(e)
			created[key] = i
		case events.TypeDeleted:
			key := renameKey(e)
			deleted[key] = i
		}
	}
	out := make([]events.Event, 0, len(evs))
	for i, e := range evs {
		if e.Type == events.TypeDeleted {
			key := renameKey(e)
			if ci, ok := created[key]; ok && !usedCreated[ci] && !usedDeleted[i] {
				re := evs[ci]
				// Preserve the *relative* old path so digest output stays
				// consistent with how every other event displays paths.
				oldPath := e.RelPath
				re.Type = events.TypeRenamed
				re.OldPath = &oldPath
				out = append(out, re)
				usedCreated[ci] = true
				usedDeleted[i] = true
				continue
			}
		}
		if usedCreated[i] || usedDeleted[i] {
			continue
		}
		out = append(out, e)
	}
	return out
}

func renameKey(e events.Event) string {
	return strconv.FormatInt(e.Size, 10) + "|" + e.MTime.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
}
