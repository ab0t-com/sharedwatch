package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// WatchRoot is one labeled directory the daemon observes. Label is the stable
// agent-prompt-safe handle that appears in --root flags, the watch_root
// column on events/digests/snapshots, and status output. Path is the absolute
// (resolved) directory on disk.
//
// Single-root setups (the legacy default) use an unlabeled WatchRoot with
// Label = "". The empty-string convention is preserved end-to-end so existing
// callers see no change.
type WatchRoot struct {
	Label string
	Path  string
}

// labelRE accepts the documented grammar: 1–64 chars from [a-zA-Z0-9_-], no
// leading dash. Empty labels are valid (the single-root sentinel) but cannot
// be specified explicitly via flags.
var labelRE = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]{0,63}$`)

// reservedLabels are tokens the CLI grammar treats specially or that would
// confuse `--root <label>` filter parsing.
var reservedLabels = map[string]struct{}{
	"all":   {},
	"none":  {},
	"mixed": {},
}

// ValidateLabel returns nil iff label is acceptable as an explicit user-
// supplied label. Empty label is rejected here — callers wanting the legacy
// single-root sentinel should not call ValidateLabel; they should set
// WatchRoots empty and rely on the bare WatchPath fallback.
func ValidateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("label is empty")
	}
	if _, ok := reservedLabels[label]; ok {
		return fmt.Errorf("label %q is reserved", label)
	}
	if !labelRE.MatchString(label) {
		return fmt.Errorf("label %q must match [a-zA-Z0-9_-]{1,64} with no leading dash", label)
	}
	return nil
}

// AutoLabel derives a default label from a filesystem path (the basename),
// validates it, and returns it. If the basename is empty, reserved, or fails
// the regex, an error is returned so the caller can surface a clear message
// (typically: "ambiguous root; pass --root <label>=<path>").
func AutoLabel(path string) (string, error) {
	base := filepath.Base(strings.TrimRight(path, "/"))
	if err := ValidateLabel(base); err != nil {
		return "", fmt.Errorf("auto-derived label %q from path %q: %w", base, path, err)
	}
	return base, nil
}

// NormalizeRoots resolves every root's path to absolute, validates labels,
// rejects label/path collisions, and returns the canonical slice. Single-root
// callers (empty `in`) pass through unchanged.
func NormalizeRoots(in []WatchRoot) ([]WatchRoot, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seenLabel := make(map[string]string, len(in))
	out := make([]WatchRoot, 0, len(in))
	for i, r := range in {
		if r.Path == "" {
			return nil, fmt.Errorf("root #%d: path is empty", i+1)
		}
		abs, err := filepath.Abs(r.Path)
		if err != nil {
			return nil, fmt.Errorf("root #%d (%q): %w", i+1, r.Path, err)
		}
		abs = strings.TrimRight(abs, "/")
		label := r.Label
		if label == "" {
			auto, err := AutoLabel(abs)
			if err != nil {
				return nil, fmt.Errorf("root #%d (%q): %w; pass an explicit --root <label>=<path>", i+1, r.Path, err)
			}
			label = auto
		} else if err := ValidateLabel(label); err != nil {
			return nil, fmt.Errorf("root #%d: %w", i+1, err)
		}
		if prior, ok := seenLabel[label]; ok && prior != abs {
			return nil, fmt.Errorf("label %q collides between %q and %q; pass distinct labels via --root <label>=<path>", label, prior, abs)
		}
		seenLabel[label] = abs
		out = append(out, WatchRoot{Label: label, Path: abs})
	}
	return out, nil
}
