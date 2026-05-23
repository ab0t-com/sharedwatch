package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSchemaReturnsMigratedTables(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tables, err := store.Schema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"events": false, "digests": false, "runtime_state": false, "snapshots": false, "cursors": false}
	for _, tbl := range tables {
		if _, ok := want[tbl.Name]; ok {
			want[tbl.Name] = true
			if len(tbl.Columns) == 0 {
				t.Errorf("table %s has no columns", tbl.Name)
			}
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("expected table %s in schema", name)
		}
	}
}
