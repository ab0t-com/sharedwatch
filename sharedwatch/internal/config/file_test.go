package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "watch_path: /tmp/shared\nactive_interval: 7s\nmax_batch_size: 12\nignore_patterns: a,b,c\nretention_days: 9\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WatchPath != "/tmp/shared" {
		t.Fatalf("unexpected watch path: %s", cfg.WatchPath)
	}
	if cfg.ActiveInterval != 7*time.Second {
		t.Fatalf("unexpected active interval: %s", cfg.ActiveInterval)
	}
	if cfg.MaxBatchSize != 12 {
		t.Fatalf("unexpected batch size: %d", cfg.MaxBatchSize)
	}
	if len(cfg.IgnorePatterns) != 3 {
		t.Fatalf("unexpected ignore patterns: %#v", cfg.IgnorePatterns)
	}
	if cfg.RetentionDays != 9 {
		t.Fatalf("unexpected retention days: %d", cfg.RetentionDays)
	}
}
