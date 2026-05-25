package events

import (
	"testing"
)

func TestTierClassesMinimal(t *testing.T) {
	m := TierClasses("minimal")
	for _, c := range []Class{ClassFile, ClassReconciler, ClassHook} {
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
	r := ResolveEffectiveClasses("verbose", nil)
	expected := TierClasses("verbose")
	if !classMapEqual(r, expected) {
		t.Error("nil overrides should equal pure TierClasses")
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
	if len(all) != 16 {
		t.Errorf("AllClasses() returned %d classes; expected 16 (update this test count when adding classes)", len(all))
	}
	seen := make(map[Class]bool, len(all))
	for _, c := range all {
		if seen[c] {
			t.Errorf("AllClasses() lists %q more than once", c)
		}
		seen[c] = true
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
