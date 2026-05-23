package digest

import "time"

type Status string

const (
	StatusPending  Status = "pending"
	StatusRead     Status = "read"
	StatusArchived Status = "archived"
)

type Digest struct {
	ID          string
	CreatedAt   time.Time
	WindowStart time.Time
	WindowEnd   time.Time
	Mode        string
	EventCount  int
	Summary     string
	Status      Status
	// WatchRoot carries the dominant root label of the events this digest
	// summarises, or "mixed" when consumed events spanned more than one root.
	// Empty string is the single-root legacy sentinel (no roots configured).
	// See SW-AGENT-3.
	WatchRoot string
}
