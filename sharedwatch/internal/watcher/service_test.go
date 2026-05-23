package watcher

import (
	"errors"
	"testing"
)

func TestValidateRelPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty rejected", "", true},
		{"absolute rejected", "/abs/file.md", true},
		{"escape rejected", "../escape.md", true},
		{"deep escape rejected", "ok/../../../escape.md", true},
		{"plain ok", "file.md", false},
		{"nested ok", "dir/sub/file.md", false},
		{"dot ok", "./file.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRelPath(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if tc.wantErr && !errors.Is(err, ErrInvalidRelPath) {
				t.Fatalf("expected ErrInvalidRelPath, got %v", err)
			}
		})
	}
}
