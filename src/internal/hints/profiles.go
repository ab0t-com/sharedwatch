package hints

import "strings"

// Profile controls how many hints emit and in what register.
// See SW-AGENT-17 §3.3 for the resolution rules.
type Profile string

const (
	// ProfileDefault is the human-facing register: 1–4 contextual hints
	// with reasons. Used for text output in interactive shells.
	ProfileDefault Profile = "default"

	// ProfileAgent is the machine register: up to 8 hints with reasons,
	// designed for AI agents consuming the JSON envelope.
	ProfileAgent Profile = "agent"

	// ProfileTerse is the single-line register: 0 or 1 hint, no reason.
	// Useful when piping through to a script that wants exactly one
	// suggestion (the most likely next move).
	ProfileTerse Profile = "terse"

	// ProfileOff suppresses hints entirely. Useful when an operator
	// finds them noisy or when output is being captured for diff.
	ProfileOff Profile = "off"
)

// Limit returns the maximum number of hints a profile will emit.
// 0 means "unlimited" (no profile currently uses 0).
func (p Profile) Limit() int {
	switch p {
	case ProfileAgent:
		return 8
	case ProfileDefault:
		return 4
	case ProfileTerse:
		return 1
	case ProfileOff:
		return 0
	default:
		return 4
	}
}

// IsValid reports whether p is one of the recognised profiles.
func (p Profile) IsValid() bool {
	switch p {
	case ProfileDefault, ProfileAgent, ProfileTerse, ProfileOff:
		return true
	}
	return false
}

// ResolveProfile picks the effective profile from three sources of input,
// in order of precedence:
//
//  1. The CLI flag value (`--hints <profile>`), if non-empty.
//  2. The environment variable value, if non-empty.
//  3. When isJSON is true, promote to ProfileAgent.
//  4. Default to ProfileDefault.
//
// Unrecognised values fall through to the next step rather than failing,
// so a typo in an env var doesn't break the tool — it just gets the
// default behaviour. Callers that want strict validation should check
// IsValid before calling ResolveProfile (or after, on the result).
func ResolveProfile(flag, env string, isJSON bool) Profile {
	if p := normalizeProfile(flag); p.IsValid() {
		return p
	}
	if p := normalizeProfile(env); p.IsValid() {
		return p
	}
	if isJSON {
		return ProfileAgent
	}
	return ProfileDefault
}

func normalizeProfile(s string) Profile {
	return Profile(strings.ToLower(strings.TrimSpace(s)))
}
