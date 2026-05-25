package events

// SW-AGENT-30 — event-emission profile + class taxonomy.
//
// This file is the source of truth for "which event classes belong to
// which tier." Two readers:
//
//   1. cmd/sharedwatch/config_show.go (Phase 1) — uses TierClasses +
//      ResolveEffectiveClasses to render the derived
//      `emit_effective_classes` map so operators can verify their
//      config landed correctly.
//   2. (Phase 2 — not yet) Every emit-site in the codebase will route
//      through a ShouldEmit(eventType, profile, overrides) function
//      added here, mapping types to classes via TypeClass.
//
// Phase 1 ships data only. Phase 2 adds the lookup functions. Keeping
// the table in one place from the start avoids the duplication risk
// flagged in the SW-AGENT-30 worklog pre-flight note.

// Class is the configurable unit of event emission. Users toggle
// classes (not individual event types) via `emit_overrides` —
// per-class is the right granularity for v1 (see
// docs/design/event-broker-consumer-contracts-20260525.md §6, OQ-3).
type Class string

const (
	// Always-on classes (live at the "minimal" tier). These fire
	// regardless of emit_profile; the tier system doesn't gate them.
	ClassFile       Class = "file"       // file.created/.modified/.deleted/.renamed (watcher-source)
	ClassReconciler Class = "reconciler" // reconciler-source file events
	ClassHook       Class = "hook"       // hook.completed/.failed (already gated by --on-digest flag)

	// Standard-tier classes — useful integration signals at low volume.
	ClassDigest             Class = "digest"              // digest.created
	ClassLifecycle          Class = "lifecycle"           // daemon.started/.stopping/.crashed
	ClassModeChange         Class = "mode_change"         // mode.changed
	ClassRetention          Class = "retention"           // retention.ran
	ClassFailureThreshold   Class = "failure_threshold"   // events.failed_threshold + events.stuck_detected + events.retried_batch
	ClassCoord              Class = "coord"               // lease.* + intent.*
	ClassActorLifecycle     Class = "actor_lifecycle"     // actor.registered + actor.removed
	ClassReconcileThreshold Class = "reconcile_threshold" // reconcile.drift_detected

	// Verbose-tier classes — operational observability; opt-in.
	ClassModeTTL           Class = "mode_ttl"            // mode.ttl_extended
	ClassActorStale        Class = "actor_stale"         // actor.went_stale
	ClassReconcilePerCycle Class = "reconcile_per_cycle" // reconcile.ran
	ClassSnapshot          Class = "snapshot"            // snapshot.taken

	// All-tier classes — full audit / replay; can be chatty.
	ClassActorHeartbeat Class = "actor_heartbeat" // actor.heartbeat_received
)

// AllClasses returns every defined class in stable order. Used by
// config_show to render the effective-classes map deterministically.
// New classes added in later versions extend this list at the end.
func AllClasses() []Class {
	return []Class{
		// minimal tier
		ClassFile, ClassReconciler, ClassHook,
		// standard tier
		ClassDigest, ClassLifecycle, ClassModeChange, ClassRetention,
		ClassFailureThreshold, ClassCoord, ClassActorLifecycle,
		ClassReconcileThreshold,
		// verbose tier
		ClassModeTTL, ClassActorStale, ClassReconcilePerCycle, ClassSnapshot,
		// all tier
		ClassActorHeartbeat,
	}
}

// Profile is the tier-name type. Empty / unknown profile names resolve
// to ProfileStandard (the smart default).
type Profile string

const (
	ProfileMinimal  Profile = "minimal"
	ProfileStandard Profile = "standard"
	ProfileVerbose  Profile = "verbose"
	ProfileAll      Profile = "all"
)

// TierClasses returns the class enable map for the supplied profile
// name BEFORE per-class overrides apply. Each tier is the previous
// tier's set plus its own additions (minimal ⊂ standard ⊂ verbose ⊂ all).
//
// Unknown / empty profile names fall back to "standard" — the smart
// default. We don't error on bad input because (a) config files
// shouldn't be able to brick the daemon, and (b) "standard" is the
// right answer for the typical user anyway.
func TierClasses(profile string) map[Class]bool {
	// minimal: classes that emit unconditionally. Even users on the
	// strictest tier see file events — this is the v0.0.x baseline.
	minimal := map[Class]bool{
		ClassFile:       true,
		ClassReconciler: true,
		ClassHook:       true,
	}

	standard := copyClassMap(minimal)
	for _, c := range []Class{
		ClassDigest, ClassLifecycle, ClassModeChange, ClassRetention,
		ClassFailureThreshold, ClassCoord, ClassActorLifecycle,
		ClassReconcileThreshold,
	} {
		standard[c] = true
	}

	verbose := copyClassMap(standard)
	for _, c := range []Class{
		ClassModeTTL, ClassActorStale, ClassReconcilePerCycle, ClassSnapshot,
	} {
		verbose[c] = true
	}

	all := copyClassMap(verbose)
	all[ClassActorHeartbeat] = true

	switch Profile(profile) {
	case ProfileMinimal:
		return minimal
	case ProfileStandard, Profile(""):
		return standard
	case ProfileVerbose:
		return verbose
	case ProfileAll:
		return all
	default:
		// Unknown profile name — same fallback as empty string.
		// Caller (config-resolution layer) should log a warning when
		// it detects this; this function stays pure (no I/O).
		return standard
	}
}

// ResolveEffectiveClasses applies the per-class overrides on top of
// the profile's base set and returns the final emission decision per
// class. Used by config_show to render `emit_effective_classes` for
// operator verification.
//
// Override semantics:
//   - overrides[class]=true  → class emits regardless of tier baseline
//   - overrides[class]=false → class is suppressed regardless of tier baseline
//   - class not in overrides → tier baseline decides
//   - unknown class name in overrides → silently ignored (forward-compat
//     for classes added in later versions); a future caller may want
//     to surface unknown class names back to the user, but the resolve
//     function itself stays pure
func ResolveEffectiveClasses(profile string, overrides map[string]bool) map[Class]bool {
	base := TierClasses(profile)
	for k, v := range overrides {
		c := Class(k)
		// Only apply overrides for KNOWN classes. Unknown keys are
		// silently ignored — see comment above.
		if _, known := knownClasses[c]; known {
			base[c] = v
		}
	}
	return base
}

// knownClasses is the membership-test set built once for
// ResolveEffectiveClasses. Updated automatically via AllClasses() —
// any class added to AllClasses() is recognised here for free.
var knownClasses = func() map[Class]bool {
	m := make(map[Class]bool, 16)
	for _, c := range AllClasses() {
		m[c] = true
	}
	return m
}()

// IsKnownProfile reports whether the supplied string names a valid
// profile tier. Used by the config-resolution layer to log a warning
// when an unknown profile name is supplied (before TierClasses falls
// back to standard).
func IsKnownProfile(profile string) bool {
	switch Profile(profile) {
	case ProfileMinimal, ProfileStandard, ProfileVerbose, ProfileAll:
		return true
	default:
		return false
	}
}

// copyClassMap is a tiny helper to copy a class enable map. Used by
// TierClasses so each tier builds on the previous without aliasing.
func copyClassMap(src map[Class]bool) map[Class]bool {
	dst := make(map[Class]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
