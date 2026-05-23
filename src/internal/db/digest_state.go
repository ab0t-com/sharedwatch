package db

import (
	"context"
	"fmt"
)

func (s *Store) MarkDigestRead(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE digests SET status='read' WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("mark digest read: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrDigestNotFound, id)
	}
	return nil
}

func (s *Store) MarkDigestArchived(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE digests SET status='archived' WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("mark digest archived: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrDigestNotFound, id)
	}
	return nil
}
