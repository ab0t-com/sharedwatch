package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
	"sharedwatch/internal/mode"
)

// Adapter is the storage boundary for sharedwatch.
//
// SQLite remains the default backend, but this interface makes storage type
// configurable so another adapter can be added later without rewriting the
// watcher / consumer / reconcile flows.
type Adapter interface {
	Close() error
	InsertOrCoalesceEvent(ctx context.Context, e events.Event, window time.Duration) error
	InsertOrCoalesceEventResult(ctx context.Context, e events.Event, window time.Duration) (coalescedIntoID string, err error)
	// InsertEvent is the plain insert primitive (no coalesce). Used by
	// the hook subsystem (SW-AGENT-29) to write meta-events that should
	// always be distinct rows — hook runs are user-triggered actions,
	// not file-change observations, so they should never merge.
	InsertEvent(ctx context.Context, e events.Event) error
	GetRuntime(ctx context.Context) (mode.Runtime, error)
	UpsertRuntime(ctx context.Context, r mode.Runtime) error
	GetRuntimeJSON(ctx context.Context, key string) (string, bool, error)
	UpsertRuntimeJSON(ctx context.Context, key, valueJSON string) error
	PendingCount(ctx context.Context) (int, error)
	PendingCountByRoot(ctx context.Context, watchRoot string) (int, error)
	// SW-AGENT-30 Phase 4.6: thresholds use these counters to decide
	// whether to emit events.failed_threshold / events.stuck_detected.
	FailedCount(ctx context.Context) (int, error)
	StuckCount(ctx context.Context, olderThan time.Duration) (int, error)
	LastEventAtByRoot(ctx context.Context, watchRoot string) (time.Time, error)
	Health(ctx context.Context) (Health, error)
	ListDigests(ctx context.Context, limit int) ([]digest.Digest, error)
	ListDigestsFiltered(ctx context.Context, limit int, status string) ([]digest.Digest, error)
	ListDigestsByFilter(ctx context.Context, f DigestFilter) ([]digest.Digest, error)
	GetDigest(ctx context.Context, id string) (digest.Digest, error)
	ClaimPendingEvents(ctx context.Context, limit int) ([]events.Event, error)
	InsertDigest(ctx context.Context, d digest.Digest) error
	MarkEventsProcessed(ctx context.Context, ids []string) error
	MarkEventsFailed(ctx context.Context, ids []string) error
	RequeueFailedEvents(ctx context.Context) (int64, error)
	RequeueFailedEventsWithLimit(ctx context.Context, maxRetries int) (int64, error)
	RecoverStuckProcessing(ctx context.Context, olderThan time.Duration) (int64, error)
	MarkDigestRead(ctx context.Context, id string) error
	MarkDigestArchived(ctx context.Context, id string) error
	SaveSnapshot(ctx context.Context, id string, source string, watchRoot string, snap catalog.Snapshot) error
	LatestSnapshot(ctx context.Context, source string, watchRoot string) (catalog.Snapshot, bool, error)
	PruneOldProcessedEvents(ctx context.Context, olderThan time.Duration) (int64, error)
	PruneArchivedDigests(ctx context.Context, olderThan time.Duration) (int64, error)
	PruneOldSnapshots(ctx context.Context, source string, watchRoot string, keep int) (int64, error)

	// Agent-facing surface (SW-AGENT-1).
	QueryEvents(ctx context.Context, f EventFilter) ([]events.Event, error)
	GetCursor(ctx context.Context, name string) (NamedCursor, bool, error)
	UpsertCursor(ctx context.Context, name string, p CursorPosition) error
	DeleteCursor(ctx context.Context, name string) error
	ListCursors(ctx context.Context) ([]NamedCursor, error)
	RawSQL(ctx context.Context, query string, allowWrite bool) ([]string, [][]any, error)
	Schema(ctx context.Context) ([]TableInfo, error)

	// Actors registry (SW-AGENT-8).
	UpsertActorHeartbeat(ctx context.Context, r ActorRecord) error
	GetActor(ctx context.Context, actorID string) (ActorRecord, bool, error)
	ListActors(ctx context.Context) ([]ActorRecord, error)
	PruneStaleActors(ctx context.Context, olderThan time.Duration) (int64, error)

	// Intents + leases (SW-AGENT-12).
	InsertIntent(ctx context.Context, r IntentRecord) error
	ListIntents(ctx context.Context, f IntentFilter) ([]IntentRecord, error)
	RevokeIntent(ctx context.Context, intentID string) error
	PruneExpiredIntents(ctx context.Context) (int64, error)
	InsertLease(ctx context.Context, r LeaseRecord) error
	ListLeases(ctx context.Context, f LeaseFilter) ([]LeaseRecord, error)
	ReleaseLease(ctx context.Context, leaseID string) error
	RenewLease(ctx context.Context, leaseID string, ttl time.Duration) (LeaseRecord, error)
	PruneExpiredLeases(ctx context.Context) (int64, error)
}

func OpenAdapter(ctx context.Context, storageType string, dbPath string) (Adapter, error) {
	switch strings.ToLower(strings.TrimSpace(storageType)) {
	case "", "sqlite":
		return Open(ctx, dbPath)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}
