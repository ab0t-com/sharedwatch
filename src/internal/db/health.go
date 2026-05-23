package db

import (
	"context"
	"fmt"
	"time"
)

type Health struct {
	PendingEvents   int       `json:"pending_events"`
	FailedEvents    int       `json:"failed_events"`
	TotalDigests    int       `json:"total_digests"`
	UnreadDigests   int       `json:"unread_digests"`
	ArchivedDigests int       `json:"archived_digests"`
	CheckedAt       time.Time `json:"checked_at"`
}

func (s *Store) Health(ctx context.Context) (Health, error) {
	pending, err := s.countByQuery(ctx, `SELECT COUNT(*) FROM events WHERE status = 'pending'`)
	if err != nil {
		return Health{}, fmt.Errorf("health pending: %w", err)
	}
	failed, err := s.countByQuery(ctx, `SELECT COUNT(*) FROM events WHERE status = 'failed'`)
	if err != nil {
		return Health{}, fmt.Errorf("health failed: %w", err)
	}
	totalDigests, err := s.countByQuery(ctx, `SELECT COUNT(*) FROM digests`)
	if err != nil {
		return Health{}, fmt.Errorf("health digests: %w", err)
	}
	unreadDigests, err := s.countByQuery(ctx, `SELECT COUNT(*) FROM digests WHERE status = 'pending'`)
	if err != nil {
		return Health{}, fmt.Errorf("health unread digests: %w", err)
	}
	archivedDigests, err := s.countByQuery(ctx, `SELECT COUNT(*) FROM digests WHERE status = 'archived'`)
	if err != nil {
		return Health{}, fmt.Errorf("health archived digests: %w", err)
	}
	return Health{PendingEvents: pending, FailedEvents: failed, TotalDigests: totalDigests, UnreadDigests: unreadDigests, ArchivedDigests: archivedDigests, CheckedAt: time.Now().UTC()}, nil
}

func (s *Store) countByQuery(ctx context.Context, q string) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
