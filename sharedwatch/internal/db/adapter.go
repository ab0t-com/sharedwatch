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
	GetRuntime(ctx context.Context) (mode.Runtime, error)
	UpsertRuntime(ctx context.Context, r mode.Runtime) error
	PendingCount(ctx context.Context) (int, error)
	Health(ctx context.Context) (Health, error)
	ListDigests(ctx context.Context, limit int) ([]digest.Digest, error)
	ListDigestsFiltered(ctx context.Context, limit int, status string) ([]digest.Digest, error)
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
	SaveSnapshot(ctx context.Context, id string, source string, snap catalog.Snapshot) error
	LatestSnapshot(ctx context.Context, source string) (catalog.Snapshot, bool, error)
	PruneOldProcessedEvents(ctx context.Context, olderThan time.Duration) (int64, error)
	PruneArchivedDigests(ctx context.Context, olderThan time.Duration) (int64, error)
	PruneOldSnapshots(ctx context.Context, source string, keep int) (int64, error)

	// Agent-facing surface (SW-AGENT-1).
	QueryEvents(ctx context.Context, f EventFilter) ([]events.Event, error)
	GetCursor(ctx context.Context, name string) (NamedCursor, bool, error)
	UpsertCursor(ctx context.Context, name string, p CursorPosition) error
	DeleteCursor(ctx context.Context, name string) error
	ListCursors(ctx context.Context) ([]NamedCursor, error)
	RawSQL(ctx context.Context, query string, allowWrite bool) ([]string, [][]any, error)
	Schema(ctx context.Context) ([]TableInfo, error)
}

func OpenAdapter(ctx context.Context, storageType string, dbPath string) (Adapter, error) {
	switch strings.ToLower(strings.TrimSpace(storageType)) {
	case "", "sqlite":
		return Open(ctx, dbPath)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}
