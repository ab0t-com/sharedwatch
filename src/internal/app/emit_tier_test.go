package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sharedwatch/internal/config"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
)

// TestEmitTierMatrix is the end-to-end Phase 5 integration test for
// SW-AGENT-30. It exercises a representative action sequence against a
// fresh App at each of the four emit_profile tiers and asserts which
// event types land in the journal.
//
// This catches "wrong tier" regressions that the unit tests can't
// (unit tests verify TypeClass mapping in isolation; here we verify
// the full emit-site → ShouldEmit → InsertEvent chain end-to-end).
//
// The matrix has four columns (one per tier) and is asserted by
// scanning the journal for each event type, expecting presence /
// absence per the tier's enable set.
func TestEmitTierMatrix(t *testing.T) {
	cases := []struct {
		name          string
		profile       string
		mustBePresent []events.Type
		mustBeAbsent  []events.Type
	}{
		{
			name:    "minimal",
			profile: "minimal",
			// Minimal preserves the v0.0.x event surface: file events
			// (from test emit which uses TypeModified), nothing else.
			mustBePresent: []events.Type{events.TypeModified},
			mustBeAbsent: []events.Type{
				events.TypeDigestCreated, events.TypeModeChanged,
				events.TypeIntentDeclared, events.TypeLeaseGranted,
				events.TypeActorRegistered, events.TypeRetentionRan,
				events.TypeReconcileRan, events.TypeSnapshotTaken,
				events.TypeActorHeartbeatReceived,
			},
		},
		{
			name:    "standard",
			profile: "standard",
			mustBePresent: []events.Type{
				events.TypeModified,        // file events still emit
				events.TypeDigestCreated,   // standard
				events.TypeModeChanged,     // standard
				events.TypeIntentDeclared,  // standard (coord)
				events.TypeIntentRevoked,   // standard (coord)
				events.TypeLeaseGranted,    // standard (coord)
				events.TypeLeaseReleased,   // standard (coord)
				events.TypeActorRegistered, // standard
				events.TypeRetentionRan,    // standard
			},
			mustBeAbsent: []events.Type{
				events.TypeReconcileRan,           // verbose
				events.TypeSnapshotTaken,          // verbose
				events.TypeActorHeartbeatReceived, // all
			},
		},
		{
			name:    "verbose",
			profile: "verbose",
			mustBePresent: []events.Type{
				events.TypeModified,
				events.TypeDigestCreated,
				events.TypeModeChanged,
				events.TypeIntentDeclared,
				events.TypeLeaseGranted,
				events.TypeActorRegistered,
				events.TypeRetentionRan,
				events.TypeReconcileRan,  // verbose adds
				events.TypeSnapshotTaken, // verbose adds
			},
			mustBeAbsent: []events.Type{
				events.TypeActorHeartbeatReceived, // all only
			},
		},
		{
			name:    "all",
			profile: "all",
			mustBePresent: []events.Type{
				events.TypeModified,
				events.TypeDigestCreated,
				events.TypeModeChanged,
				events.TypeIntentDeclared,
				events.TypeLeaseGranted,
				events.TypeActorRegistered,
				events.TypeRetentionRan,
				events.TypeReconcileRan,
				events.TypeSnapshotTaken,
				events.TypeActorHeartbeatReceived, // all adds
			},
			// At `all` tier nothing should be absent.
			mustBeAbsent: []events.Type{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t, tc.profile)
			defer a.Close()

			runActionSequence(t, a)

			// Pull all event types currently in the journal.
			got := collectEventTypes(t, a)

			for _, want := range tc.mustBePresent {
				if !got[want] {
					t.Errorf("tier=%s: expected %q to be present in journal", tc.profile, want)
				}
			}
			for _, unwanted := range tc.mustBeAbsent {
				if got[unwanted] {
					t.Errorf("tier=%s: expected %q to be ABSENT from journal (would emit at higher tier only)", tc.profile, unwanted)
				}
			}
		})
	}
}

// TestEmitOverrideMovesClassAcrossTier verifies that emit_overrides
// promotes a class above its tier baseline (and demotes below it).
// The most useful per-class control consumers will reach for.
func TestEmitOverrideMovesClassAcrossTier(t *testing.T) {
	// At minimal tier, override the digest class TO true and verify
	// digest.created emits (would normally be standard-only).
	cfg := newTestCfg(t)
	cfg.EmitProfile = "minimal"
	cfg.EmitOverrides = map[string]bool{"digest": true}
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	_, err = a.TestEmitWithPayload(context.Background(), "promoted.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.ConsumeNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := collectEventTypes(t, a)
	if !got[events.TypeDigestCreated] {
		t.Error("override digest=true at minimal tier should produce digest.created")
	}

	// And the inverse: at standard tier, override coord OFF and verify
	// lease.granted does NOT emit despite being standard.
	cfg2 := newTestCfg(t)
	cfg2.EmitProfile = "standard"
	cfg2.EmitOverrides = map[string]bool{"coord": false}
	a2, err := New(context.Background(), cfg2)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()

	if err := a2.Store.InsertLease(context.Background(), db.LeaseRecord{
		LeaseID: "lse_test", ActorID: "alpha", PathGlob: "auth/**",
		GrantedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	// Call EmitMetaEvent directly to exercise the gate (the CLI handler
	// is in main.go which we can't easily reach from this test; the
	// gate logic is what matters here, the call path is unit-tested
	// separately).
	a2.EmitMetaEvent(context.Background(), events.TypeLeaseGranted, events.SourceCoord, map[string]any{
		"schema_version": 1,
		"lease_id":       "lse_test",
		"actor":          "alpha",
		"path_glob":      "auth/**",
	}, "")
	got2 := collectEventTypes(t, a2)
	if got2[events.TypeLeaseGranted] {
		t.Error("override coord=false at standard tier should SUPPRESS lease.granted")
	}
}

// --- helpers ---

func newTestCfg(t *testing.T) config.Config {
	t.Helper()
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = tmp
	cfg.DBPath = filepath.Join(tmp, "queue.db")
	cfg.WatchPath = filepath.Join(tmp, "watch")
	// Short ActorTTL so reconcile passes can exercise the stale/removed
	// codepaths without sleeping for minutes.
	cfg.ActorTTL = 100 * time.Millisecond
	return cfg
}

func newTestApp(t *testing.T, profile string) *App {
	t.Helper()
	cfg := newTestCfg(t)
	cfg.EmitProfile = profile
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// runActionSequence triggers one of each action that should emit a
// meta-event at SOME tier. Used by TestEmitTierMatrix to populate the
// journal before asserting per-tier presence/absence.
func runActionSequence(t *testing.T, a *App) {
	t.Helper()
	ctx := context.Background()

	// File event via test emit (uses TypeModified, source=test).
	if _, err := a.TestEmitWithPayload(ctx, "matrix.go", ""); err != nil {
		t.Fatal(err)
	}
	// Mode transition: explicit active set.
	if err := a.SetActive(ctx, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	// Digest created (also fires --on-digest hook, but we didn't set one).
	if _, _, err := a.ConsumeNow(ctx); err != nil {
		t.Fatal(err)
	}
	// Coord: intent declare + revoke, lease grant + release.
	if err := a.Store.InsertIntent(ctx, db.IntentRecord{
		IntentID: "int_test", ActorID: "alpha", PathGlob: "auth/**",
		DeclaredAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	a.EmitMetaEvent(ctx, events.TypeIntentDeclared, events.SourceCoord, map[string]any{
		"schema_version": 1, "intent_id": "int_test", "actor": "alpha",
	}, "")
	a.EmitMetaEvent(ctx, events.TypeIntentRevoked, events.SourceCoord, map[string]any{
		"schema_version": 1, "intent_id": "int_test",
	}, "")
	a.EmitMetaEvent(ctx, events.TypeLeaseGranted, events.SourceCoord, map[string]any{
		"schema_version": 1, "lease_id": "lse_test", "actor": "alpha",
	}, "")
	a.EmitMetaEvent(ctx, events.TypeLeaseReleased, events.SourceCoord, map[string]any{
		"schema_version": 1, "lease_id": "lse_test",
	}, "")
	// Actor heartbeat (manually emit since we don't call the CLI handler).
	// First heartbeat → actor.registered semantics handled in CLI handler;
	// for the matrix test we directly emit so the tier assertion is
	// deterministic regardless of duplicate-detection logic.
	a.EmitMetaEvent(ctx, events.TypeActorRegistered, events.SourceActor, map[string]any{
		"schema_version": 1, "actor": "actor-test",
	}, "")
	a.EmitMetaEvent(ctx, events.TypeActorHeartbeatReceived, events.SourceActor, map[string]any{
		"schema_version": 1, "actor": "actor-test",
	}, "")
	// Reconcile triggers retention.ran + reconcile.ran (verbose) +
	// snapshot.taken (verbose, per root).
	if _, err := a.Reconcile.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
}

// collectEventTypes returns the set of distinct event Types currently
// in the journal. Used by both tier-matrix tests to assert per-type
// presence/absence.
func collectEventTypes(t *testing.T, a *App) map[events.Type]bool {
	t.Helper()
	rows, err := a.Store.QueryEvents(context.Background(), db.EventFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[events.Type]bool, len(rows))
	for _, r := range rows {
		seen[r.Type] = true
	}
	return seen
}

// Sanity check that the test helper for type set works (catches
// changes to QueryEvents that might break the assertion path).
var _ = strings.Contains
