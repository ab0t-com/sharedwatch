package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRawSQLReadOnly(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	t.Run("select allowed", func(t *testing.T) {
		cols, _, err := store.RawSQL(ctx, "SELECT 1 AS x", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 1 || cols[0] != "x" {
			t.Fatalf("unexpected cols %+v", cols)
		}
	})
	t.Run("update denied without write", func(t *testing.T) {
		_, _, err := store.RawSQL(ctx, "UPDATE events SET status='pending'", false)
		if !errors.Is(err, ErrWriteSQLDenied) {
			t.Fatalf("expected ErrWriteSQLDenied, got %v", err)
		}
	})
	t.Run("multi-statement denied", func(t *testing.T) {
		_, _, err := store.RawSQL(ctx, "SELECT 1; DROP TABLE events;", false)
		if !errors.Is(err, ErrWriteSQLDenied) {
			t.Fatalf("expected ErrWriteSQLDenied, got %v", err)
		}
	})
	t.Run("trailing semicolon ok", func(t *testing.T) {
		_, _, err := store.RawSQL(ctx, "SELECT 1;", false)
		if err != nil {
			t.Fatalf("trailing-semi should be allowed: %v", err)
		}
	})
	t.Run("with cte allowed", func(t *testing.T) {
		_, _, err := store.RawSQL(ctx, "WITH t AS (SELECT 1) SELECT * FROM t", false)
		if err != nil {
			t.Fatalf("WITH should be allowed: %v", err)
		}
	})
	t.Run("write flag bypasses", func(t *testing.T) {
		_, _, err := store.RawSQL(ctx, "CREATE TABLE IF NOT EXISTS scratch(x INT)", true)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("comments stripped", func(t *testing.T) {
		_, _, err := store.RawSQL(ctx, "-- comment\n  SELECT 1", false)
		if err != nil {
			t.Fatal(err)
		}
	})
}
