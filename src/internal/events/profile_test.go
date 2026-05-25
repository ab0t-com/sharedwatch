package events

import (
	"testing"
)

func TestTierClassesMinimal(t *testing.T) {
	m := TierClasses("minimal")
	for _, c := range []Class{ClassFile, ClassHook} {
		if !m[c] {
			t.Errorf("minimal tier should include %q", c)
		}
	}
	// Nothing else should be enabled at minimal — the v0.0.x baseline.
	for _, c := range []Class{
		ClassDigest, ClassLifecycle, ClassModeChange, ClassRetention,
		ClassFailureThreshold, ClassCoord, ClassActorLifecycle,
		ClassReconcileThreshold, ClassModeTTL, ClassActorStale,
		ClassReconcilePerCycle, ClassSnapshot, ClassActorHeartbeat,
	} {
		if m[c] {
			t.Errorf("minimal tier should NOT include %q", c)
		}
	}
}

func TestTierClassesStandardExtendsMinimal(t *testing.T) {
	mini := TierClasses("minimal")
	std := TierClasses("standard")
	// Every class enabled at minimal must also be enabled at standard.
	for c, enabled := range mini {
		if enabled && !std[c] {
			t.Errorf("standard tier must include all minimal classes; missing %q", c)
		}
	}
	// Standard adds these specifically.
	for _, c := range []Class{
		ClassDigest, ClassLifecycle, ClassModeChange, ClassRetention,
		ClassFailureThreshold, ClassCoord, ClassActorLifecycle,
		ClassReconcileThreshold,
	} {
		if !std[c] {
			t.Errorf("standard tier should include %q", c)
		}
	}
	// Standard does NOT enable verbose-tier classes.
	for _, c := range []Class{ClassModeTTL, ClassActorStale, ClassReconcilePerCycle, ClassSnapshot, ClassActorHeartbeat} {
		if std[c] {
			t.Errorf("standard tier should NOT include verbose-tier class %q", c)
		}
	}
}

func TestTierClassesVerboseExtendsStandard(t *testing.T) {
	std := TierClasses("standard")
	v := TierClasses("verbose")
	for c, enabled := range std {
		if enabled && !v[c] {
			t.Errorf("verbose tier must include all standard classes; missing %q", c)
		}
	}
	// Verbose adds these.
	for _, c := range []Class{ClassModeTTL, ClassActorStale, ClassReconcilePerCycle, ClassSnapshot} {
		if !v[c] {
			t.Errorf("verbose tier should include %q", c)
		}
	}
	// Verbose does NOT enable actor_heartbeat (all-tier only).
	if v[ClassActorHeartbeat] {
		t.Error("verbose tier should NOT include actor_heartbeat")
	}
}

func TestTierClassesAllExtendsVerbose(t *testing.T) {
	v := TierClasses("verbose")
	a := TierClasses("all")
	for c, enabled := range v {
		if enabled && !a[c] {
			t.Errorf("all tier must include all verbose classes; missing %q", c)
		}
	}
	if !a[ClassActorHeartbeat] {
		t.Error("all tier must include actor_heartbeat")
	}
}

func TestTierClassesFallbackOnUnknown(t *testing.T) {
	// Unknown profile names fall back to standard — config files
	// shouldn't brick the daemon.
	cases := []string{"", "made-up", "MINIMAL", "Standard", "verbose-tier"}
	std := TierClasses("standard")
	for _, p := range cases {
		got := TierClasses(p)
		if !classMapEqual(got, std) {
			t.Errorf("TierClasses(%q) should fall back to standard, got differences", p)
		}
	}
}

func TestTierIsolationNoCrossTierMutation(t *testing.T) {
	// Confirm TierClasses returns independent maps — mutating one
	// shouldn't change another. Regression guard against accidental
	// map-aliasing bugs in the copy-builder logic.
	std1 := TierClasses("standard")
	std1[ClassFile] = false // try to mutate
	std2 := TierClasses("standard")
	if std2[ClassFile] != true {
		t.Error("mutating one returned map should not affect subsequent calls")
	}
	v := TierClasses("verbose")
	if v[ClassFile] != true {
		t.Error("mutating standard should not affect verbose")
	}
}

func TestResolveEffectiveClassesOverrideIn(t *testing.T) {
	// Override a verbose-only class TO true at standard tier.
	r := ResolveEffectiveClasses("standard", map[string]bool{
		"actor_heartbeat": true,
	})
	if !r[ClassActorHeartbeat] {
		t.Error("override actor_heartbeat=true should enable it at standard tier")
	}
}

func TestResolveEffectiveClassesOverrideOut(t *testing.T) {
	// Override a standard-tier class TO false at standard tier.
	r := ResolveEffectiveClasses("standard", map[string]bool{
		"coord": false,
	})
	if r[ClassCoord] {
		t.Error("override coord=false should disable it at standard tier")
	}
}

func TestResolveEffectiveClassesUnknownClassIgnored(t *testing.T) {
	// Forward-compat: unknown class names in overrides don't error
	// and don't pollute the result map.
	r := ResolveEffectiveClasses("standard", map[string]bool{
		"foo_bar_baz": true,
	})
	if _, ok := r["foo_bar_baz"]; ok {
		t.Error("unknown class names in overrides should be silently dropped")
	}
}

func TestResolveEffectiveClassesNilOverrides(t *testing.T) {
	// nil overrides should produce: every class enabled in the tier
	// baseline set to true; every other known class set to false.
	// (The new contract — ResolveEffectiveClasses returns the COMPLETE
	// truth table, unlike TierClasses which returns the sparse
	// enabled-set. See ResolveEffectiveClasses doc for rationale.)
	r := ResolveEffectiveClasses("verbose", nil)
	if len(r) != len(AllClasses()) {
		t.Errorf("ResolveEffectiveClasses should return all %d known classes, got %d", len(AllClasses()), len(r))
	}
	base := TierClasses("verbose")
	for _, c := range AllClasses() {
		want := base[c] // false if not in tier baseline
		if r[c] != want {
			t.Errorf("ResolveEffectiveClasses(verbose, nil)[%q] = %v, want %v (per TierClasses baseline)", c, r[c], want)
		}
	}
}

func TestResolveEffectiveClassesCompleteTruthTable(t *testing.T) {
	// Regression guard for the Phase 2 contract: ResolveEffectiveClasses
	// must always return the complete truth table — every known class
	// with an explicit true/false. This is what makes JSON output match
	// text output (both render all 15 classes) and what makes
	// `result[class]` unambiguous (false = suppressed, not "missing").
	for _, profile := range []string{"minimal", "standard", "verbose", "all"} {
		r := ResolveEffectiveClasses(profile, nil)
		if len(r) != len(AllClasses()) {
			t.Errorf("profile=%q: ResolveEffectiveClasses returned %d entries, want all %d known classes", profile, len(r), len(AllClasses()))
		}
		for _, c := range AllClasses() {
			if _, ok := r[c]; !ok {
				t.Errorf("profile=%q: class %q missing from result map (must be present with explicit bool)", profile, c)
			}
		}
	}
}

func TestIsKnownProfile(t *testing.T) {
	for _, p := range []string{"minimal", "standard", "verbose", "all"} {
		if !IsKnownProfile(p) {
			t.Errorf("IsKnownProfile(%q) should be true", p)
		}
	}
	for _, p := range []string{"", "MINIMAL", "Standard", "fancy", "off"} {
		if IsKnownProfile(p) {
			t.Errorf("IsKnownProfile(%q) should be false", p)
		}
	}
}

func TestAllClassesIncludesEverything(t *testing.T) {
	// Regression guard: AllClasses() must list every defined Class
	// constant. If a class is added to the const block but not to
	// AllClasses, knownClasses will be incomplete and overrides for
	// the new class will be silently ignored — a real bug.
	all := AllClasses()
	if len(all) != 15 {
		t.Errorf("AllClasses() returned %d classes; expected 15 (update this test count when adding classes)", len(all))
	}
	seen := make(map[Class]bool, len(all))
	for _, c := range all {
		if seen[c] {
			t.Errorf("AllClasses() lists %q more than once", c)
		}
		seen[c] = true
	}
}

// ====================================================================
// SW-AGENT-30 Phase 2 — TypeClass + ShouldEmit
// ====================================================================

func TestTypeClassFileEvents(t *testing.T) {
	for _, ty := range []Type{TypeCreated, TypeModified, TypeDeleted, TypeRenamed} {
		if got := TypeClass(ty); got != ClassFile {
			t.Errorf("TypeClass(%q) = %q, want %q", ty, got, ClassFile)
		}
	}
}

func TestTypeClassHookEvents(t *testing.T) {
	for _, ty := range []Type{TypeHookCompleted, TypeHookFailed} {
		if got := TypeClass(ty); got != ClassHook {
			t.Errorf("TypeClass(%q) = %q, want %q", ty, got, ClassHook)
		}
	}
}

func TestTypeClassUnknownFallsBackToFile(t *testing.T) {
	// Backwards-compat: legacy or future-version event types this
	// binary doesn't recognise must fall through to ClassFile (minimal
	// tier) so they aren't silently dropped.
	cases := []Type{"", "made.up.type", "future.event"}
	for _, ty := range cases {
		if got := TypeClass(ty); got != ClassFile {
			t.Errorf("TypeClass(%q) = %q, want fallback ClassFile", ty, got)
		}
	}
}

func TestShouldEmitMinimalProfileHidesEverythingButFileAndHook(t *testing.T) {
	// At the minimal tier, only file + hook classes emit. Phase 3 will
	// add ~20 more types; this test will be extended then. For now we
	// verify the contract holds for the v0.1.0 type surface.
	wantOn := []Type{TypeCreated, TypeModified, TypeDeleted, TypeRenamed, TypeHookCompleted, TypeHookFailed}
	for _, ty := range wantOn {
		if !ShouldEmit(ty, "minimal", nil) {
			t.Errorf("ShouldEmit(%q, minimal) should be true", ty)
		}
	}
}

func TestShouldEmitStandardProfileIncludesMinimal(t *testing.T) {
	// Anything emitting at minimal must also emit at standard.
	for _, ty := range []Type{TypeCreated, TypeHookCompleted} {
		if !ShouldEmit(ty, "standard", nil) {
			t.Errorf("ShouldEmit(%q, standard) should be true (inherited from minimal)", ty)
		}
	}
}

func TestShouldEmitOverrideOptsClassOut(t *testing.T) {
	// Even at standard tier, override file=false disables ClassFile.
	// (Caveat emptor: users who set this turn off the product's
	// primary signal; documented in profile.go.)
	if ShouldEmit(TypeCreated, "standard", map[string]bool{"file": false}) {
		t.Error("override file=false should suppress file events at any tier")
	}
}

func TestShouldEmitOverrideOptsClassIn(t *testing.T) {
	// Phase 3 will add types that map to higher-tier classes; until
	// those exist we test the override-in path with the actor_heartbeat
	// class (it has no mapped types yet, but the override semantics
	// still apply — ResolveEffectiveClasses sets the bit, and once
	// Phase 3 adds the type, ShouldEmit returns true).
	r := ResolveEffectiveClasses("minimal", map[string]bool{"actor_heartbeat": true})
	if !r[ClassActorHeartbeat] {
		t.Error("override actor_heartbeat=true should enable the class at minimal tier")
	}
}

func TestShouldEmitEmptyProfileDefaultsToStandard(t *testing.T) {
	// Phase 2.3 backwards-compat: empty profile string falls through
	// to the standard tier. Tests that haven't been updated to set the
	// profile see the v0.1.0 default behaviour rather than accidentally
	// emitting nothing.
	if !ShouldEmit(TypeCreated, "", nil) {
		t.Error("empty profile should default to standard, allowing file events")
	}
	if !ShouldEmit(TypeHookCompleted, "", nil) {
		t.Error("empty profile should default to standard, allowing hook events")
	}
}

func TestShouldEmitNilOverridesIsSafe(t *testing.T) {
	// Defensive: a nil overrides map must not panic on map-read.
	if !ShouldEmit(TypeCreated, "standard", nil) {
		t.Error("nil overrides should not break ShouldEmit")
	}
}

func TestShouldEmitUnknownProfileFallsBackToStandard(t *testing.T) {
	// An unknown profile name should behave as standard at the
	// ShouldEmit layer (the warning is logged separately at config-
	// resolution time; the runtime authority should not brick on bad
	// data).
	if !ShouldEmit(TypeCreated, "totally-made-up", nil) {
		t.Error("unknown profile should fall back to standard (file events emit)")
	}
}

func classMapEqual(a, b map[Class]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
