package app

import (
	"time"

	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
)

// queryEventsFilterForOverview is a small helper centralising the
// since-bounded filter the overview's aggregations use.
func queryEventsFilterForOverview(since time.Time, limit int) db.EventFilter {
	return db.EventFilter{Since: since, Limit: limit}
}

// extractActorJSON pulls the actor field out of a payload_json blob.
// Tolerates empty / non-JSON / forward-version payloads; returns "" when
// unable.
func extractActorJSON(s string) string {
	return events.ExtractActor(s)
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case []byte:
		if len(n) == 0 {
			return 0
		}
		// Stringy fallback: SQLite sometimes hands counts back as text.
		var x int
		for _, b := range n {
			if b < '0' || b > '9' {
				return x
			}
			x = x*10 + int(b-'0')
		}
		return x
	}
	return 0
}
