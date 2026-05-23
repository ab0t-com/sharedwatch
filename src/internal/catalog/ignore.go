package catalog

import (
	"path/filepath"
	"strings"
)

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

// Ignored returns true when relPath should be skipped by the watcher. A path
// is ignored if any pattern matches:
//   - the full rel_path (`filepath.Match` or exact equality), OR
//   - the basename (e.g. `*.tmp` matches `auth/scratch.tmp`), OR
//   - ANY path segment (e.g. `.git` matches `.git/objects/abc` because one of
//     the segments is `.git`). Without this third check, the documented
//     default `.git` pattern only matched the literal file named `.git` and
//     leaked everything under `.git/` into the journal — caught by dogfood
//     scenario 13.
func Ignored(relPath string, patterns []string) bool {
	segments := strings.Split(relPath, "/")
	base := filepath.Base(relPath)
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
		if ok, _ := filepath.Match(p, relPath); ok {
			return true
		}
		if relPath == p {
			return true
		}
		if base == p {
			return true
		}
		// Segment match — covers the case where any ancestor directory matches
		// the pattern. e.g. .git matches .git/objects/abc.
		for _, seg := range segments {
			if seg == p {
				return true
			}
			if ok, _ := filepath.Match(p, seg); ok {
				return true
			}
		}
	}
	return false
}
