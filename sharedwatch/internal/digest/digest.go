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
}
