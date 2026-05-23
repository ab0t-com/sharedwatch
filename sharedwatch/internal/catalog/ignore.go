package catalog

import "path/filepath"

// Included returns true if the relPath matches any of the positive patterns.
// Empty patterns means "include everything" — callers should not invoke this
// in that case.
func Included(relPath string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, filepath.Base(relPath)); ok {
			return true
		}
		if ok, _ := filepath.Match(p, relPath); ok {
			return true
		}
		if relPath == p {
			return true
		}
	}
	return false
}

func Ignored(relPath string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, filepath.Base(relPath)); ok {
			return true
		}
		if ok, _ := filepath.Match(p, relPath); ok {
			return true
		}
		if relPath == p {
			return true
		}
		if filepath.Base(relPath) == p {
			return true
		}
	}
	return false
}
