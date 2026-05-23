package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sharedwatch/internal/config"
)

func TestActiveModeExtendsOnConsume(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = filepath.Join(t.TempDir(), "queue.db")
	cfg.ActiveTTL = 2 * time.Minute
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.SetActive(context.Background(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := a.TestEmit(context.Background(), "hello.md"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := a.ConsumeNow(context.Background()); err != nil || !ok {
		t.Fatalf("consume failed ok=%v err=%v", ok, err)
	}
	rt, err := a.Store.GetRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(rt.ActiveUntil) < time.Minute {
		t.Fatalf("expected active ttl extension, got %s", rt.ActiveUntil)
	}
}
