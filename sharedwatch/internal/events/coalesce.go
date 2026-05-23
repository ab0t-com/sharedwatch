package events

import "time"

func ShouldCoalesce(existing Event, incoming Event, window time.Duration) bool {
	if existing.RelPath == "" || incoming.RelPath == "" {
		return false
	}
	if existing.RelPath != incoming.RelPath {
		return false
	}
	if existing.Status != StatusPending {
		return false
	}
	if incoming.Timestamp.Sub(existing.Timestamp) > window {
		return false
	}

	if existing.Type == TypeCreated && incoming.Type == TypeModified {
		return true
	}
	if existing.Type == TypeModified && incoming.Type == TypeModified {
		return true
	}
	return false
}

func Coalesce(existing Event, incoming Event) Event {
	out := existing
	if incoming.Timestamp.After(existing.Timestamp) {
		out.Timestamp = incoming.Timestamp
	}
	if incoming.MTime.After(existing.MTime) {
		out.MTime = incoming.MTime
	}
	if incoming.Size > 0 {
		out.Size = incoming.Size
	}
	if incoming.Hash != "" {
		out.Hash = incoming.Hash
	}
	if existing.Type == TypeCreated && incoming.Type == TypeModified {
		out.Type = TypeCreated
	} else {
		out.Type = incoming.Type
	}
	out.PayloadJSON = incoming.PayloadJSON
	return out
}
