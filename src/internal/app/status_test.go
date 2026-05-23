package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"sharedwatch/internal/config"
)

func TestStatusIncludesHealthFields(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = filepath.Join(t.TempDir(), "queue.db")
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	out, err := a.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"pending=", "failed=", "digests=", "unread_digests=", "archived_digests="} {
		if !strings.Contains(out, s) {
			t.Fatalf("status missing %s in %s", s, out)
		}
	}
}
