package events

import (
	"encoding/json"
	"log/slog"
)

// PayloadV1 is the canonical attribution schema embedded in events.payload_json.
// The schema_version field is always set to 1 on emission; the validation rules
// (soft, never reject) live in BuildPayloadV1 and ExtractActor.
//
// Field order in the struct controls field order in the marshalled JSON, so
// schema_version intentionally comes first.
type PayloadV1 struct {
	SchemaVersion int      `json:"schema_version"`
	Actor         string   `json:"actor,omitempty"`
	ActorKind     string   `json:"actor_kind,omitempty"`
	Session       string   `json:"session,omitempty"`
	Task          string   `json:"task,omitempty"`
	Intent        string   `json:"intent,omitempty"`
	Addressee     string   `json:"addressee,omitempty"`
	RefEventID    string   `json:"ref_event_id,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// IsV1Empty reports whether p carries no attribution at all. Callers should
// emit "" (no payload) rather than `{"schema_version":1}` in that case so the
// existing "no payload set" behavior is preserved.
func IsV1Empty(p PayloadV1) bool {
	return p.Actor == "" &&
		p.ActorKind == "" &&
		p.Session == "" &&
		p.Task == "" &&
		p.Intent == "" &&
		p.Addressee == "" &&
		p.RefEventID == "" &&
		len(p.Tags) == 0
}

// BuildPayloadV1 returns the canonical JSON string for p, or "" if p carries no
// attribution. SchemaVersion in p is ignored — the output always sets it to 1.
//
// Marshalling a fixed struct cannot fail in practice; if it ever does we
// degrade to "{}" and slog a warning rather than propagating an error, because
// the call sites are event-emission paths where dropping the event would be
// worse than emitting an unattributed one.
func BuildPayloadV1(p PayloadV1) string {
	if IsV1Empty(p) {
		return ""
	}
	p.SchemaVersion = 1
	b, err := json.Marshal(p)
	if err != nil {
		slog.Warn("BuildPayloadV1 marshal failed (unexpected)", "err", err)
		return "{}"
	}
	return string(b)
}

// ExtractActor returns the actor field from a payload_json blob, or "" if the
// blob is empty, not an object, or lacks the key. Unknown keys in the blob are
// silently tolerated. Schema-version mismatch does NOT block extraction — we
// want best-effort attribution even from forward-version payloads.
//
// Used by callers that need quick access to actor without parsing the full
// payload (e.g., coalesce, which keys on (rel_path, actor)).
func ExtractActor(payloadJSON string) string {
	if payloadJSON == "" || payloadJSON == "{}" {
		return ""
	}
	var probe struct {
		Actor string `json:"actor"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &probe); err != nil {
		return ""
	}
	return probe.Actor
}
