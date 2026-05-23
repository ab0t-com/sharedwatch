package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// (asInt helper used elsewhere; declared in overview_helpers.go)

// Overview is the L1 progressive-disclosure envelope returned by
// `sharedwatch overview`. Designed to fit comfortably under ~500 tokens for a
// busy 24h folder. Every value in the `Drill` map is a literal shell command
// the caller can run to expand a dimension (hypermedia / HATEOAS style).
//
// FormatVersion is the first key emitted in JSON output (see field tags +
// struct field order; Go's encoding/json preserves declared field order).
type Overview struct {
	FormatVersion int               `json:"format_version"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Since         string            `json:"since"`
	Mode          string            `json:"mode"`
	Pending       int               `json:"pending"`
	Failed        int               `json:"failed"`
	EventsInRange int               `json:"events_in_range"`
	ByType        []TypeCount       `json:"by_type"`
	TopActors     []ActorCount      `json:"top_actors"`
	Roots         []RootView        `json:"roots,omitempty"`
	ActiveActors  []ActorView       `json:"active_actors,omitempty"`
	Drill         map[string]string `json:"drill"`
}

type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type ActorCount struct {
	Actor string `json:"actor"`
	Count int    `json:"count"`
}

// ComputeOverview builds the L1 envelope. since defaults to 24h when zero.
// Cached against runtime_state with a 60s TTL so an agent polling overview
// every few seconds doesn't repeatedly aggregate the journal.
//
// The cache is keyed on the `since` parameter — non-default windows bypass
// the cache to keep the cached value canonical for the most common call
// shape (`overview --since 24h`).
func (a *App) ComputeOverview(ctx context.Context, since time.Duration) (Overview, error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	if since == 24*time.Hour {
		if cached, ok, _ := a.loadCachedOverview(ctx); ok {
			return cached, nil
		}
	}
	ov, err := a.buildOverview(ctx, since)
	if err != nil {
		return Overview{}, err
	}
	if since == 24*time.Hour {
		_ = a.saveCachedOverview(ctx, ov)
	}
	return ov, nil
}

func (a *App) buildOverview(ctx context.Context, since time.Duration) (Overview, error) {
	snap, err := a.StatusSnapshot(ctx)
	if err != nil {
		return Overview{}, err
	}
	sinceT := time.Now().UTC().Add(-since)

	byType, err := a.aggregateByType(ctx, sinceT)
	if err != nil {
		return Overview{}, err
	}
	topActors, err := a.aggregateTopActors(ctx, sinceT, 5)
	if err != nil {
		return Overview{}, err
	}
	total := 0
	for _, c := range byType {
		total += c.Count
	}

	ov := Overview{
		FormatVersion: 1,
		GeneratedAt:   time.Now().UTC(),
		Since:         since.String(),
		Mode:          snap.Mode,
		Pending:       snap.Pending,
		Failed:        snap.Failed,
		EventsInRange: total,
		ByType:        byType,
		TopActors:     topActors,
		Roots:         snap.Roots,
	}
	if actors, err := a.ActorsView(ctx); err == nil && len(actors) > 0 {
		ov.ActiveActors = actors
	}
	ov.Drill = buildDrillMap(ov)
	return ov, nil
}

// aggregateByType walks the since-bounded event set via the structured query
// path (EventFilter honours the timestamp bound and accepts a limit). The cap
// keeps aggregation O(n) but bounded — busy folders with > 5k events in the
// window settle for an approximation rather than a hot-path full-table scan.
//
// Implementation note: we deliberately do NOT use RawSQL with `?` placeholders
// here — RawSQL is a fixed string-only signature on the Adapter (it forbids
// bound params as part of its read-only tripwire). Going through QueryEvents
// keeps us safe and consistent with the rest of the agent surface.
func (a *App) aggregateByType(ctx context.Context, since time.Time) ([]TypeCount, error) {
	const cap = 5000
	evs, err := a.Store.QueryEvents(ctx, queryEventsFilterForOverview(since, cap))
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, e := range evs {
		counts[string(e.Type)]++
	}
	out := make([]TypeCount, 0, len(counts))
	for t, c := range counts {
		out = append(out, TypeCount{Type: t, Count: c})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type < out[j].Type
	})
	return out, nil
}

func (a *App) aggregateTopActors(ctx context.Context, since time.Time, limit int) ([]ActorCount, error) {
	const cap = 5000
	evs, err := a.Store.QueryEvents(ctx, queryEventsFilterForOverview(since, cap))
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, e := range evs {
		actor := extractActorJSON(e.PayloadJSON)
		if actor == "" {
			continue
		}
		counts[actor]++
	}
	out := make([]ActorCount, 0, len(counts))
	for a, c := range counts {
		out = append(out, ActorCount{Actor: a, Count: c})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Actor < out[j].Actor
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func buildDrillMap(ov Overview) map[string]string {
	d := map[string]string{
		"events_recent":  "sharedwatch events list --since 1h --format jsonl",
		"events_failed":  "sharedwatch events list --status failed --format jsonl",
		"events_by_type": "sharedwatch sql \"SELECT type, COUNT(*) FROM events GROUP BY type\"",
	}
	for _, r := range ov.Roots {
		d["root:"+r.Label] = fmt.Sprintf("sharedwatch events list --root %s --since 1h --format jsonl", r.Label)
	}
	for _, ac := range ov.TopActors {
		d["actor:"+ac.Actor] = fmt.Sprintf("sharedwatch events list --payload-key actor --payload-value %s --since 1h --format jsonl", ac.Actor)
	}
	return d
}

const overviewCacheKey = "overview_v1_24h"
const overviewCacheTTL = 60 * time.Second

type cachedOverviewEnvelope struct {
	StoredAt time.Time `json:"stored_at"`
	Overview Overview  `json:"overview"`
}

func (a *App) loadCachedOverview(ctx context.Context) (Overview, bool, error) {
	raw, ok, err := a.Store.GetRuntimeJSON(ctx, overviewCacheKey)
	if err != nil || !ok {
		return Overview{}, false, err
	}
	var env cachedOverviewEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return Overview{}, false, nil // treat as cache miss on corruption
	}
	if time.Since(env.StoredAt) > overviewCacheTTL {
		return Overview{}, false, nil
	}
	return env.Overview, true, nil
}

func (a *App) saveCachedOverview(ctx context.Context, ov Overview) error {
	env := cachedOverviewEnvelope{StoredAt: time.Now().UTC(), Overview: ov}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return a.Store.UpsertRuntimeJSON(ctx, overviewCacheKey, string(b))
}
