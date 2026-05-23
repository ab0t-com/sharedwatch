package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/db"
)

func TestReconcileColdStartEmitsCreatedForExistingFiles(t *testing.T) {
	ctx := context.Background()
	watch := t.TempDir()
	if err := os.WriteFile(filepath.Join(watch, "preexisting.md"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	svc := Service{Store: store, WatchPath: watch, Recursive: true, Window: 5 * time.Second, RetentionDays: 30}

	// Cold start: pre-existing files should land as created events, not be
	// silently swallowed into an invisible baseline.
	n, err := svc.RunNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 cold-start event for pre-existing file, got %d", n)
	}
}

func TestReconcileDetectsDrift(t *testing.T) {
	ctx := context.Background()
	watch := t.TempDir()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	svc := Service{Store: store, WatchPath: watch, Recursive: true, Window: 5 * time.Second, RetentionDays: 30}

	// First pass: empty folder, no prior snapshot, no existing files -> 0 events.
	if n, err := svc.RunNow(ctx); err != nil || n != 0 {
		t.Fatalf("expected first pass to record 0 events on empty dir, got n=%d err=%v", n, err)
	}

	if err := os.WriteFile(filepath.Join(watch, "hello.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := svc.RunNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 recovery event for new file, got %d", n)
	}
}

func TestReconcileBoundsSnapshotRows(t *testing.T) {
	ctx := context.Background()
	watch := t.TempDir()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	svc := Service{Store: store, WatchPath: watch, Recursive: true, Window: 5 * time.Second, RetentionDays: 30}
	for i := 0; i < 10; i++ {
		if _, err := svc.RunNow(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshots WHERE source = 'reconciler'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count > 5 {
		t.Fatalf("expected reconciler snapshots bounded to 5, got %d", count)
	}
}
