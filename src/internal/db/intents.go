package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// IntentRecord is the persisted form of an `intent declare` row. Intents are
// forward-looking advisory signals — "I plan to edit X within the next N
// minutes." Peers consult them via `intent list` before claiming overlapping
// work. Not enforcement; cooperation only.
type IntentRecord struct {
	IntentID     string    `json:"intent_id"`
	ActorID      string    `json:"actor_id"`
	PathGlob     string    `json:"path_glob"`
	DeclaredAt   time.Time `json:"declared_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Task         string    `json:"task,omitempty"`
	Intent       string    `json:"intent,omitempty"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
}

// IntentFilter parameterises ListIntents. Empty fields = no constraint.
// IncludeExpired controls whether already-expired (but not yet pruned) rows
// are returned.
type IntentFilter struct {
	ActorID        string
	PathGlob       string
	IncludeExpired bool
}

func (s *Store) InsertIntent(ctx context.Context, r IntentRecord) error {
	if r.IntentID == "" || r.ActorID == "" || r.PathGlob == "" {
		return fmt.Errorf("insert intent: intent_id, actor_id, path_glob are required")
	}
	if r.DeclaredAt.IsZero() {
		r.DeclaredAt = time.Now().UTC()
	}
	if r.ExpiresAt.IsZero() {
		return fmt.Errorf("insert intent: expires_at is required (set a TTL)")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO intents (
		intent_id, actor_id, path_glob, declared_at, expires_at, task, intent, metadata_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.IntentID, r.ActorID, r.PathGlob,
		r.DeclaredAt.UTC().Format(time.RFC3339Nano),
		r.ExpiresAt.UTC().Format(time.RFC3339Nano),
		r.Task, r.Intent, emptyJSON(r.MetadataJSON))
	if err != nil {
		return fmt.Errorf("insert intent: %w", err)
	}
	return nil
}

func (s *Store) ListIntents(ctx context.Context, f IntentFilter) ([]IntentRecord, error) {
	var clauses []string
	var args []any
	if f.ActorID != "" {
		clauses = append(clauses, "actor_id = ?")
		args = append(args, f.ActorID)
	}
	if f.PathGlob != "" {
		clauses = append(clauses, "path_glob = ?")
		args = append(args, f.PathGlob)
	}
	if !f.IncludeExpired {
		clauses = append(clauses, "expires_at > ?")
		args = append(args, time.Now().UTC().Format(time.RFC3339Nano))
	}
	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + joinAnd(clauses)
	}
	q := fmt.Sprintf(`SELECT intent_id, actor_id, path_glob, declared_at, expires_at, task, intent, metadata_json FROM intents %s ORDER BY declared_at DESC`, where)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list intents: %w", err)
	}
	defer rows.Close()
	var out []IntentRecord
	for rows.Next() {
		var (
			r                     IntentRecord
			declaredAt, expiresAt string
			task, intent, meta    sql.NullString
		)
		if err := rows.Scan(&r.IntentID, &r.ActorID, &r.PathGlob, &declaredAt, &expiresAt, &task, &intent, &meta); err != nil {
			return nil, fmt.Errorf("scan intent: %w", err)
		}
		r.DeclaredAt, _ = time.Parse(time.RFC3339Nano, declaredAt)
		r.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
		r.Task = task.String
		r.Intent = intent.String
		r.MetadataJSON = meta.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RevokeIntent(ctx context.Context, intentID string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM intents WHERE intent_id = ?`, intentID)
	if err != nil {
		return fmt.Errorf("revoke intent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("intent not found: %s", intentID)
	}
	return nil
}

func (s *Store) PruneExpiredIntents(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM intents WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune expired intents: %w", err)
	}
	return res.RowsAffected()
}

func joinAnd(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " AND "
		}
		out += p
	}
	return out
}
