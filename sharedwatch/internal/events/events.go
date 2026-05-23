package events

import "time"

type Type string

type Status string

type Source string

const (
	TypeCreated  Type = "file.created"
	TypeModified Type = "file.modified"
	TypeDeleted  Type = "file.deleted"
	TypeRenamed  Type = "file.renamed"
)

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusProcessed  Status = "processed"
	StatusFailed     Status = "failed"
	StatusSuppressed Status = "suppressed"
)

const (
	SourceWatcher    Source = "watcher"
	SourceReconciler Source = "reconciler"
	SourceTest       Source = "test"
)

type Event struct {
	ID            string
	Type          Type
	Path          string
	RelPath       string
	OldPath       *string
	Timestamp     time.Time
	Source        Source
	Size          int64
	MTime         time.Time
	Hash          string
	Status        Status
	RetryCount    int
	CoalescedInto *string
	PayloadJSON   string
	ProducerID    string
}
