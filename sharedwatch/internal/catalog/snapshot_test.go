package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSnapshotIgnoresPatterns(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := BuildSnapshot(root, true, []string{"*.tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Files["x.tmp"]; ok {
		t.Fatal("tmp file should be ignored")
	}
	if _, ok := s.Files["a.md"]; !ok {
		t.Fatal("a.md should be present")
	}
}
