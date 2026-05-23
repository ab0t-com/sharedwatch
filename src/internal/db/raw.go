package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrWriteSQLDenied is returned by RawSQL when a non-SELECT statement is
// submitted without allowWrite.
var ErrWriteSQLDenied = errors.New("non-SELECT statements require --write")

// RawSQL executes a single SQL statement and returns column names + rows.
// allowWrite=false rejects anything that doesn't start with SELECT/WITH/EXPLAIN
// and refuses multi-statement input (anything with a `;` outside the trailing
// position). This is a tripwire, not a security boundary.
func (s *Store) RawSQL(ctx context.Context, query string, allowWrite bool) ([]string, [][]any, error) {
	if !allowWrite {
		if err := assertReadOnly(query); err != nil {
			return nil, nil, err
		}
	}
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("exec sql: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("read columns: %w", err)
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, vals)
	}
	return cols, out, rows.Err()
}

// stripSQLComments removes -- line comments and /* block */ comments so the
// keyword check looks at actual statement text.
func stripSQLComments(q string) string {
	var b strings.Builder
	i := 0
	for i < len(q) {
		switch {
		case i+1 < len(q) && q[i] == '-' && q[i+1] == '-':
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case i+1 < len(q) && q[i] == '/' && q[i+1] == '*':
			i += 2
			for i+1 < len(q) && !(q[i] == '*' && q[i+1] == '/') {
				i++
			}
			if i+1 < len(q) {
				i += 2
			}
		default:
			b.WriteByte(q[i])
			i++
		}
	}
	return b.String()
}

func assertReadOnly(query string) error {
	stripped := strings.TrimSpace(stripSQLComments(query))
	// Reject multi-statement inputs unless the only trailing thing is whitespace.
	if idx := strings.Index(stripped, ";"); idx >= 0 && strings.TrimSpace(stripped[idx+1:]) != "" {
		return fmt.Errorf("%w: multi-statement input not allowed in read-only mode", ErrWriteSQLDenied)
	}
	upper := strings.ToUpper(stripped)
	allowed := []string{"SELECT", "WITH", "EXPLAIN", "PRAGMA TABLE_INFO", "PRAGMA INDEX_LIST", "PRAGMA INDEX_INFO"}
	for _, kw := range allowed {
		if strings.HasPrefix(upper, kw) {
			return nil
		}
	}
	return fmt.Errorf("%w: query begins with %q", ErrWriteSQLDenied, firstWord(stripped))
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n("); i >= 0 {
		return s[:i]
	}
	return s
}
