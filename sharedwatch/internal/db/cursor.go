package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// CursorPosition is the deterministic ordering position for event iteration.
// (CreatedAt, ID) is unique by construction — created_at carries nanoseconds
// and id is generated from crypto/rand.
type CursorPosition struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// EncodeCursor returns an opaque, URL-safe token. Empty position → empty token.
func EncodeCursor(p CursorPosition) string {
	if p.ID == "" && p.CreatedAt.IsZero() {
		return ""
	}
	type wire struct {
		Ts int64  `json:"t"`
		ID string `json:"i"`
	}
	b, _ := json.Marshal(wire{Ts: p.CreatedAt.UTC().UnixNano(), ID: p.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses a token produced by EncodeCursor. Empty token returns
// the zero CursorPosition without an error so callers can pass --since-cursor=""
// and get unfiltered output.
func DecodeCursor(token string) (CursorPosition, error) {
	if token == "" {
		return CursorPosition{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return CursorPosition{}, fmt.Errorf("decode cursor: %w", err)
	}
	var w struct {
		Ts int64  `json:"t"`
		ID string `json:"i"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return CursorPosition{}, fmt.Errorf("parse cursor: %w", err)
	}
	return CursorPosition{CreatedAt: time.Unix(0, w.Ts).UTC(), ID: w.ID}, nil
}

// NamedCursor is a persisted iteration position with a stable handle.
type NamedCursor struct {
	Name      string
	Position  CursorPosition
	UpdatedAt time.Time
}

func (s *Store) GetCursor(ctx context.Context, name string) (NamedCursor, bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT name, created_at_nano, last_id, updated_at FROM cursors WHERE name = ?`, name)
	var c NamedCursor
	var nano int64
	var updated string
	if err := row.Scan(&c.Name, &nano, &c.Position.ID, &updated); err != nil {
		if err == sql.ErrNoRows {
			return NamedCursor{}, false, nil
		}
		return NamedCursor{}, false, fmt.Errorf("get cursor: %w", err)
	}
	c.Position.CreatedAt = time.Unix(0, nano).UTC()
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return c, true, nil
}

func (s *Store) UpsertCursor(ctx context.Context, name string, p CursorPosition) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO cursors(name, created_at_nano, last_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET created_at_nano = excluded.created_at_nano, last_id = excluded.last_id, updated_at = excluded.updated_at`,
		name, p.CreatedAt.UTC().UnixNano(), p.ID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert cursor: %w", err)
	}
	return nil
}

func (s *Store) DeleteCursor(ctx context.Context, name string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM cursors WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete cursor: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cursor not found: %s", name)
	}
	return nil
}

func (s *Store) ListCursors(ctx context.Context) ([]NamedCursor, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name, created_at_nano, last_id, updated_at FROM cursors ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list cursors: %w", err)
	}
	defer rows.Close()
	var out []NamedCursor
	for rows.Next() {
		var c NamedCursor
		var nano int64
		var updated string
		if err := rows.Scan(&c.Name, &nano, &c.Position.ID, &updated); err != nil {
			return nil, fmt.Errorf("scan cursor: %w", err)
		}
		c.Position.CreatedAt = time.Unix(0, nano).UTC()
		c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, c)
	}
	return out, rows.Err()
}
