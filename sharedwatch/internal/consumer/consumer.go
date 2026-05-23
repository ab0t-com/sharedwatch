package consumer

import (
	"context"
	"fmt"
	"time"

	"sharedwatch/internal/db"
	"sharedwatch/internal/digest"
	"sharedwatch/internal/events"
)

type Service struct {
	Store db.Adapter
}

func (s Service) ConsumePending(ctx context.Context, mode string, batchSize int) (digest.Digest, bool, error) {
	evs, err := s.Store.ClaimPendingEvents(ctx, batchSize)
	if err != nil {
		return digest.Digest{}, false, err
	}
	if len(evs) == 0 {
		return digest.Digest{}, false, nil
	}
	summary := digest.RenderHumanSummary(evs)
	d := digest.Digest{
		ID:          fmt.Sprintf("dgs_%d", time.Now().UTC().UnixNano()),
		CreatedAt:   time.Now().UTC(),
		WindowStart: evs[0].Timestamp,
		WindowEnd:   evs[len(evs)-1].Timestamp,
		Mode:        mode,
		EventCount:  len(evs),
		Summary:     summary,
		Status:      digest.StatusPending,
	}
	if err := s.Store.InsertDigest(ctx, d); err != nil {
		_ = s.Store.MarkEventsFailed(ctx, ids(evs))
		return digest.Digest{}, false, err
	}
	if err := s.Store.MarkEventsProcessed(ctx, ids(evs)); err != nil {
		return digest.Digest{}, false, err
	}
	return d, true, nil
}

func ids(evs []events.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.ID)
	}
	return out
}
