// Package hints provides a small, profile-driven, modular system for
// emitting "what to run next" suggestions alongside CLI command output.
//
// The package has zero dependencies on other sharedwatch packages — it
// consumes only the Context value passed in by the caller. This keeps it
// portable: lifting it into a standalone module later is a `git mv` away.
// See SW-AGENT-17 (tickets/ticket-smart-hints-profiles-20260524_040256.md)
// §8 for the extract-to-module checklist.
//
// Usage from a command handler:
//
//	ctx := hints.Context{
//	    Profile: hints.ResolveProfile(flagValue, os.Getenv("SHAREDWATCH_HINTS"), isJSON),
//	    Pending: snap.Pending,
//	    Failed:  snap.Failed,
//	    Roots:   toRootRefs(snap.Roots),
//	}
//	set := hints.For("status", ctx)
//	hints.RenderText(os.Stdout, set)              // for human output
//	emitJSONEnvelopeIncluding("next", set.Hints)  // for JSON output
package hints

import "sync"

// Hint is one next-step suggestion. Stable across releases — additive only.
type Hint struct {
	// Name is a stable identifier (e.g. "consume_pending"). Use snake_case.
	// Consumers may key behaviour off Name; keep it semantic, not pretty.
	Name string `json:"name"`
	// Command is the full runnable command line a caller can copy-paste or exec.
	Command string `json:"command"`
	// Reason is a short, English, half-sentence explanation. Optional —
	// omitted in terse profile output and in compact renderers.
	Reason string `json:"reason,omitempty"`
}

// HintSet is the envelope returned by For. The Profile field is included
// so consumers can tell what register was used (useful when --hints was
// auto-promoted by JSON-output detection).
type HintSet struct {
	Profile Profile `json:"profile"`
	Hints   []Hint  `json:"hints"`
}

// RootRef is a minimal projection of a watch-root, carried in Context so
// providers can suggest per-root drills without importing the app package.
type RootRef struct {
	Label   string
	Path    string
	Pending int
}

// DigestRef is a minimal projection of a recent digest row.
type DigestRef struct {
	ID         string
	EventCount int
	Status     string
}

// Context is the state snapshot a Provider inspects. Additive — new fields
// land with zero-value defaults so existing providers keep compiling.
type Context struct {
	// Profile is the resolved profile to emit under. Providers should
	// usually ignore this; truncation to the profile's Limit() happens
	// centrally in For().
	Profile Profile

	// Aggregate counts from the journal.
	Pending       int
	Failed        int
	Digests       int
	UnreadDigests int

	// Multi-root projection. Empty in single-root setups.
	Roots []RootRef

	// Top observed actor in the relevant window (empty if none).
	TopActor string

	// Cursor state for `events list --cursor-name`.
	HasCursor      bool
	CursorName     string
	CursorReturned int

	// Recent digest IDs, newest first; max ~5.
	RecentDigests []DigestRef

	// For `digest show <id>` — the id just shown.
	ShownDigestID string

	// For `events stats --root <root>` — the root scope + its top type.
	StatsRoot    string
	StatsTopType string

	// For `init` — the resolved watch path the binary just printed.
	WatchPath string

	// For `version` — whether release/LATEST advertises a newer tag.
	NewerExists bool
	Latest      string

	// For `overview` — top-N actors and types so the provider can
	// generate narrow drill commands without re-querying.
	TopActors []string
	TopTypes  []string
}

// Provider is a pure function: state in, hints out. Providers must NOT
// truncate to the profile limit themselves — return everything they would
// usefully suggest; For() applies the cap.
type Provider func(ctx Context) []Hint

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Provider)
)

// Register wires a provider for a command name. Idempotent: a second
// registration replaces the first (useful for tests). Command names are
// space-separated for multi-word commands (e.g. "digest list").
func Register(command string, p Provider) {
	if command == "" || p == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[command] = p
}

// For resolves the registered provider for command, runs it against ctx,
// then truncates to ctx.Profile.Limit(). Returns an empty HintSet (not nil)
// when the command has no provider or the profile is off — so callers can
// safely range over set.Hints without nil checks.
func For(command string, ctx Context) HintSet {
	set := HintSet{Profile: ctx.Profile, Hints: nil}
	if ctx.Profile == ProfileOff {
		return set
	}
	registryMu.RLock()
	p := registry[command]
	registryMu.RUnlock()
	if p == nil {
		return set
	}
	raw := p(ctx)
	limit := ctx.Profile.Limit()
	if limit > 0 && len(raw) > limit {
		raw = raw[:limit]
	}
	// For terse profile, also strip reasons (single-line UX).
	if ctx.Profile == ProfileTerse {
		for i := range raw {
			raw[i].Reason = ""
		}
	}
	set.Hints = raw
	return set
}

// Clear is a test helper that wipes the registry. Production code should
// not call this; it exists so unit tests can isolate provider behaviour.
func Clear() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]Provider)
}
