package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// LeaseRecord is the persisted form of a `lease grant` row. Leases are
// stronger advisories than intents — "I am editing X right now; please
// don't clobber." The TTL is enforced (max 1h); auto-release expires stale
// rows. Like intents, leases are cooperative — not OS-level locking.
type LeaseRecord struct {
	LeaseID      string    `json:"lease_id"`
	ActorID      string    `json:"actor_id"`
	PathGlob     string    `json:"path_glob"`
	GrantedAt    time.Time `json:"granted_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	RenewalCount int       `json:"renewal_count"`
	Exclusive    bool      `json:"exclusive"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
}

// LeaseTTLMax caps individual lease lifetimes. Prevents the squatting failure
// mode noted in the design doc (see Skills/sharedwatch-client-future/references/leases.md).
const LeaseTTLMax = time.Hour

type LeaseFilter struct {
	ActorID        string
	PathGlob       string
	IncludeExpired bool
}

func (s *Store) InsertLease(ctx context.Context, r LeaseRecord) error {
	if r.LeaseID == "" || r.ActorID == "" || r.PathGlob == "" {
		return fmt.Errorf("insert lease: lease_id, actor_id, path_glob are required")
	}
	if r.GrantedAt.IsZero() {
		r.GrantedAt = time.Now().UTC()
	}
	if r.ExpiresAt.IsZero() {
		return fmt.Errorf("insert lease: expires_at is required (set a TTL)")
	}
	if r.ExpiresAt.Sub(r.GrantedAt) > LeaseTTLMax {
		return fmt.Errorf("insert lease: TTL exceeds max (%s)", LeaseTTLMax)
	}
	excl := 0
	if r.Exclusive {
		excl = 1
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO leases (
		lease_id, actor_id, path_glob, granted_at, expires_at, renewal_count, exclusive, metadata_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.LeaseID, r.ActorID, r.PathGlob,
		r.GrantedAt.UTC().Format(time.RFC3339Nano),
		r.ExpiresAt.UTC().Format(time.RFC3339Nano),
		r.RenewalCount, excl, emptyJSON(r.MetadataJSON))
	if err != nil {
		return fmt.Errorf("insert lease: %w", err)
	}
	return nil
}

func (s *Store) ListLeases(ctx context.Context, f LeaseFilter) ([]LeaseRecord, error) {
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
	q := fmt.Sprintf(`SELECT lease_id, actor_id, path_glob, granted_at, expires_at, renewal_count, exclusive, metadata_json FROM leases %s ORDER BY granted_at DESC`, where)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list leases: %w", err)
	}
	defer rows.Close()
	var out []LeaseRecord
	for rows.Next() {
		var (
			r                    LeaseRecord
			grantedAt, expiresAt string
			excl                 int
			meta                 sql.NullString
		)
		if err := rows.Scan(&r.LeaseID, &r.ActorID, &r.PathGlob, &grantedAt, &expiresAt, &r.RenewalCount, &excl, &meta); err != nil {
			return nil, fmt.Errorf("scan lease: %w", err)
		}
		r.GrantedAt, _ = time.Parse(time.RFC3339Nano, grantedAt)
		r.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
		r.Exclusive = excl != 0
		r.MetadataJSON = meta.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ReleaseLease(ctx context.Context, leaseID string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM leases WHERE lease_id = ?`, leaseID)
	if err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("lease not found: %s", leaseID)
	}
	return nil
}

// RenewLease bumps renewal_count and extends expires_at by ttl. The renewed
// lease still cannot exceed LeaseTTLMax relative to its original GrantedAt
// (squat protection). Returns the updated record.
func (s *Store) RenewLease(ctx context.Context, leaseID string, ttl time.Duration) (LeaseRecord, error) {
	if ttl <= 0 {
		return LeaseRecord{}, fmt.Errorf("renew lease: ttl must be positive")
	}
	leases, err := s.ListLeases(ctx, LeaseFilter{IncludeExpired: true})
	if err != nil {
		return LeaseRecord{}, err
	}
	var target *LeaseRecord
	for i := range leases {
		if leases[i].LeaseID == leaseID {
			target = &leases[i]
			break
		}
	}
	if target == nil {
		return LeaseRecord{}, fmt.Errorf("lease not found: %s", leaseID)
	}
	newExpires := time.Now().UTC().Add(ttl)
	if newExpires.Sub(target.GrantedAt) > LeaseTTLMax {
		newExpires = target.GrantedAt.Add(LeaseTTLMax)
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE leases SET expires_at = ?, renewal_count = renewal_count + 1 WHERE lease_id = ?`,
		newExpires.UTC().Format(time.RFC3339Nano), leaseID)
	if err != nil {
		return LeaseRecord{}, fmt.Errorf("renew lease: %w", err)
	}
	target.ExpiresAt = newExpires
	target.RenewalCount++
	return *target, nil
}

func (s *Store) PruneExpiredLeases(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM leases WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune expired leases: %w", err)
	}
	return res.RowsAffected()
}

// LeaseGlobMatchesPath reports whether the lease's path_glob covers the given
// rel_path. Supports `*` (any non-slash run) and `**` (any depth) — matches
// the path-glob semantics used by EventFilter.PathGlob so an agent's "I'm
// editing auth/**" claim covers `auth/login.go` AND `auth/sub/oauth.go`.
//
// Used by the watcher's advisory warning (SW-AGENT-12, S7.9): when an event
// arrives on a path covered by an active lease whose actor differs from the
// event's actor, the watcher logs a warn line.
func LeaseGlobMatchesPath(glob, path string) bool {
	if glob == "" || path == "" {
		return false
	}
	if glob == path {
		return true
	}
	if strings.Contains(glob, "**") {
		idx := strings.Index(glob, "**")
		prefix := strings.TrimSuffix(glob[:idx], "/")
		suffix := strings.TrimPrefix(glob[idx+2:], "/")
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			return false
		}
		if suffix == "" {
			return true
		}
		segs := strings.Split(path, "/")
		for i := range segs {
			tail := strings.Join(segs[i:], "/")
			if ok, _ := filepath.Match(suffix, tail); ok {
				return true
			}
		}
		return false
	}
	ok, _ := filepath.Match(glob, path)
	return ok
}
