package events

import "time"

// ShouldCoalesce decides whether `incoming` should be folded into `existing`
// instead of becoming its own row. The contract:
//
//   - Same rel_path required.
//   - Existing must still be `pending` (not claimed by the consumer).
//   - Inside the coalesce window measured against existing.Timestamp.
//   - Same actor required (parsed from payload_json.actor). Two empty-actor
//     events still coalesce — preserves single-tenant behaviour. Two non-empty
//     actors with different values do NOT coalesce — the load-bearing fix
//     from SW-AGENT-11 / dogfood scenario 17.
//   - Only create→modify or modify→modify pairs (creates never absorb a
//     subsequent delete, etc.).
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
	if ExtractActor(existing.PayloadJSON) != ExtractActor(incoming.PayloadJSON) {
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
