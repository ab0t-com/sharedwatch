package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSnapshotIncludeFilter(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"keep.md":  "yes",
		"skip.bin": "no",
		"also.md":  "yes",
		"skip.log": "no",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := BuildSnapshotWithOptions(root, true, nil, Options{Includes: []string{"*.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Files["keep.md"]; !ok {
		t.Errorf("keep.md should be present")
	}
	if _, ok := s.Files["also.md"]; !ok {
		t.Errorf("also.md should be present")
	}
	if _, ok := s.Files["skip.bin"]; ok {
		t.Errorf("skip.bin should be filtered out by --include *.md")
	}
}

func TestBuildSnapshotHashing(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small.txt")
	big := filepath.Join(root, "big.bin")
	if err := os.WriteFile(small, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, []byte(strings.Repeat("a", 1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := BuildSnapshotWithOptions(root, true, nil, Options{HashEnabled: true, HashMaxSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Files["small.txt"].Hash; got == "" || len(got) != 64 {
		t.Fatalf("small file should be hashed, got %q", got)
	}
	if got := s.Files["big.bin"].Hash; got != "" {
		t.Fatalf("big file should be skipped (over cap), got hash %q", got)
	}

	// And: with hashing disabled, nothing is hashed even if under cap.
	s2, _ := BuildSnapshotWithOptions(root, true, nil, Options{HashEnabled: false})
	if got := s2.Files["small.txt"].Hash; got != "" {
		t.Fatalf("expected no hash when disabled, got %q", got)
	}
}
