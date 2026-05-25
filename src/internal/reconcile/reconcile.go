package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/config"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
	"sharedwatch/internal/hooks"
	"sharedwatch/internal/mode"
	"sharedwatch/internal/watcher"
)

type Service struct {
	Store db.Adapter
	// WatchPath is the legacy single-root path. Used iff Roots is empty.
	WatchPath string
	// Roots is the multi-root configuration. When non-empty, RunNow iterates
	// every root and reconciles each separately (SW-AGENT-3).
	Roots []config.WatchRoot

	Recursive       bool
	Window          time.Duration
	IgnorePatterns  []string
	IncludePatterns []string
	HashEnabled     bool
	HashMaxSize     int64
	RetentionDays   int
	ProducerID      string
	// PayloadJSON applied to events emitted by the reconciler (recovery events,
	// cold-start creates) when the event lacks its own payload. Mirrors
	// watcher.Service.PayloadJSON.
	PayloadJSON string
	// ActorTTL drives the per-pass actor-registry prune. Stale rows past
	// 2× ActorTTL are deleted. Zero disables the prune (kept for tests).
	ActorTTL time.Duration
	// DataDir is the umbrella data directory; used to locate
	// <DataDir>/hooks/ for sidecar-file pruning during the retention
	// pass. Empty disables sidecar pruning (kept for tests).
	// SW-AGENT-29 Phase 5.
	DataDir string
	// SW-AGENT-30 Phase 4.4-4.6: emit-decision fields. Reconcile owns
	// the emission for reconcile.ran / reconcile.drift_detected /
	// retention.ran / events.failed_threshold / events.stuck_detected /
	// lease.expired / intent.expired / actor.went_stale / actor.removed /
	// snapshot.taken (every event that fires from the reconcile cycle).
	// Empty profile → "standard" fallback per events.ShouldEmit.
	EmitProfile    string
	EmitOverrides  map[string]bool
	EmitThresholds map[string]int
	// Logger is used for emission diagnostics. Nil-safe (helper checks).
	Logger interface {
		Error(msg string, args ...any)
	}
}

func (s Service) effectiveRoots() []config.WatchRoot {
	if len(s.Roots) > 0 {
		return s.Roots
	}
	return []config.WatchRoot{{Label: "", Path: s.WatchPath}}
}

func (s Service) RunNow(ctx context.Context) (int, error) {
	startedAt := time.Now()
	total := 0
	rootCount := 0
	// Per-root drift detection — emit reconcile.drift_detected (standard
	// tier, threshold-gated) for any root whose recovered count exceeds
	// the threshold. Emission happens here (per-root) rather than in
	// postPasses (per-cycle aggregate) so consumers can attribute drift
	// to the specific root that's misbehaving.
	for _, root := range s.effectiveRoots() {
		n, err := s.reconcileRoot(ctx, root)
		if err != nil {
			return total, fmt.Errorf("reconcile root %q (%s): %w", root.Label, root.Path, err)
		}
		if n > 0 {
			s.emitReconcileDriftDetected(ctx, root.Label, n)
		}
		total += n
		rootCount++
	}
	if err := s.postPasses(ctx); err != nil {
		return total, err
	}
	// SW-AGENT-30 Phase 4.4: per-cycle reconcile.ran summary (verbose
	// tier). One emission per RunNow call regardless of root count.
	s.emitReconcileRan(ctx, total, time.Since(startedAt), rootCount)
	return total, nil
}

func (s Service) reconcileRoot(ctx context.Context, root config.WatchRoot) (int, error) {
	current, err := catalog.BuildSnapshotWithOptions(root.Path, s.Recursive, s.IgnorePatterns, catalog.Options{
		Includes:    s.IncludePatterns,
		HashEnabled: s.HashEnabled,
		HashMaxSize: s.HashMaxSize,
	})
	if err != nil {
		return 0, err
	}
	prev, ok, err := s.Store.LatestSnapshot(ctx, watcher.SnapshotSourceReconciler, root.Label)
	if err != nil {
		return 0, err
	}
	count := 0
	var evs []events.Event
	if ok {
		evs = watcher.DetectRenames(watcher.DiffSnapshots(prev, current, events.SourceReconciler))
	} else {
		// Per-root cold start, matching watcher.Service.scanRoot. The first
		// reconcile against a populated root emits creates for everything in
		// it, regardless of whether other roots have been reconciled before.
		for rel, f := range current.Files {
			evs = append(evs, events.Event{
				ID:        watcher.NewEventID(),
				Type:      events.TypeCreated,
				Path:      f.Path,
				RelPath:   rel,
				Timestamp: current.TakenAt,
				Source:    events.SourceReconciler,
				Size:      f.Size,
				MTime:     f.MTime,
				Hash:      f.Hash,
				Status:    events.StatusPending,
			})
		}
	}
	for i := range evs {
		if evs[i].ProducerID == "" {
			evs[i].ProducerID = s.ProducerID
		}
		if evs[i].PayloadJSON == "" && s.PayloadJSON != "" {
			evs[i].PayloadJSON = s.PayloadJSON
		}
		if evs[i].WatchRoot == "" {
			evs[i].WatchRoot = root.Label
		}
	}
	leases, _ := s.Store.ListLeases(ctx, db.LeaseFilter{})
	for _, e := range evs {
		watcher.EmitLeaseAdvisoryIfMismatch(e, leases)
		// SW-AGENT-30 Phase 4.8: mirror the watcher's journal-side
		// emission so reconciler-source events also produce
		// lease.violated meta-events. Both paths use the same
		// detection (watcher.LeaseViolationsFor) for consistency.
		s.emitLeaseViolations(ctx, e, leases)
		if err := s.Store.InsertOrCoalesceEvent(ctx, e, s.Window); err != nil {
			return count, err
		}
		count++
	}
	snapshotStart := time.Now()
	if err := s.Store.SaveSnapshot(ctx, fmt.Sprintf("snp_reconcile_%s_%d", root.Label, time.Now().UTC().UnixNano()), watcher.SnapshotSourceReconciler, root.Label, current); err != nil {
		return count, err
	}
	// SW-AGENT-30 Phase 4.10: snapshot.taken (verbose tier). Per-root,
	// per-reconcile-pass. Useful for ops dashboards tracking reconcile
	// cost; mostly low-signal in normal operation.
	s.emitSnapshotTaken(ctx, root.Label, len(current.Files), time.Since(snapshotStart))
	_, _ = s.Store.PruneOldSnapshots(ctx, watcher.SnapshotSourceReconciler, root.Label, 5)
	return count, nil
}

// postPasses runs the once-per-cycle bookkeeping that does not depend on
// per-root state — runtime checkpoint, retention pruning, actor-registry
// prune. Extracted from RunNow so the per-root loop can stay focused on
// snapshot/diff/emit.
//
// SW-AGENT-30 Phase 4.4-4.6: emits per-cycle observability events
// (reconcile.ran, reconcile.drift_detected, retention.ran,
// events.failed_threshold, events.stuck_detected) at the end of the
// pass. Per-pass emission instead of rising-edge (the design doc
// preferred rising-edge but that requires persisted prior-count state;
// per-pass is the simpler v1 — a sustained problem fires once per
// reconcile cycle, which is ~30 min default = manageable noise).
func (s Service) postPasses(ctx context.Context) error {
	startedAt := time.Now().UTC()
	r, err := s.Store.GetRuntime(ctx)
	if err != nil {
		return err
	}
	r.LastReconcileRun = startedAt
	// For multi-root setups LastSnapshotHash is no longer a single deterministic
	// value across all roots; we leave it as the last-seen snapshot hash from
	// whichever root happened to finish last. Single-root semantics unchanged.
	if r.Mode == "" {
		r = mode.DefaultRuntime()
		r.LastReconcileRun = startedAt
	}
	if err := s.Store.UpsertRuntime(ctx, r); err != nil {
		return err
	}
	days := s.RetentionDays
	if days <= 0 {
		days = 30
	}
	retention := time.Duration(days) * 24 * time.Hour
	pruned := map[string]int64{}
	if n, e := s.Store.PruneOldProcessedEvents(ctx, retention); e == nil {
		pruned["events"] = n
	}
	if n, e := s.Store.PruneArchivedDigests(ctx, retention); e == nil {
		pruned["digests"] = n
	}
	if s.ActorTTL > 0 {
		// SW-AGENT-30 Phase 4.9: enumerate stale + about-to-be-removed
		// actors so we can emit one event per state-change. Same
		// list-then-prune pattern as lease.expired (4.7).
		nowCheck := time.Now().UTC()
		if actors, e := s.Store.ListActors(ctx); e == nil {
			for _, a := range actors {
				age := nowCheck.Sub(a.LastHeartbeat)
				switch {
				case age > 2*s.ActorTTL:
					// About to be pruned. Emit actor.removed (standard).
					s.emitActorRemoved(ctx, a, age)
				case age > s.ActorTTL:
					// Stale but not yet expired. Emit actor.went_stale (verbose).
					s.emitActorWentStale(ctx, a, age)
				}
			}
		}
		if n, e := s.Store.PruneStaleActors(ctx, 2*s.ActorTTL); e == nil {
			pruned["actors"] = n
		}
	}
	// SW-AGENT-29 Phase 5: prune hook sidecar files whose digest no
	// longer exists in the DB.
	if s.DataDir != "" {
		sidecarDir := filepath.Join(s.DataDir, "hooks")
		_, _ = hooks.PruneOrphanedSidecars(sidecarDir, func(digestID string) bool {
			_, err := s.Store.GetDigest(ctx, digestID)
			return err == nil
		})
	}
	// SW-AGENT-12: prune expired intents + leases each pass.
	// SW-AGENT-30 Phase 4.7: list-then-prune for the .expired events.
	// We enumerate expired rows first (so we can emit one event per
	// pruned row), then call the existing prune (which deletes the
	// same rows). The list+prune is NOT atomic — a row that expired
	// AND was re-granted between our list and the prune is technically
	// possible but vanishingly rare (would require sub-millisecond
	// timing). Worst case: one extra lease.expired event for a row
	// that was about to get re-granted; consumers should be idempotent.
	now := time.Now().UTC()
	if intents, e := s.Store.ListIntents(ctx, db.IntentFilter{IncludeExpired: true}); e == nil {
		for _, in := range intents {
			if in.ExpiresAt.Before(now) {
				s.emitIntentExpired(ctx, in)
			}
		}
	}
	if leases, e := s.Store.ListLeases(ctx, db.LeaseFilter{IncludeExpired: true}); e == nil {
		for _, l := range leases {
			if l.ExpiresAt.Before(now) {
				s.emitLeaseExpired(ctx, l)
			}
		}
	}
	if n, e := s.Store.PruneExpiredIntents(ctx); e == nil {
		pruned["intents"] = n
	}
	if n, e := s.Store.PruneExpiredLeases(ctx); e == nil {
		pruned["leases"] = n
	}

	// SW-AGENT-30 Phase 4.5: emit retention.ran summary so audit
	// consumers see evidence that retention happened with how-much.
	s.emitRetentionRan(ctx, pruned, time.Since(startedAt), days)

	// SW-AGENT-30 Phase 4.6: failure-surface threshold events. Both
	// emit when the count exceeds the configured threshold; threshold=0
	// disables (semantically meaningful — "I never want this event").
	if failed, e := s.Store.FailedCount(ctx); e == nil {
		s.emitFailedThreshold(ctx, failed)
	}
	if stuck, e := s.Store.StuckCount(ctx, 5*time.Minute); e == nil {
		s.emitStuckDetected(ctx, stuck)
	}

	return nil
}

// shouldEmit is reconcile's local shortcut for events.ShouldEmit. Keeps
// the per-emit call sites tight.
func (s Service) shouldEmit(t events.Type) bool {
	return events.ShouldEmit(t, s.EmitProfile, s.EmitOverrides)
}

// insertMeta wraps the store insert with the standard error-logging.
// Caller has already gated via shouldEmit before constructing the event.
func (s Service) insertMeta(ctx context.Context, e events.Event) {
	if err := s.Store.InsertEvent(ctx, e); err != nil && s.Logger != nil {
		s.Logger.Error("emit failed", "err", err, "type", string(e.Type))
	}
}

// emitReconcileRan writes the verbose-tier per-cycle metadata event.
// SW-AGENT-30 Phase 4.4.
func (s Service) emitReconcileRan(ctx context.Context, totalRecovered int, duration time.Duration, snapshotsTaken int) {
	if !s.shouldEmit(events.TypeReconcileRan) {
		return
	}
	pj, _ := json.Marshal(struct {
		SchemaVersion   int   `json:"schema_version"`
		DurationMS      int64 `json:"duration_ms"`
		EventsRecovered int   `json:"events_recovered"`
		SnapshotsTaken  int   `json:"snapshots_taken"`
	}{1, duration.Milliseconds(), totalRecovered, snapshotsTaken})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeReconcileRan,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceReconciler,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// emitReconcileDriftDetected fires when a single reconcile pass
// recovers more events than the configured threshold — a signal that
// the watcher path is missing events at a concerning rate.
// SW-AGENT-30 Phase 4.4.
func (s Service) emitReconcileDriftDetected(ctx context.Context, watchRoot string, recovered int) {
	threshold := s.EmitThresholds["reconcile_drift"]
	if threshold <= 0 || recovered <= threshold {
		return
	}
	if !s.shouldEmit(events.TypeReconcileDriftDetected) {
		return
	}
	pj, _ := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		WatchRoot     string `json:"watch_root,omitempty"`
		Recovered     int    `json:"recovered"`
		Threshold     int    `json:"threshold"`
	}{1, watchRoot, recovered, threshold})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeReconcileDriftDetected,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceReconciler,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
		WatchRoot:   watchRoot,
	})
}

// emitRetentionRan writes the per-cycle retention summary so compliance/
// audit consumers can prove "data was deleted on schedule." Standard tier.
// SW-AGENT-30 Phase 4.5.
func (s Service) emitRetentionRan(ctx context.Context, pruned map[string]int64, duration time.Duration, retentionDays int) {
	if !s.shouldEmit(events.TypeRetentionRan) {
		return
	}
	pj, _ := json.Marshal(struct {
		SchemaVersion   int   `json:"schema_version"`
		EventsPruned    int64 `json:"events_pruned"`
		DigestsPruned   int64 `json:"digests_pruned"`
		SnapshotsPruned int64 `json:"snapshots_pruned"`
		ActorsPruned    int64 `json:"actors_pruned"`
		IntentsPruned   int64 `json:"intents_pruned"`
		LeasesPruned    int64 `json:"leases_pruned"`
		DurationMS      int64 `json:"duration_ms"`
		RetentionDays   int   `json:"retention_days"`
	}{
		SchemaVersion:   1,
		EventsPruned:    pruned["events"],
		DigestsPruned:   pruned["digests"],
		SnapshotsPruned: pruned["snapshots"], // not tracked by us yet; reads as 0
		ActorsPruned:    pruned["actors"],
		IntentsPruned:   pruned["intents"],
		LeasesPruned:    pruned["leases"],
		DurationMS:      duration.Milliseconds(),
		RetentionDays:   retentionDays,
	})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeRetentionRan,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceRetention,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// emitFailedThreshold fires when the failed-event count exceeds
// emit_thresholds.events_failed. v1 emits per cycle (not rising-edge);
// a sustained failure fires once per ~30-min reconcile cycle.
// SW-AGENT-30 Phase 4.6.
func (s Service) emitFailedThreshold(ctx context.Context, count int) {
	threshold := s.EmitThresholds["events_failed"]
	if threshold <= 0 || count <= threshold {
		return
	}
	if !s.shouldEmit(events.TypeEventsFailedThreshold) {
		return
	}
	pj, _ := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		Count         int `json:"count"`
		Threshold     int `json:"threshold"`
	}{1, count, threshold})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeEventsFailedThreshold,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceDaemon,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// emitStuckDetected fires when count of `processing`-status events
// older than 5 min exceeds emit_thresholds.events_stuck. v1 per-cycle.
// SW-AGENT-30 Phase 4.6.
func (s Service) emitStuckDetected(ctx context.Context, count int) {
	threshold := s.EmitThresholds["events_stuck"]
	if threshold <= 0 || count <= threshold {
		return
	}
	if !s.shouldEmit(events.TypeEventsStuckDetected) {
		return
	}
	pj, _ := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		Count         int `json:"count"`
		Threshold     int `json:"threshold"`
	}{1, count, threshold})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeEventsStuckDetected,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceDaemon,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// emitLeaseViolations is reconcile's mirror of watcher.Service.emitLeaseViolations.
// Both call sites (watcher.scanRoot, reconcile.reconcileRoot) need to
// emit the same journal event when a file change lands inside another
// actor's lease — having matching methods means the violation shape
// is identical regardless of which subsystem detected it.
// SW-AGENT-30 Phase 4.8.
func (s Service) emitLeaseViolations(ctx context.Context, e events.Event, leases []db.LeaseRecord) {
	if !s.shouldEmit(events.TypeLeaseViolated) {
		return
	}
	violators := watcher.LeaseViolationsFor(e, leases)
	if len(violators) == 0 {
		return
	}
	violator := events.ExtractActor(e.PayloadJSON)
	for _, l := range violators {
		payload, err := json.Marshal(map[string]any{
			"schema_version":    1,
			"violator_actor":    violator,
			"lease_actor":       l.ActorID,
			"lease_id":          l.LeaseID,
			"path_glob":         l.PathGlob,
			"observed_event_id": e.ID,
			"lease_expires_at":  l.ExpiresAt,
		})
		if err != nil {
			continue
		}
		s.insertMeta(ctx, events.Event{
			ID:          newID(),
			Type:        events.TypeLeaseViolated,
			Timestamp:   time.Now().UTC(),
			Source:      events.SourceReconciler,
			Status:      events.StatusProcessed,
			PayloadJSON: string(payload),
			WatchRoot:   e.WatchRoot,
			RelPath:     e.RelPath,
		})
	}
}

// emitSnapshotTaken writes snapshot.taken (verbose, ClassSnapshot)
// for each per-root snapshot the reconciler captures. SW-AGENT-30
// Phase 4.10.
func (s Service) emitSnapshotTaken(ctx context.Context, watchRoot string, fileCount int, duration time.Duration) {
	if !s.shouldEmit(events.TypeSnapshotTaken) {
		return
	}
	pj, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"watch_root":     watchRoot,
		"file_count":     fileCount,
		"duration_ms":    duration.Milliseconds(),
	})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeSnapshotTaken,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceReconciler,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
		WatchRoot:   watchRoot,
	})
}

// emitActorRemoved writes actor.removed (standard, ClassActorLifecycle)
// for an actor that's about to be pruned past 2× ActorTTL. SW-AGENT-30
// Phase 4.9.
func (s Service) emitActorRemoved(ctx context.Context, a db.ActorRecord, age time.Duration) {
	if !s.shouldEmit(events.TypeActorRemoved) {
		return
	}
	pj, _ := json.Marshal(map[string]any{
		"schema_version":     1,
		"actor":              a.ActorID,
		"last_heartbeat":     a.LastHeartbeat,
		"removed_after_secs": int64(age.Seconds()),
	})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeActorRemoved,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceActor,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// emitActorWentStale writes actor.went_stale (verbose, ClassActorStale)
// for an actor whose last heartbeat is past ActorTTL but not yet past
// 2× ActorTTL (the prune threshold). SW-AGENT-30 Phase 4.9.
func (s Service) emitActorWentStale(ctx context.Context, a db.ActorRecord, age time.Duration) {
	if !s.shouldEmit(events.TypeActorWentStale) {
		return
	}
	pj, _ := json.Marshal(map[string]any{
		"schema_version":   1,
		"actor":            a.ActorID,
		"last_heartbeat":   a.LastHeartbeat,
		"stale_since_secs": int64(age.Seconds()),
	})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeActorWentStale,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceActor,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// emitLeaseExpired writes lease.expired (standard, ClassCoord) for a
// lease that's about to be pruned. SW-AGENT-30 Phase 4.7.
func (s Service) emitLeaseExpired(ctx context.Context, l db.LeaseRecord) {
	if !s.shouldEmit(events.TypeLeaseExpired) {
		return
	}
	pj, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"lease_id":       l.LeaseID,
		"actor":          l.ActorID,
		"path_glob":      l.PathGlob,
	})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeLeaseExpired,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceCoord,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// emitIntentExpired writes intent.expired (standard, ClassCoord) for
// an intent that's about to be pruned. SW-AGENT-30 Phase 4.7.
func (s Service) emitIntentExpired(ctx context.Context, in db.IntentRecord) {
	if !s.shouldEmit(events.TypeIntentExpired) {
		return
	}
	pj, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"intent_id":      in.IntentID,
		"actor":          in.ActorID,
		"reason":         "expired",
	})
	s.insertMeta(ctx, events.Event{
		ID:          newID(),
		Type:        events.TypeIntentExpired,
		Timestamp:   time.Now().UTC(),
		Source:      events.SourceCoord,
		Status:      events.StatusProcessed,
		PayloadJSON: string(pj),
	})
}

// newID returns a fresh meta-event id. Local helper — duplicated from
// app.go's newMetaEventID rather than imported to keep the reconcile
// package free of an app-package dependency (would be circular). Same
// shape as everywhere else: "evt_" + 16 hex chars.
func newID() string {
	var b [8]byte
	if _, err := osReadRand(b[:]); err != nil {
		return fmt.Sprintf("evt_%016x", time.Now().UnixNano())
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i, by := range b {
		out[i*2] = hex[by>>4]
		out[i*2+1] = hex[by&0x0f]
	}
	return "evt_" + string(out)
}

var osReadRand = func(b []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.ReadFull(f, b)
}
