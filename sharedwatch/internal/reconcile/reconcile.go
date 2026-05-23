package reconcile

import (
	"context"
	"fmt"
	"time"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
	"sharedwatch/internal/mode"
	"sharedwatch/internal/watcher"
)

type Service struct {
	Store           db.Adapter
	WatchPath       string
	Recursive       bool
	Window          time.Duration
	IgnorePatterns  []string
	IncludePatterns []string
	HashEnabled     bool
	HashMaxSize     int64
	RetentionDays   int
	ProducerID      string
}

func (s Service) RunNow(ctx context.Context) (int, error) {
	current, err := catalog.BuildSnapshotWithOptions(s.WatchPath, s.Recursive, s.IgnorePatterns, catalog.Options{
		Includes:    s.IncludePatterns,
		HashEnabled: s.HashEnabled,
		HashMaxSize: s.HashMaxSize,
	})
	if err != nil {
		return 0, err
	}
	prev, ok, err := s.Store.LatestSnapshot(ctx, watcher.SnapshotSourceReconciler)
	if err != nil {
		return 0, err
	}
	count := 0
	var evs []events.Event
	if ok {
		evs = watcher.DetectRenames(watcher.DiffSnapshots(prev, current, events.SourceReconciler))
	} else {
		// Cold start: treat every existing file as a freshly-observed create
		// so `sharedwatch reconcile now` on a populated folder produces work
		// instead of silently establishing an invisible baseline. Matches the
		// watcher.ScanAndQueue cold-start behaviour.
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
	}
	for _, e := range evs {
		if err := s.Store.InsertOrCoalesceEvent(ctx, e, s.Window); err != nil {
			return count, err
		}
		count++
	}
	if err := s.Store.SaveSnapshot(ctx, fmt.Sprintf("snp_reconcile_%d", time.Now().UTC().UnixNano()), watcher.SnapshotSourceReconciler, current); err != nil {
		return count, err
	}
	r, err := s.Store.GetRuntime(ctx)
	if err != nil {
		return count, err
	}
	r.LastReconcileRun = time.Now().UTC()
	r.LastSnapshotHash = catalog.SnapshotHash(current)
	if r.Mode == "" {
		r = mode.DefaultRuntime()
		r.LastReconcileRun = time.Now().UTC()
		r.LastSnapshotHash = catalog.SnapshotHash(current)
	}
	if err := s.Store.UpsertRuntime(ctx, r); err != nil {
		return count, err
	}
	days := s.RetentionDays
	if days <= 0 {
		days = 30
	}
	retention := time.Duration(days) * 24 * time.Hour
	_, _ = s.Store.PruneOldProcessedEvents(ctx, retention)
	_, _ = s.Store.PruneArchivedDigests(ctx, retention)
	_, _ = s.Store.PruneOldSnapshots(ctx, watcher.SnapshotSourceReconciler, 5)
	return count, nil
}
