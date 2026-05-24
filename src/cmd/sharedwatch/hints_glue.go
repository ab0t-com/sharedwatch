package main

import (
	"os"

	"sharedwatch/internal/app"
	"sharedwatch/internal/config"
	"sharedwatch/internal/hints"
)

// hintsProfileFlag holds the value of the root --hints flag for the
// duration of this invocation. main() sets it from the flag value during
// parsing; handlers read it via resolveHintsProfile().
var hintsProfileFlag string

// quietMode mirrors the --quiet root flag. When true:
//   - resolveHintsProfile returns ProfileOff regardless of other inputs
//   - friendly informational lines (init / stop / no-daemon) are suppressed
//
// Errors still print. quietMode never affects the actual data output.
var quietMode bool

// configSearch records what the config-file search step actually did
// for this invocation. main() populates it during config load; handlers
// that need to render it (e.g. `config show`) read it directly.
var configSearch config.SearchResult

// resolveHintsProfile picks the effective profile for the current
// invocation. --quiet (highest) > --hints flag > SHAREDWATCH_HINTS env
// > json-output promotion > default. Pass isJSON=true when the
// command's output is the JSON envelope (so agents reading JSON get
// the richer hint set automatically).
func resolveHintsProfile(isJSON bool) hints.Profile {
	if quietMode {
		return hints.ProfileOff
	}
	return hints.ResolveProfile(hintsProfileFlag, os.Getenv("SHAREDWATCH_HINTS"), isJSON)
}

// toHintsRoots projects app.RootView slices into the leaner
// hints.RootRef so the hints package stays free of app-package imports.
func toHintsRoots(in []app.RootView) []hints.RootRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]hints.RootRef, 0, len(in))
	for _, r := range in {
		out = append(out, hints.RootRef{Label: r.Label, Path: r.Path, Pending: r.Pending})
	}
	return out
}
