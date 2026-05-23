package db

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"sharedwatch/internal/events"
)

// EventFilter parameterizes Store.QueryEvents. All zero-valued fields are
// treated as "no constraint" so callers can construct a minimal filter and
// add only the dimensions they care about.
type EventFilter struct {
	Since        time.Time // inclusive lower bound on created_at (zero = no bound)
	Until        time.Time // exclusive upper bound on created_at (zero = no bound)
	Types        []string  // OR-filter on type column
	Sources      []string  // OR-filter on source column
	Statuses     []string  // OR-filter on status column
	ProducerIDs  []string  // OR-filter on producer_id column
	WatchRoots   []string  // OR-filter on watch_root column (SW-AGENT-3)
	PathGlob     string    // filepath.Match pattern applied to rel_path (post-filter)
	PayloadKey   string    // if set, post-filter: parse payload_json as object, require [key] == value
	PayloadValue string    // see PayloadKey
	Limit        int       // 0 or negative = no limit
	OrderAsc     bool      // default false (newest first)
	AfterNano    int64     // cursor tie-break
	AfterID      string    // cursor tie-break
}

// QueryEvents returns events matching the filter. It never modifies status.
// When the filter includes a cursor (AfterNano > 0), results are ordered ASC
// regardless of OrderAsc so the cursor can advance monotonically.
func (s *Store) QueryEvents(ctx context.Context, f EventFilter) ([]events.Event, error) {
	var (
		clauses []string
		args    []any
	)
	if !f.Since.IsZero() {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	if !f.Until.IsZero() {
		clauses = append(clauses, "created_at < ?")
		args = append(args, f.Until.UTC().Format(time.RFC3339Nano))
	}
	if len(f.Types) > 0 {
		ph, vals := placeholders(f.Types)
		clauses = append(clauses, "type IN ("+ph+")")
		args = append(args, vals...)
	}
	if len(f.Sources) > 0 {
		ph, vals := placeholders(f.Sources)
		clauses = append(clauses, "source IN ("+ph+")")
		args = append(args, vals...)
	}
	if len(f.Statuses) > 0 {
		ph, vals := placeholders(f.Statuses)
		clauses = append(clauses, "status IN ("+ph+")")
		args = append(args, vals...)
	}
	if len(f.ProducerIDs) > 0 {
		ph, vals := placeholders(f.ProducerIDs)
		clauses = append(clauses, "producer_id IN ("+ph+")")
		args = append(args, vals...)
	}
	if len(f.WatchRoots) > 0 {
		ph, vals := placeholders(f.WatchRoots)
		clauses = append(clauses, "watch_root IN ("+ph+")")
		args = append(args, vals...)
	}

	cursorActive := f.AfterNano > 0 || f.AfterID != ""
	if cursorActive {
		// (created_at_nano > a) OR (created_at_nano == a AND id > b)
		// We don't have a nano column, so compare against the RFC3339Nano string
		// form which sorts the same way for UTC.
		afterStr := time.Unix(0, f.AfterNano).UTC().Format(time.RFC3339Nano)
		clauses = append(clauses, "(created_at > ? OR (created_at = ? AND id > ?))")
		args = append(args, afterStr, afterStr, f.AfterID)
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	order := "DESC"
	if f.OrderAsc || cursorActive {
		order = "ASC"
	}
	limitClause := ""
	if f.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT %d", f.Limit)
	}

	q := fmt.Sprintf(`SELECT id, type, path, rel_path, old_path, source, status, retry_count, observed_at, file_size, mtime, content_hash, coalesced_into, payload_json, producer_id, watch_root
		FROM events %s ORDER BY created_at %s, id %s %s`, where, order, order, limitClause)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var out []events.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if f.PathGlob != "" {
			match, _ := filepath.Match(f.PathGlob, e.RelPath)
			if !match && !globDeep(f.PathGlob, e.RelPath) {
				continue
			}
		}
		if f.PayloadKey != "" {
			var obj map[string]any
			if e.PayloadJSON == "" {
				continue
			}
			if err := json.Unmarshal([]byte(e.PayloadJSON), &obj); err != nil {
				continue
			}
			v, ok := obj[f.PayloadKey]
			if !ok || fmt.Sprintf("%v", v) != f.PayloadValue {
				continue
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func placeholders(vals []string) (string, []any) {
	parts := make([]string, len(vals))
	out := make([]any, len(vals))
	for i, v := range vals {
		parts[i] = "?"
		out[i] = v
	}
	return strings.Join(parts, ","), out
}

// globDeep adds `**` semantics on top of filepath.Match: `auth/**` matches
// `auth/a.md` and `auth/sub/dir/file.md`. Other patterns fall back to
// filepath.Match.
func globDeep(pattern, path string) bool {
	if strings.Contains(pattern, "**") {
		// Replace ** with a path that matches any depth, then check prefix.
		idx := strings.Index(pattern, "**")
		prefix := pattern[:idx]
		suffix := pattern[idx+2:]
		if !strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")) && prefix != "" {
			return false
		}
		if suffix == "" {
			return true
		}
		// Match suffix against any tail of the path.
		segs := strings.Split(path, "/")
		for i := range segs {
			tail := strings.Join(segs[i:], "/")
			if ok, _ := filepath.Match(strings.TrimPrefix(suffix, "/"), tail); ok {
				return true
			}
		}
		return false
	}
	ok, _ := filepath.Match(pattern, path)
	return ok
}
