package watcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/config"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
)

// ErrInvalidRelPath is returned by EmitSynthetic when the supplied relpath
// would escape the watch root, is absolute, or is empty.
var ErrInvalidRelPath = fmt.Errorf("invalid relpath")

type Service struct {
	Store db.Adapter
	// WatchPath is the legacy single-root path. Used iff Roots is empty
	// (label "" = single-root sentinel; events emitted with watch_root="").
	WatchPath string
	// Roots is the multi-root configuration. When non-empty, ScanAndQueue
	// iterates every root and tags emitted events with that root's Label.
	// See SW-AGENT-3.
	Roots []config.WatchRoot

	Recursive       bool
	CoalesceWindow  time.Duration
	IgnorePatterns  []string
	IncludePatterns []string
	HashEnabled     bool
	HashMaxSize     int64
	ProducerID      string
	// PayloadJSON is the default payload_json applied to every event produced
	// by the watcher (auto-detected diffs, cold-start creates) when the event
	// does not already carry a payload. Synthetic emits via
	// EmitSyntheticWithPayload still win when they pass an explicit payload.
	PayloadJSON string
}

// effectiveRoots returns the per-iteration roots used by ScanAndQueue. When
// Roots is empty, falls back to a one-element slice with Label="" wrapping
// WatchPath — preserves the legacy single-root call shape.
func (s Service) effectiveRoots() []config.WatchRoot {
	if len(s.Roots) > 0 {
		return s.Roots
	}
	return []config.WatchRoot{{Label: "", Path: s.WatchPath}}
}

func (s Service) EmitSynthetic(ctx context.Context, relPath string, typ events.Type, source events.Source) (events.Event, error) {
	return s.EmitSyntheticWithPayload(ctx, relPath, typ, source, "")
}

// EmitSyntheticWithPayload behaves like EmitSynthetic but lets callers attach
// arbitrary JSON metadata. payloadJSON must already be valid JSON (or empty,
// in which case the service's default PayloadJSON is used; if that is also
// empty, a `{"synthetic":true,"path":...}` marker is used so the event is
// distinguishable from real watcher activity).
//
// In multi-root mode the synthetic event is tagged with the FIRST configured
// root's label (a sensible default for testing). A future `test emit --root
// <label>` flag will allow explicit targeting.
func (s Service) EmitSyntheticWithPayload(ctx context.Context, relPath string, typ events.Type, source events.Source, payloadJSON string) (events.Event, error) {
	e, _, err := s.EmitSyntheticWithPayloadResult(ctx, relPath, typ, source, payloadJSON)
	return e, err
}

// EmitSyntheticWithPayloadResult behaves like EmitSyntheticWithPayload but
// additionally returns the id of any prior event the new emit coalesced
// into (empty if a fresh row was inserted). Test/CLI callers use this so
// they can surface "coalesced into <id>" instead of advertising a phantom
// id for a row that never landed.
func (s Service) EmitSyntheticWithPayloadResult(ctx context.Context, relPath string, typ events.Type, source events.Source, payloadJSON string) (events.Event, string, error) {
	if err := validateRelPath(relPath); err != nil {
		return events.Event{}, "", err
	}
	roots := s.effectiveRoots()
	root := roots[0]
	abs := root.Path + "/" + relPath
	if payloadJSON == "" {
		if s.PayloadJSON != "" {
			payloadJSON = s.PayloadJSON
		} else {
			payloadJSON = fmt.Sprintf(`{"synthetic":true,"path":%q}`, relPath)
		}
	}
	e := events.Event{
		ID:          newID("evt"),
		Type:        typ,
		Path:        abs,
		RelPath:     relPath,
		Timestamp:   time.Now().UTC(),
		Source:      source,
		Status:      events.StatusPending,
		PayloadJSON: payloadJSON,
		ProducerID:  s.ProducerID,
		WatchRoot:   root.Label,
	}
	coalescedInto, err := s.Store.InsertOrCoalesceEventResult(ctx, e, s.CoalesceWindow)
	if err != nil {
		return events.Event{}, "", err
	}
	return e, coalescedInto, nil
}

// ScanAndQueue snapshots every configured root, diffs against the prior
// per-root snapshot, and enqueues resulting events. Cross-root coalesce and
// cross-root rename pairing are forbidden by SW-AGENT-3 — this is enforced
// here by calling DiffSnapshots and DetectRenames once per root with that
// root's prior snapshot, and by stamping each emitted event with the root's
// Label so the DB-layer coalesce key (rel_path, watch_root, actor) keeps
// roots isolated.
//
// Returns the total event count across all roots.
func (s Service) ScanAndQueue(ctx context.Context, source string) (int, error) {
	total := 0
	for _, root := range s.effectiveRoots() {
		n, err := s.scanRoot(ctx, source, root)
		if err != nil {
			return total, fmt.Errorf("scan root %q (%s): %w", root.Label, root.Path, err)
		}
		total += n
	}
	return total, nil
}

func (s Service) scanRoot(ctx context.Context, source string, root config.WatchRoot) (int, error) {
	current, err := catalog.BuildSnapshotWithOptions(root.Path, s.Recursive, s.IgnorePatterns, catalog.Options{
		Includes:    s.IncludePatterns,
		HashEnabled: s.HashEnabled,
		HashMaxSize: s.HashMaxSize,
	})
	if err != nil {
		return 0, err
	}
	prev, ok, err := s.Store.LatestSnapshot(ctx, source, root.Label)
	if err != nil {
		return 0, err
	}
	var evs []events.Event
	if ok {
		evs = DetectRenames(DiffSnapshots(prev, current, events.Source(source)))
	} else {
		// Per-root cold start: each root's first-seen scan emits creates for
		// everything in it, independent of sibling roots' cold-start state.
		for rel, file := range current.Files {
			evs = append(evs, events.Event{
				ID:        newID("evt"),
				Type:      events.TypeCreated,
				Path:      file.Path,
				RelPath:   rel,
				Timestamp: maxTime(current.TakenAt, file.MTime),
				Source:    events.Source(source),
				Size:      file.Size,
				MTime:     file.MTime,
				Hash:      file.Hash,
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
	leases, _ := s.Store.ListLeases(ctx, db.LeaseFilter{}) // active leases only
	for _, e := range evs {
		emitLeaseAdvisoryIfMismatch(e, leases)
		if err := s.Store.InsertOrCoalesceEvent(ctx, e, s.CoalesceWindow); err != nil {
			return 0, err
		}
	}
	if err := s.Store.SaveSnapshot(ctx, newID("snp"), source, root.Label, current); err != nil {
		return 0, err
	}
	_, _ = s.Store.PruneOldSnapshots(ctx, source, root.Label, 5)
	return len(evs), nil
}

// EmitLeaseAdvisoryIfMismatch is the exported helper for callers outside the
// watcher package (notably reconcile.Service.reconcileRoot, which emits its
// own events and runs the same advisory check). Forwards to the internal
// implementation.
func EmitLeaseAdvisoryIfMismatch(e events.Event, leases []db.LeaseRecord) {
	emitLeaseAdvisoryIfMismatch(e, leases)
}

// emitLeaseAdvisoryIfMismatch logs a warn line when the event lands on a path
// covered by an active lease whose actor differs from the event's actor.
// Advisory only — does not block the insert. This is the SW-AGENT-12 S7.9
// hook: cooperative peers see the warning in logs and can decide whether to
// back off; non-cooperative peers are unaffected (the FS write already
// happened by the time the watcher sees it). For hard guarantees, use a
// real concurrency control mechanism.
func emitLeaseAdvisoryIfMismatch(e events.Event, leases []db.LeaseRecord) {
	if len(leases) == 0 {
		return
	}
	actor := events.ExtractActor(e.PayloadJSON)
	for _, l := range leases {
		if l.ActorID == actor {
			continue // owner editing their own leased path is the happy path
		}
		if !db.LeaseGlobMatchesPath(l.PathGlob, e.RelPath) {
			continue
		}
		slog.Warn("event lands on path covered by another actor's lease",
			"event_id", e.ID,
			"rel_path", e.RelPath,
			"watch_root", e.WatchRoot,
			"event_actor", actor,
			"lease_id", l.LeaseID,
			"lease_actor", l.ActorID,
			"lease_path_glob", l.PathGlob,
			"lease_expires_at", l.ExpiresAt.Format(time.RFC3339),
		)
	}
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// NewEventID exposes the event-id generator for other packages (currently
// reconcile, when synthesizing cold-start created events).
func NewEventID() string { return newID("evt") }

func validateRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty", ErrInvalidRelPath)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: absolute paths not allowed (%q)", ErrInvalidRelPath, p)
	}
	cleaned := path.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%w: path escapes watch root (%q)", ErrInvalidRelPath, p)
	}
	return nil
}
