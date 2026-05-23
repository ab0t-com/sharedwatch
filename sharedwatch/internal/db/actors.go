package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// ActorRecord is the persisted form of one actors-registry row. Mirrors the
// schema written by Store.migrate.
type ActorRecord struct {
	ActorID       string    `json:"actor_id"`
	Label         string    `json:"label,omitempty"`
	ActorKind     string    `json:"actor_kind,omitempty"`
	Focus         string    `json:"focus,omitempty"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	MetadataJSON  string    `json:"metadata_json,omitempty"`
}

// UpsertActorHeartbeat writes (or refreshes) a heartbeat for the given actor.
// last_heartbeat is set to now(UTC). Non-empty Label/ActorKind/Focus/Metadata
// fields overwrite the prior value; empty fields preserve the prior value, so
// a thin heartbeat ("just refresh my timestamp") doesn't have to repeat all
// metadata.
//
// Logs a warn-level message when actor_kind changes between heartbeats — the
// most common identity-confusion smell (e.g., two unrelated processes reusing
// the same actor_id from different roles).
func (s *Store) UpsertActorHeartbeat(ctx context.Context, r ActorRecord) error {
	if r.ActorID == "" {
		return fmt.Errorf("upsert actor: actor_id is required")
	}
	prev, ok, err := s.GetActor(ctx, r.ActorID)
	if err != nil {
		return err
	}
	if ok && prev.ActorKind != "" && r.ActorKind != "" && prev.ActorKind != r.ActorKind {
		slog.Warn("actor_kind changed between heartbeats; possible actor_id collision",
			"actor_id", r.ActorID, "prev_kind", prev.ActorKind, "new_kind", r.ActorKind)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Use COALESCE on optional columns so a thin heartbeat keeps prior metadata
	// instead of clobbering it. last_heartbeat always advances to now.
	_, err = s.DB.ExecContext(ctx, `INSERT INTO actors (actor_id, label, actor_kind, focus, last_heartbeat, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(actor_id) DO UPDATE SET
			label          = CASE WHEN excluded.label          = '' THEN actors.label          ELSE excluded.label          END,
			actor_kind     = CASE WHEN excluded.actor_kind     = '' THEN actors.actor_kind     ELSE excluded.actor_kind     END,
			focus          = CASE WHEN excluded.focus          = '' THEN actors.focus          ELSE excluded.focus          END,
			metadata_json  = CASE WHEN excluded.metadata_json  = '{}' OR excluded.metadata_json = '' THEN actors.metadata_json ELSE excluded.metadata_json END,
			last_heartbeat = excluded.last_heartbeat`,
		r.ActorID, r.Label, r.ActorKind, r.Focus, now, emptyJSON(r.MetadataJSON))
	if err != nil {
		return fmt.Errorf("upsert actor heartbeat: %w", err)
	}
	return nil
}

// GetActor returns one actor row by id. Returns ok=false when not found.
func (s *Store) GetActor(ctx context.Context, actorID string) (ActorRecord, bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT actor_id, label, actor_kind, focus, last_heartbeat, metadata_json
		FROM actors WHERE actor_id = ?`, actorID)
	var (
		ar          ActorRecord
		lastHB      string
		label, kind sql.NullString
		focus, meta sql.NullString
	)
	err := row.Scan(&ar.ActorID, &label, &kind, &focus, &lastHB, &meta)
	if err == sql.ErrNoRows {
		return ActorRecord{}, false, nil
	}
	if err != nil {
		return ActorRecord{}, false, fmt.Errorf("get actor: %w", err)
	}
	ar.Label = label.String
	ar.ActorKind = kind.String
	ar.Focus = focus.String
	ar.MetadataJSON = meta.String
	ar.LastHeartbeat, _ = time.Parse(time.RFC3339Nano, lastHB)
	return ar, true, nil
}

// ListActors returns all registered actors, newest heartbeat first. Callers
// derive `stale` from LastHeartbeat against their configured TTL.
func (s *Store) ListActors(ctx context.Context) ([]ActorRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT actor_id, label, actor_kind, focus, last_heartbeat, metadata_json
		FROM actors ORDER BY last_heartbeat DESC`)
	if err != nil {
		return nil, fmt.Errorf("list actors: %w", err)
	}
	defer rows.Close()
	var out []ActorRecord
	for rows.Next() {
		var (
			ar          ActorRecord
			lastHB      string
			label, kind sql.NullString
			focus, meta sql.NullString
		)
		if err := rows.Scan(&ar.ActorID, &label, &kind, &focus, &lastHB, &meta); err != nil {
			return nil, fmt.Errorf("scan actor: %w", err)
		}
		ar.Label = label.String
		ar.ActorKind = kind.String
		ar.Focus = focus.String
		ar.MetadataJSON = meta.String
		ar.LastHeartbeat, _ = time.Parse(time.RFC3339Nano, lastHB)
		out = append(out, ar)
	}
	return out, rows.Err()
}

// PruneStaleActors deletes actor rows whose last_heartbeat is older than the
// supplied threshold. Returns the count removed. Intended to be called by the
// reconciler at the same cadence as the other prune passes.
func (s *Store) PruneStaleActors(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	res, err := s.DB.ExecContext(ctx, `DELETE FROM actors WHERE last_heartbeat < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune stale actors: %w", err)
	}
	return res.RowsAffected()
}
