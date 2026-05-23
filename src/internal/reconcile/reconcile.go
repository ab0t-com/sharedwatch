package reconcile

import (
	"context"
	"fmt"
	"time"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/config"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
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
}

func (s Service) effectiveRoots() []config.WatchRoot {
	if len(s.Roots) > 0 {
		return s.Roots
	}
	return []config.WatchRoot{{Label: "", Path: s.WatchPath}}
}

func (s Service) RunNow(ctx context.Context) (int, error) {
	total := 0
	for _, root := range s.effectiveRoots() {
		n, err := s.reconcileRoot(ctx, root)
		if err != nil {
			return total, fmt.Errorf("reconcile root %q (%s): %w", root.Label, root.Path, err)
		}
		total += n
	}
	if err := s.postPasses(ctx); err != nil {
		return total, err
	}
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
		if err := s.Store.InsertOrCoalesceEvent(ctx, e, s.Window); err != nil {
			return count, err
		}
		count++
	}
	if err := s.Store.SaveSnapshot(ctx, fmt.Sprintf("snp_reconcile_%s_%d", root.Label, time.Now().UTC().UnixNano()), watcher.SnapshotSourceReconciler, root.Label, current); err != nil {
		return count, err
	}
	_, _ = s.Store.PruneOldSnapshots(ctx, watcher.SnapshotSourceReconciler, root.Label, 5)
	return count, nil
}

// postPasses runs the once-per-cycle bookkeeping that does not depend on
// per-root state — runtime checkpoint, retention pruning, actor-registry
// prune. Extracted from RunNow so the per-root loop can stay focused on
// snapshot/diff/emit.
func (s Service) postPasses(ctx context.Context) error {
	r, err := s.Store.GetRuntime(ctx)
	if err != nil {
		return err
	}
	r.LastReconcileRun = time.Now().UTC()
	// For multi-root setups LastSnapshotHash is no longer a single deterministic
	// value across all roots; we leave it as the last-seen snapshot hash from
	// whichever root happened to finish last. Single-root semantics unchanged.
	if r.Mode == "" {
		r = mode.DefaultRuntime()
		r.LastReconcileRun = time.Now().UTC()
	}
	if err := s.Store.UpsertRuntime(ctx, r); err != nil {
		return err
	}
	days := s.RetentionDays
	if days <= 0 {
		days = 30
	}
	retention := time.Duration(days) * 24 * time.Hour
	_, _ = s.Store.PruneOldProcessedEvents(ctx, retention)
	_, _ = s.Store.PruneArchivedDigests(ctx, retention)
	if s.ActorTTL > 0 {
		_, _ = s.Store.PruneStaleActors(ctx, 2*s.ActorTTL)
	}
	// SW-AGENT-12: prune expired intents + leases each pass. Each row's own
	// expires_at acts as the threshold, so no per-row TTL bookkeeping needed.
	_, _ = s.Store.PruneExpiredIntents(ctx)
	_, _ = s.Store.PruneExpiredLeases(ctx)
	return nil
}
