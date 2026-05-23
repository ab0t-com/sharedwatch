package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type FileState struct {
	RelPath string    `json:"rel_path"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	MTime   time.Time `json:"mtime"`
	Hash    string    `json:"hash,omitempty"`
}

type Snapshot struct {
	TakenAt time.Time            `json:"taken_at"`
	Files   map[string]FileState `json:"files"`
}

// Options controls optional snapshot behavior. Zero value = legacy behavior.
type Options struct {
	Includes    []string // positive filter; empty = match all
	HashEnabled bool
	HashMaxSize int64 // 0 = no cap
}

func BuildSnapshot(root string, recursive bool, ignorePatterns ...[]string) (Snapshot, error) {
	var ignores []string
	if len(ignorePatterns) > 0 {
		ignores = ignorePatterns[0]
	}
	return BuildSnapshotWithOptions(root, recursive, ignores, Options{})
}

func BuildSnapshotWithOptions(root string, recursive bool, ignores []string, opts Options) (Snapshot, error) {
	s := Snapshot{TakenAt: time.Now().UTC(), Files: map[string]FileState{}}
	walkFn := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if !recursive {
				rel, _ := filepath.Rel(root, path)
				if rel != "." && filepath.Dir(rel) == "." {
					return filepath.SkipDir
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if len(opts.Includes) > 0 && !Included(rel, opts.Includes) {
			return nil
		}
		if Ignored(rel, ignores) {
			return nil
		}
		fs := FileState{
			RelPath: rel,
			Path:    path,
			Size:    info.Size(),
			MTime:   info.ModTime().UTC(),
		}
		if opts.HashEnabled && (opts.HashMaxSize <= 0 || info.Size() <= opts.HashMaxSize) {
			if h, err := hashFile(path); err == nil {
				fs.Hash = h
			}
		}
		s.Files[rel] = fs
		return nil
	}
	if err := filepath.WalkDir(root, walkFn); err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	return s, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func SnapshotHash(s Snapshot) string {
	keys := make([]string, 0, len(s.Files))
	for k := range s.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		f := s.Files[k]
		_, _ = io.WriteString(h, f.RelPath)
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, f.MTime.UTC().Format(time.RFC3339Nano))
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, fmt.Sprintf("%d", f.Size))
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, f.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}
