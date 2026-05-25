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
	//
	// Note: ClassFile covers file events from BOTH the watcher path AND
	// the reconciler recovery path — they share `Type` (e.g.
	// `file.created`) and differ only in `Source`. Consumers who want
	// to exclude reconciler dupes filter at read time via
	// `events list --source watcher`, not via emit-class config.
	// SW-AGENT-30 Phase 2 corrected the Phase-1 ClassReconciler
	// misconception (dropped as a class; it never had a Type to map to).
	ClassFile Class = "file" // file.created/.modified/.deleted/.renamed (any source)
	ClassHook Class = "hook" // hook.completed/.failed (already gated by --on-digest flag)

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
		ClassFile, ClassHook,
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
		ClassFile: true,
		ClassHook: true,
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
// the profile's base set and returns the COMPLETE truth table — every
// known class mapped to its final emission decision (true=emit,
// false=suppress). Returning the full table (not just the enabled set)
// gives callers two guarantees:
//
//  1. JSON serialisation includes all classes — operators see
//     `actor_heartbeat: false` explicitly rather than a missing key,
//     matching the text-mode `formatEffectiveClasses` output.
//  2. `result[Class]` is unambiguous — false always means "suppressed",
//     never "key missing." Callers don't need to disambiguate.
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
	// Ensure every known class has an entry (defaulting to false for
	// classes the tier doesn't enable). This makes the output a
	// complete truth table rather than a sparse enabled-set.
	result := make(map[Class]bool, len(knownClasses))
	for c := range knownClasses {
		result[c] = base[c] // false if missing from tier baseline
	}
	for k, v := range overrides {
		c := Class(k)
		if _, known := knownClasses[c]; known {
			result[c] = v
		}
	}
	return result
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

// TypeClass maps an event Type to the Class it belongs to. ShouldEmit
// uses this to look up whether the event's class is enabled by the
// current profile + overrides.
//
// SW-AGENT-30 Phase 2. Phase 3 will add the ~20 new event types and
// extend this switch with their class mappings; today the function
// covers the v0.1.0 type surface (file events + hook meta-events).
//
// Unknown / unrecognised types fall through to ClassFile — the safe
// backwards-compatible default. Rationale: legacy DBs may carry event
// types this binary doesn't recognise; we want them to emit (at the
// minimal tier) rather than be silently dropped. Phase 3+ types will
// each get an explicit case.
func TypeClass(t Type) Class {
	switch t {
	// File events (watcher AND reconciler sources share Type).
	case TypeCreated, TypeModified, TypeDeleted, TypeRenamed:
		return ClassFile

	// Hook meta-events (SW-AGENT-29). Class is always-on at minimal
	// tier — gating already happens via the --on-digest flag (no
	// flag set ⇒ no hook fires ⇒ no meta-event emitted in the first
	// place; the class boundary is belt-and-suspenders).
	case TypeHookCompleted, TypeHookFailed:
		return ClassHook

	default:
		// Unknown types — backwards-compatible default. Belongs at
		// minimal tier so legacy / future-version events aren't silently
		// dropped by an outdated client binary.
		return ClassFile
	}
}

// ShouldEmit is the central emission authority. Every emit site in
// the codebase (Phase 4 wires this) routes through it:
//
//	if events.ShouldEmit(e.Type, cfg.EmitProfile, cfg.EmitOverrides) {
//	    _ = store.InsertEvent(ctx, e)
//	}
//
// Returns true iff the event's class (per TypeClass) is enabled by
// the resolved tier profile combined with any per-class overrides.
//
// Empty profile, empty overrides, AND nil overrides all yield the
// smart-default behaviour (standard tier; no overrides applied). This
// ensures callers that haven't been updated yet — or tests that don't
// set the profile — see the v0.1.0 default behaviour rather than
// accidentally emit nothing.
//
// SW-AGENT-30 Phase 2.3 (the backwards-compat default).
func ShouldEmit(t Type, profile string, overrides map[string]bool) bool {
	class := TypeClass(t)
	return ResolveEffectiveClasses(profile, overrides)[class]
}
