package db

import (
	"context"
	"fmt"
)

type ColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"not_null"`
	PrimaryKey bool   `json:"primary_key"`
	Default    string `json:"default,omitempty"`
}

type TableInfo struct {
	Name    string       `json:"name"`
	SQL     string       `json:"sql"`
	Columns []ColumnInfo `json:"columns"`
}

// Schema returns the live structure of every user table in the DB. Only
// includes tables created by sharedwatch (excludes internal sqlite_ tables).
func (s *Store) Schema(ctx context.Context) ([]TableInfo, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite_master: %w", err)
	}
	defer rows.Close()
	var out []TableInfo
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Name, &t.SQL); err != nil {
			return nil, fmt.Errorf("scan table: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		cols, err := s.tableColumns(ctx, out[i].Name)
		if err != nil {
			return nil, err
		}
		out[i].Columns = cols
	}
	return out, nil
}

func (s *Store) tableColumns(ctx context.Context, table string) ([]ColumnInfo, error) {
	// PRAGMA can't be parameterized; we control the input (it came from
	// sqlite_master), so direct interpolation is safe here.
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	var out []ColumnInfo
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
			return nil, fmt.Errorf("scan column: %w", err)
		}
		c := ColumnInfo{Name: name, Type: typ, NotNull: notnull != 0, PrimaryKey: pk != 0}
		if deflt != nil {
			c.Default = fmt.Sprintf("%v", deflt)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
