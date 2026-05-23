package db

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) PruneOldProcessedEvents(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM events WHERE status = 'processed' AND processed_at < ?`, time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune processed events: %w", err)
	}
	return res.RowsAffected()
}

func (s *Store) PruneArchivedDigests(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM digests WHERE status = 'archived' AND created_at < ?`, time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune archived digests: %w", err)
	}
	return res.RowsAffected()
}

// PruneOldSnapshots keeps only the most recent `keep` snapshot rows for the
// given source. With keep=1 the latest snapshot per source is retained, which
// is all LatestSnapshot ever reads.
func (s *Store) PruneOldSnapshots(ctx context.Context, source string, keep int) (int64, error) {
	if keep < 1 {
		keep = 1
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM snapshots
		WHERE source = ? AND id NOT IN (
			SELECT id FROM snapshots WHERE source = ? ORDER BY created_at DESC LIMIT ?
		)`, source, source, keep)
	if err != nil {
		return 0, fmt.Errorf("prune old snapshots: %w", err)
	}
	return res.RowsAffected()
}
