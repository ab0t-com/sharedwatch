package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
	"sharedwatch/internal/mode"
)

type Store struct {
	DB *sql.DB
}

// Open creates the SQLite-backed storage adapter.
// If another backend is added later, keep this implementation behind the
// Adapter interface and select it via config.StorageType.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// WAL gives multi-reader / single-writer concurrency so CLI queries work
	// while `run` is active. busy_timeout(5000) makes lock contention wait
	// rather than fail with SQLITE_BUSY immediately.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply %s: %w", pragma, err)
		}
	}
	store := &Store{DB: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			path TEXT NOT NULL,
			rel_path TEXT NOT NULL,
			old_path TEXT,
			source TEXT NOT NULL,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			observed_at TEXT NOT NULL,
			processed_at TEXT,
			file_size INTEGER NOT NULL DEFAULT 0,
			mtime TEXT,
			content_hash TEXT,
			coalesced_into TEXT,
			payload_json TEXT NOT NULL DEFAULT '{}'
		);`,
		`CREATE INDEX IF NOT EXISTS idx_events_status_created ON events(status, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_events_rel_path_status ON events(rel_path, status, created_at);`,
		`CREATE TABLE IF NOT EXISTS digests (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			window_start TEXT NOT NULL,
			window_end TEXT NOT NULL,
			mode TEXT NOT NULL,
			event_count INTEGER NOT NULL,
			summary_text TEXT NOT NULL,
			status TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_digests_status_created ON digests(status, created_at);`,
		`CREATE TABLE IF NOT EXISTS runtime_state (
			key TEXT PRIMARY KEY,
			value_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS snapshots (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			source TEXT NOT NULL,
			snapshot_json TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS cursors (
			name TEXT PRIMARY KEY,
			created_at_nano INTEGER NOT NULL,
			last_id TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS actors (
			actor_id TEXT PRIMARY KEY,
			label TEXT NOT NULL DEFAULT '',
			actor_kind TEXT NOT NULL DEFAULT '',
			focus TEXT NOT NULL DEFAULT '',
			last_heartbeat TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}'
		);`,
		`CREATE INDEX IF NOT EXISTS idx_actors_last_heartbeat ON actors(last_heartbeat);`,
		`CREATE TABLE IF NOT EXISTS intents (
			intent_id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL,
			path_glob TEXT NOT NULL,
			declared_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			task TEXT NOT NULL DEFAULT '',
			intent TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}'
		);`,
		`CREATE INDEX IF NOT EXISTS idx_intents_actor ON intents(actor_id, expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_intents_expires ON intents(expires_at);`,
		`CREATE TABLE IF NOT EXISTS leases (
			lease_id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL,
			path_glob TEXT NOT NULL,
			granted_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			renewal_count INTEGER NOT NULL DEFAULT 0,
			exclusive INTEGER NOT NULL DEFAULT 0,
			metadata_json TEXT NOT NULL DEFAULT '{}'
		);`,
		`CREATE INDEX IF NOT EXISTS idx_leases_actor ON leases(actor_id, expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_leases_expires ON leases(expires_at);`,
	}
	for _, stmt := range stmts {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Idempotent column additions for older DBs.
	if err := s.addColumnIfMissing(ctx, "events", "producer_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_events_producer ON events(producer_id, created_at)`); err != nil {
		return fmt.Errorf("migrate index: %w", err)
	}
	// SW-AGENT-3: multi-folder support. watch_root carries the root label
	// (NOT the path) so agent queries can reference stable handles. Legacy
	// rows have watch_root = '' (the single-root sentinel) — this matches
	// "no --root filter set" semantics in EventFilter.
	if err := s.addColumnIfMissing(ctx, "events", "watch_root", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing(ctx, "digests", "watch_root", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing(ctx, "snapshots", "watch_root", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_events_root_created ON events(watch_root, created_at)`); err != nil {
		return fmt.Errorf("migrate idx_events_root_created: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_events_root_relpath ON events(watch_root, rel_path, status, created_at)`); err != nil {
		return fmt.Errorf("migrate idx_events_root_relpath: %w", err)
	}
	return nil
}

func (s *Store) addColumnIfMissing(ctx context.Context, table, column, ddl string) error {
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			deflt   any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &deflt, &pk); err != nil {
			return fmt.Errorf("scan column: %w", err)
		}
		if name == column {
			return nil
		}
	}
	if _, err := s.DB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s %s", table, column, ddl)); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}

func (s *Store) InsertEvent(ctx context.Context, e events.Event) error {
	if e.Status == "" {
		e.Status = events.StatusPending
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO events (
		id, type, path, rel_path, old_path, source, status, retry_count,
		created_at, observed_at, file_size, mtime, content_hash, coalesced_into, payload_json, producer_id, watch_root
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Type, e.Path, e.RelPath, strPtr(e.OldPath), e.Source, e.Status, e.RetryCount,
		e.Timestamp.UTC().Format(time.RFC3339Nano), e.Timestamp.UTC().Format(time.RFC3339Nano), e.Size,
		nullableTime(e.MTime), nullableString(e.Hash), strPtr(e.CoalescedInto), emptyJSON(e.PayloadJSON), e.ProducerID,
		e.WatchRoot,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// FindRecentPendingByRelPath returns the most recent pending event on relPath
// regardless of actor or watch_root. Retained for callers that don't care
// about attribution OR multi-root scoping. New coalesce paths should use
// FindRecentPendingByRelPathAndActor instead so cross-actor and cross-root
// events are kept distinct (see SW-AGENT-11 + SW-AGENT-3).
func (s *Store) FindRecentPendingByRelPath(ctx context.Context, relPath string, since time.Time) (events.Event, bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, type, path, rel_path, old_path, source, status, retry_count, observed_at, file_size, mtime, content_hash, coalesced_into, payload_json, producer_id, watch_root
		FROM events WHERE rel_path = ? AND status = 'pending' AND observed_at >= ? ORDER BY observed_at DESC LIMIT 1`, relPath, since.UTC().Format(time.RFC3339Nano))
	e, err := scanEvent(row)
	if err == sql.ErrNoRows {
		return events.Event{}, false, nil
	}
	if err != nil {
		return events.Event{}, false, fmt.Errorf("find recent pending: %w", err)
	}
	return e, true, nil
}

// FindRecentPendingByRelPathAndActor returns the most recent pending event on
// relPath that matches BOTH the supplied actor AND watch_root scope.
// SW-AGENT-3 + SW-AGENT-11 together: coalesce is keyed on
// (rel_path, watch_root, actor) — two README.md files in different roots stay
// distinct; same-path different-actor events stay distinct; same-path
// same-actor same-root events inside the window coalesce.
//
// Empty actor matches events whose payload lacks an actor key (legacy
// single-tenant). Empty watchRoot matches legacy single-root events.
//
// Implementation: we fetch a small bounded set of recent candidates filtered
// on (rel_path, watch_root) in SQL and then filter actor in Go via
// events.ExtractActor. This avoids depending on SQLite's JSON1 extension on
// the hot insertion path.
func (s *Store) FindRecentPendingByRelPathAndActor(ctx context.Context, relPath, actor, watchRoot string, since time.Time) (events.Event, bool, error) {
	const candidateLimit = 16
	rows, err := s.DB.QueryContext(ctx, `SELECT id, type, path, rel_path, old_path, source, status, retry_count, observed_at, file_size, mtime, content_hash, coalesced_into, payload_json, producer_id, watch_root
		FROM events WHERE rel_path = ? AND watch_root = ? AND status = 'pending' AND observed_at >= ? ORDER BY observed_at DESC LIMIT ?`, relPath, watchRoot, since.UTC().Format(time.RFC3339Nano), candidateLimit)
	if err != nil {
		return events.Event{}, false, fmt.Errorf("find recent pending (actor): %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return events.Event{}, false, fmt.Errorf("scan candidate: %w", err)
		}
		if events.ExtractActor(e.PayloadJSON) == actor {
			return e, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return events.Event{}, false, fmt.Errorf("iterate candidates: %w", err)
	}
	return events.Event{}, false, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(row rowScanner) (events.Event, error) {
	var e events.Event
	var oldPath, mtime, coalescedInto, hash sql.NullString
	var observedAt string
	if err := row.Scan(&e.ID, &e.Type, &e.Path, &e.RelPath, &oldPath, &e.Source, &e.Status, &e.RetryCount, &observedAt, &e.Size, &mtime, &hash, &coalescedInto, &e.PayloadJSON, &e.ProducerID, &e.WatchRoot); err != nil {
		return events.Event{}, err
	}
	if oldPath.Valid && oldPath.String != "" {
		v := oldPath.String
		e.OldPath = &v
	}
	if hash.Valid {
		e.Hash = hash.String
	}
	e.Timestamp, _ = time.Parse(time.RFC3339Nano, observedAt)
	if mtime.Valid && mtime.String != "" {
		e.MTime, _ = time.Parse(time.RFC3339Nano, mtime.String)
	}
	if coalescedInto.Valid && coalescedInto.String != "" {
		v := coalescedInto.String
		e.CoalescedInto = &v
	}
	return e, nil
}

func (s *Store) UpdateEvent(ctx context.Context, e events.Event) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE events SET type=?, path=?, old_path=?, source=?, status=?, retry_count=?, observed_at=?, file_size=?, mtime=?, content_hash=?, coalesced_into=?, payload_json=?, producer_id=?, watch_root=? WHERE id=?`,
		e.Type, e.Path, strPtr(e.OldPath), e.Source, e.Status, e.RetryCount, e.Timestamp.UTC().Format(time.RFC3339Nano), e.Size, nullableTime(e.MTime), nullableString(e.Hash), strPtr(e.CoalescedInto), emptyJSON(e.PayloadJSON), e.ProducerID, e.WatchRoot, e.ID)
	if err != nil {
		return fmt.Errorf("update event: %w", err)
	}
	return nil
}

func (s *Store) InsertOrCoalesceEvent(ctx context.Context, e events.Event, window time.Duration) error {
	// Actor- and root-aware coalesce: only merge with a prior pending event on
	// the same path inside the same watch_root with the same actor. Two
	// agents editing the same file inside the window remain distinct
	// (SW-AGENT-11); same-relpath events across roots remain distinct
	// (SW-AGENT-3). Empty actor and empty watch_root preserve legacy
	// single-tenant single-root behaviour.
	actor := events.ExtractActor(e.PayloadJSON)
	existing, ok, err := s.FindRecentPendingByRelPathAndActor(ctx, e.RelPath, actor, e.WatchRoot, e.Timestamp.Add(-window))
	if err != nil {
		return err
	}
	if ok && events.ShouldCoalesce(existing, e, window) {
		merged := events.Coalesce(existing, e)
		return s.UpdateEvent(ctx, merged)
	}
	return s.InsertEvent(ctx, e)
}

func (s *Store) ClaimPendingEvents(ctx context.Context, limit int) ([]events.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id, type, path, rel_path, old_path, source, status, retry_count, observed_at, file_size, mtime, content_hash, coalesced_into, payload_json, producer_id, watch_root FROM events WHERE status = 'pending' ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending: %w", err)
	}
	defer rows.Close()
	var out []events.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan pending: %w", err)
		}
		out = append(out, e)
	}
	for i, e := range out {
		if _, err := tx.ExecContext(ctx, `UPDATE events SET status='processing' WHERE id=?`, e.ID); err != nil {
			return nil, fmt.Errorf("mark processing: %w", err)
		}
		out[i].Status = events.StatusProcessing
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return out, nil
}

func (s *Store) MarkEventsProcessed(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, err := s.DB.ExecContext(ctx, `UPDATE events SET status='processed', processed_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
			return fmt.Errorf("mark processed: %w", err)
		}
	}
	return nil
}

func (s *Store) MarkEventsFailed(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, err := s.DB.ExecContext(ctx, `UPDATE events SET status='failed', retry_count=retry_count+1 WHERE id=?`, id); err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
	}
	return nil
}

// RequeueFailedEvents moves all events currently in status='failed' back to
// 'pending' so the next consume can pick them up. Returns the count actually
// requeued so the CLI can tell the user whether there was anything to do.
func (s *Store) RequeueFailedEvents(ctx context.Context) (int64, error) {
	return s.RequeueFailedEventsWithLimit(ctx, 0)
}

// RequeueFailedEventsWithLimit is like RequeueFailedEvents but skips events
// whose retry_count has reached maxRetries. maxRetries <= 0 = no cap.
func (s *Store) RequeueFailedEventsWithLimit(ctx context.Context, maxRetries int) (int64, error) {
	var res sqlResult
	var err error
	if maxRetries <= 0 {
		r, e := s.DB.ExecContext(ctx, `UPDATE events SET status='pending' WHERE status='failed'`)
		res, err = r, e
	} else {
		r, e := s.DB.ExecContext(ctx, `UPDATE events SET status='pending' WHERE status='failed' AND retry_count < ?`, maxRetries)
		res, err = r, e
	}
	if err != nil {
		return 0, fmt.Errorf("requeue failed events: %w", err)
	}
	return res.RowsAffected()
}

// RecoverStuckProcessing finds events stuck in status='processing' older than
// olderThan and flips them back to 'pending' so a fresh consume can pick them
// up. This catches the case where a consumer crashed between
// ClaimPendingEvents and MarkEventsProcessed.
func (s *Store) RecoverStuckProcessing(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	res, err := s.DB.ExecContext(ctx, `UPDATE events SET status='pending' WHERE status='processing' AND observed_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("recover stuck: %w", err)
	}
	return res.RowsAffected()
}

// sqlResult is the subset of sql.Result we use (for swapping branches in
// RequeueFailedEventsWithLimit without converting types).
type sqlResult interface {
	RowsAffected() (int64, error)
}

func (s *Store) InsertDigest(ctx context.Context, d digest.Digest) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO digests (
		id, created_at, window_start, window_end, mode, event_count, summary_text, status, watch_root
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID,
		d.CreatedAt.UTC().Format(time.RFC3339Nano),
		d.WindowStart.UTC().Format(time.RFC3339Nano),
		d.WindowEnd.UTC().Format(time.RFC3339Nano),
		d.Mode,
		d.EventCount,
		d.Summary,
		d.Status,
		d.WatchRoot,
	)
	if err != nil {
		return fmt.Errorf("insert digest: %w", err)
	}
	return nil
}

func (s *Store) ListDigests(ctx context.Context, limit int) ([]digest.Digest, error) {
	return s.ListDigestsFiltered(ctx, limit, "")
}

// ListDigestsFiltered returns the newest `limit` digests, optionally filtered
// to a single status ("" means no filter). Use ListDigestsByFilter for the
// richer filter shape (status + watch_root); kept for the existing single-
// status callers.
func (s *Store) ListDigestsFiltered(ctx context.Context, limit int, status string) ([]digest.Digest, error) {
	return s.ListDigestsByFilter(ctx, DigestFilter{Limit: limit, Status: status})
}

// DigestFilter is the parameter shape for ListDigestsByFilter. All zero-valued
// fields are treated as "no constraint" (mirrors EventFilter).
type DigestFilter struct {
	Limit      int      // 0 or negative → default 20
	Status     string   // empty = any
	WatchRoots []string // empty = any
}

func (s *Store) ListDigestsByFilter(ctx context.Context, f DigestFilter) ([]digest.Digest, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	var clauses []string
	var args []any
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if len(f.WatchRoots) > 0 {
		ph, vals := placeholders(f.WatchRoots)
		clauses = append(clauses, "watch_root IN ("+ph+")")
		args = append(args, vals...)
	}
	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	q := fmt.Sprintf(`SELECT id, created_at, window_start, window_end, mode, event_count, summary_text, status, watch_root FROM digests %s ORDER BY created_at DESC LIMIT ?`, where)
	args = append(args, f.Limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list digests: %w", err)
	}
	defer rows.Close()
	var out []digest.Digest
	for rows.Next() {
		var d digest.Digest
		var createdAt, start, end, stat string
		if err := rows.Scan(&d.ID, &createdAt, &start, &end, &d.Mode, &d.EventCount, &d.Summary, &stat, &d.WatchRoot); err != nil {
			return nil, fmt.Errorf("scan digest: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		d.WindowStart, _ = time.Parse(time.RFC3339Nano, start)
		d.WindowEnd, _ = time.Parse(time.RFC3339Nano, end)
		d.Status = digest.Status(stat)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ErrDigestNotFound is returned by GetDigest/MarkDigest* when no row matches
// the given id, so callers can render a clean user-facing error instead of a
// raw SQL message.
var ErrDigestNotFound = fmt.Errorf("digest not found")

func (s *Store) GetDigest(ctx context.Context, id string) (digest.Digest, error) {
	var d digest.Digest
	var createdAt, start, end, status string
	err := s.DB.QueryRowContext(ctx, `SELECT id, created_at, window_start, window_end, mode, event_count, summary_text, status, watch_root FROM digests WHERE id = ?`, id).Scan(&d.ID, &createdAt, &start, &end, &d.Mode, &d.EventCount, &d.Summary, &status, &d.WatchRoot)
	if err == sql.ErrNoRows {
		return digest.Digest{}, fmt.Errorf("%w: %s", ErrDigestNotFound, id)
	}
	if err != nil {
		return digest.Digest{}, fmt.Errorf("get digest: %w", err)
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	d.WindowStart, _ = time.Parse(time.RFC3339Nano, start)
	d.WindowEnd, _ = time.Parse(time.RFC3339Nano, end)
	d.Status = digest.Status(status)
	return d, nil
}

// UpsertRuntimeJSON writes an arbitrary string value under a non-runtime key
// in runtime_state. Used by App.saveCachedOverview to cache the L1 envelope.
// Distinct from UpsertRuntime, which targets the well-known 'runtime' row.
func (s *Store) UpsertRuntimeJSON(ctx context.Context, key, valueJSON string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO runtime_state(key, value_json, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
		key, valueJSON, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert runtime json (%s): %w", key, err)
	}
	return nil
}

// GetRuntimeJSON reads back a value stored via UpsertRuntimeJSON. Returns
// ok=false when the key isn't present.
func (s *Store) GetRuntimeJSON(ctx context.Context, key string) (string, bool, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT value_json FROM runtime_state WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get runtime json (%s): %w", key, err)
	}
	return raw, true, nil
}

func (s *Store) UpsertRuntime(ctx context.Context, r mode.Runtime) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal runtime: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO runtime_state(key, value_json, updated_at) VALUES ('runtime', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`, string(b), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert runtime: %w", err)
	}
	return nil
}

func (s *Store) GetRuntime(ctx context.Context) (mode.Runtime, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT value_json FROM runtime_state WHERE key = 'runtime'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return mode.DefaultRuntime(), nil
	}
	if err != nil {
		return mode.Runtime{}, fmt.Errorf("get runtime: %w", err)
	}
	var r mode.Runtime
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return mode.Runtime{}, fmt.Errorf("unmarshal runtime: %w", err)
	}
	return r, nil
}

// SaveSnapshot persists a per-(source, watch_root) snapshot. watch_root="" is
// the legacy single-root sentinel and continues to work; multi-root callers
// pass the root's Label.
func (s *Store) SaveSnapshot(ctx context.Context, id string, source string, watchRoot string, snap catalog.Snapshot) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO snapshots(id, created_at, source, watch_root, snapshot_json) VALUES (?, ?, ?, ?, ?)`, id, time.Now().UTC().Format(time.RFC3339Nano), source, watchRoot, string(b))
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}

// LatestSnapshot returns the most recent snapshot for the (source, watch_root)
// pair. Critical correctness: SW-AGENT-3 requires per-root snapshot lookup so
// a populated root doesn't get diffed against another root's snapshot (which
// would produce a phantom whole-tree create/delete cascade).
func (s *Store) LatestSnapshot(ctx context.Context, source string, watchRoot string) (catalog.Snapshot, bool, error) {
	var raw string
	row := s.DB.QueryRowContext(ctx, `SELECT snapshot_json FROM snapshots WHERE source = ? AND watch_root = ? ORDER BY created_at DESC LIMIT 1`, source, watchRoot)
	if err := row.Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return catalog.Snapshot{}, false, nil
		}
		return catalog.Snapshot{}, false, fmt.Errorf("latest snapshot: %w", err)
	}
	var snap catalog.Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return catalog.Snapshot{}, false, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return snap, true, nil
}

func (s *Store) PendingCount(ctx context.Context) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE status = 'pending'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("pending count: %w", err)
	}
	return n, nil
}

// PendingCountByRoot returns the count of pending events scoped to a single
// watch_root label. Used by status --json to produce per-root counters.
func (s *Store) PendingCountByRoot(ctx context.Context, watchRoot string) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE status = 'pending' AND watch_root = ?`, watchRoot).Scan(&n); err != nil {
		return 0, fmt.Errorf("pending count (root=%q): %w", watchRoot, err)
	}
	return n, nil
}

// LastEventAtByRoot returns the most-recent observed_at timestamp for any
// event in the given watch_root. Returns zero time when no events exist.
func (s *Store) LastEventAtByRoot(ctx context.Context, watchRoot string) (time.Time, error) {
	var ts sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT MAX(observed_at) FROM events WHERE watch_root = ?`, watchRoot).Scan(&ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("last_event_at (root=%q): %w", watchRoot, err)
	}
	if !ts.Valid || ts.String == "" {
		return time.Time{}, nil
	}
	t, _ := time.Parse(time.RFC3339Nano, ts.String)
	return t, nil
}

func (s *Store) FailedCount(ctx context.Context) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE status = 'failed'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed count: %w", err)
	}
	return n, nil
}

func (s *Store) DigestCount(ctx context.Context) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM digests`).Scan(&n); err != nil {
		return 0, fmt.Errorf("digest count: %w", err)
	}
	return n, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func strPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func emptyJSON(v string) string {
	if v == "" {
		return "{}"
	}
	return v
}
