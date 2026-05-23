package config

import (
	"strings"
	"testing"
)

func TestValidateLabel(t *testing.T) {
	cases := []struct {
		in      string
		wantErr string
	}{
		{"", "empty"},
		{"auth", ""},
		{"auth-1", ""},
		{"AUTH_2", ""},
		{"-leading-dash", "match"},
		{"all", "reserved"},
		{"none", "reserved"},
		{"mixed", "reserved"},
		{"label=with=equals", "match"},
		{"label,with,commas", "match"},
		{strings.Repeat("a", 64), ""},
		{strings.Repeat("a", 65), "match"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := ValidateLabel(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeRoots(t *testing.T) {
	t.Run("empty passes through", func(t *testing.T) {
		out, err := NormalizeRoots(nil)
		if err != nil || out != nil {
			t.Fatalf("expected (nil, nil), got (%v, %v)", out, err)
		}
	})

	t.Run("auto-label from basename", func(t *testing.T) {
		out, err := NormalizeRoots([]WatchRoot{{Path: "/tmp/auth"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Label != "auth" || out[0].Path != "/tmp/auth" {
			t.Fatalf("unexpected: %+v", out)
		}
	})

	t.Run("explicit label wins over basename", func(t *testing.T) {
		out, err := NormalizeRoots([]WatchRoot{{Label: "alpha", Path: "/tmp/auth"}})
		if err != nil {
			t.Fatal(err)
		}
		if out[0].Label != "alpha" {
			t.Fatalf("explicit label not used: %+v", out)
		}
	})

	t.Run("auto-derived collisions rejected", func(t *testing.T) {
		_, err := NormalizeRoots([]WatchRoot{
			{Path: "/a/work"},
			{Path: "/b/work"},
		})
		if err == nil || !strings.Contains(err.Error(), "collides") {
			t.Fatalf("expected collision error, got %v", err)
		}
	})

	t.Run("explicit duplicate labels rejected", func(t *testing.T) {
		_, err := NormalizeRoots([]WatchRoot{
			{Label: "x", Path: "/a/p"},
			{Label: "x", Path: "/b/p"},
		})
		if err == nil || !strings.Contains(err.Error(), "collides") {
			t.Fatalf("expected duplicate-label error, got %v", err)
		}
	})

	t.Run("same label same path is idempotent", func(t *testing.T) {
		_, err := NormalizeRoots([]WatchRoot{
			{Label: "x", Path: "/a/p"},
			{Label: "x", Path: "/a/p"},
		})
		if err != nil {
			t.Fatalf("idempotent duplicate should not error: %v", err)
		}
	})

	t.Run("empty path rejected", func(t *testing.T) {
		_, err := NormalizeRoots([]WatchRoot{{Label: "x", Path: ""}})
		if err == nil || !strings.Contains(err.Error(), "path is empty") {
			t.Fatalf("expected empty-path error, got %v", err)
		}
	})

	t.Run("reserved label rejected", func(t *testing.T) {
		_, err := NormalizeRoots([]WatchRoot{{Label: "all", Path: "/a"}})
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("expected reserved-label error, got %v", err)
		}
	})
}
