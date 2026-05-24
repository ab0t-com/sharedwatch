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

	// SW-AGENT-29 (v0.1.0): meta-event types emitted by the --on-digest
	// hook subsystem. Every hook run produces exactly one meta-event so
	// hook activity is first-class in the journal — agents can query
	// `events list --type hook.completed --type hook.failed` to inspect
	// hook history without leaving the events surface.
	TypeHookCompleted Type = "hook.completed"
	TypeHookFailed    Type = "hook.failed"
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
	// SW-AGENT-29: meta-events from the --on-digest hook subsystem
	// stamp source=hook so they're easy to filter out of file-event
	// queries (`events list --source watcher,reconciler,test` excludes
	// hook noise).
	SourceHook Source = "hook"
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
	// WatchRoot is the label of the configured watch-root this event was
	// observed in. Empty string is the single-root sentinel (legacy callers
	// that don't configure --root see "" everywhere — backward compatible).
	// See SW-AGENT-3.
	WatchRoot string
}
