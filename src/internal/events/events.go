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

	// SW-AGENT-30 (v0.1.1): event-surface expansion. Constants below
	// are dead until Phase 4 wires them at their emit sites. Each
	// constant maps to a Class via TypeClass() in profile.go; class
	// determines which emit_profile tier the event ships at by default.
	//
	// Naming convention: `<domain>.<verb>` for the wire name; the Go
	// constant drops the dot and CamelCases. e.g. `daemon.started` →
	// `TypeDaemonStarted`.
	//
	// Once shipped, these names are STABLE CONTRACTS. New behaviour
	// gets a new name; we never redefine an existing one. See
	// docs/design/event-broker-consumer-contracts-20260525.md §2.

	// digest lifecycle — closes the symmetry gap (every other
	// operation has an event type; digest creation didn't until now).
	TypeDigestCreated Type = "digest.created"

	// daemon lifecycle — operators/watchdogs subscribe to detect
	// restarts, crashes, expected vs unexpected uptime.
	TypeDaemonStarted  Type = "daemon.started"
	TypeDaemonStopping Type = "daemon.stopping"
	TypeDaemonCrashed  Type = "daemon.crashed"

	// mode transitions — agents adjust poll cadence; dashboards light
	// up "team busy" indicators.
	TypeModeChanged     Type = "mode.changed"
	TypeModeTTLExtended Type = "mode.ttl_extended"

	// retention pass evidence — compliance/audit; cost analytics.
	TypeRetentionRan Type = "retention.ran"

	// failure thresholds — rising-edge events for on-call alerting.
	// Tunable via emit_thresholds.{events_failed, events_stuck}.
	TypeEventsFailedThreshold Type = "events.failed_threshold"
	TypeEventsStuckDetected   Type = "events.stuck_detected"
	TypeEventsRetriedBatch    Type = "events.retried_batch"

	// reconcile observability — operators tune coalesce window /
	// watcher health based on these.
	TypeReconcileRan           Type = "reconcile.ran"
	TypeReconcileDriftDetected Type = "reconcile.drift_detected"

	// snapshot — per reconcile pass per root.
	TypeSnapshotTaken Type = "snapshot.taken"

	// coord — lease lifecycle. Granted/released/renewed/violated are
	// the per-action signals; expired is emitted by reconcile when
	// TTL passes.
	TypeLeaseGranted  Type = "lease.granted"
	TypeLeaseReleased Type = "lease.released"
	TypeLeaseRenewed  Type = "lease.renewed"
	TypeLeaseExpired  Type = "lease.expired"
	TypeLeaseViolated Type = "lease.violated"

	// coord — intent lifecycle. Mirrors leases (declared/revoked are
	// per-action; expired emitted by reconcile).
	TypeIntentDeclared Type = "intent.declared"
	TypeIntentRevoked  Type = "intent.revoked"
	TypeIntentExpired  Type = "intent.expired"

	// actor registry — multi-agent coordination signals.
	TypeActorRegistered        Type = "actor.registered"
	TypeActorRemoved           Type = "actor.removed"
	TypeActorWentStale         Type = "actor.went_stale"
	TypeActorHeartbeatReceived Type = "actor.heartbeat_received"
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

	// SW-AGENT-30: new sources for the expanded event surface. Each
	// is a stable contract (never renamed). Consumer-side filtering
	// via `events list --source <name>` is the recommended way to
	// scope a query to a signal class.
	SourceDaemon    Source = "daemon"    // daemon.* + events.* threshold events
	SourceMode      Source = "mode"      // mode.changed + mode.ttl_extended
	SourceCoord     Source = "coord"     // lease.* + intent.*
	SourceActor     Source = "actor"     // actor.*
	SourceRetention Source = "retention" // retention.ran
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
