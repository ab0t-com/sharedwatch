package watcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"sharedwatch/internal/catalog"
	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
)

// ErrInvalidRelPath is returned by EmitSynthetic when the supplied relpath
// would escape the watch root, is absolute, or is empty.
var ErrInvalidRelPath = fmt.Errorf("invalid relpath")

type Service struct {
	Store           db.Adapter
	WatchPath       string
	Recursive       bool
	CoalesceWindow  time.Duration
	IgnorePatterns  []string
	IncludePatterns []string
	HashEnabled     bool
	HashMaxSize     int64
	ProducerID      string
}

func (s Service) EmitSynthetic(ctx context.Context, relPath string, typ events.Type, source events.Source) (events.Event, error) {
	return s.EmitSyntheticWithPayload(ctx, relPath, typ, source, "")
}

// EmitSyntheticWithPayload behaves like EmitSynthetic but lets callers attach
// arbitrary JSON metadata. payloadJSON must already be valid JSON (or empty,
// in which case a default `{"synthetic":true,"path":...}` payload is used).
func (s Service) EmitSyntheticWithPayload(ctx context.Context, relPath string, typ events.Type, source events.Source, payloadJSON string) (events.Event, error) {
	if err := validateRelPath(relPath); err != nil {
		return events.Event{}, err
	}
	abs := s.WatchPath + "/" + relPath
	if payloadJSON == "" {
		payloadJSON = fmt.Sprintf(`{"synthetic":true,"path":%q}`, relPath)
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
	}
	if err := s.Store.InsertOrCoalesceEvent(ctx, e, s.CoalesceWindow); err != nil {
		return events.Event{}, err
	}
	return e, nil
}

func (s Service) ScanAndQueue(ctx context.Context, source string) (int, error) {
	current, err := catalog.BuildSnapshotWithOptions(s.WatchPath, s.Recursive, s.IgnorePatterns, catalog.Options{
		Includes:    s.IncludePatterns,
		HashEnabled: s.HashEnabled,
		HashMaxSize: s.HashMaxSize,
	})
	if err != nil {
		return 0, err
	}
	prev, ok, err := s.Store.LatestSnapshot(ctx, source)
	if err != nil {
		return 0, err
	}
	var evs []events.Event
	if ok {
		evs = DetectRenames(DiffSnapshots(prev, current, events.Source(source)))
	} else {
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
	}
	for _, e := range evs {
		if err := s.Store.InsertOrCoalesceEvent(ctx, e, s.CoalesceWindow); err != nil {
			return 0, err
		}
	}
	if err := s.Store.SaveSnapshot(ctx, newID("snp"), source, current); err != nil {
		return 0, err
	}
	_, _ = s.Store.PruneOldSnapshots(ctx, source, 5)
	return len(evs), nil
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
