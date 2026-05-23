package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"sharedwatch/internal/db"
	"sharedwatch/internal/events"
)

// EventsStats is the L2 progressive-disclosure envelope: a focused look at one
// root's recent activity. Required scope (one root) keeps the response
// bounded and forces agents to choose the dimension before drilling — see
// `docs/design/disclosure-attribution-discussion-20260522.md` §1.4.
type EventsStats struct {
	FormatVersion int               `json:"format_version"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Root          string            `json:"root"`
	Window        StatsWindow       `json:"window"`
	ByType        map[string]int    `json:"by_type"`
	ByActor       map[string]int    `json:"by_actor"`
	TopPaths      []PathStat        `json:"top_paths"`
	Hourly        []HourlyBucket    `json:"hourly"`
	Drill         map[string]string `json:"drill"`
}

type StatsWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type PathStat struct {
	Path   string    `json:"path"`
	Events int       `json:"events"`
	LastAt time.Time `json:"last_at"`
	Actors []string  `json:"actors,omitempty"`
}

type HourlyBucket struct {
	Hour   time.Time `json:"hour"`
	Events int       `json:"events"`
}

// ComputeEventsStats produces the L2 envelope. root is required ("" rejected);
// since defaults to 24h when zero.
func (a *App) ComputeEventsStats(ctx context.Context, root string, since time.Duration) (EventsStats, error) {
	if root == "" {
		return EventsStats{}, fmt.Errorf("--root is required for events stats; pass --root <label>. Use `overview` for cross-root counts.")
	}
	if since <= 0 {
		since = 24 * time.Hour
	}
	end := time.Now().UTC()
	start := end.Add(-since)

	evs, err := a.Store.QueryEvents(ctx, db.EventFilter{
		WatchRoots: []string{root},
		Since:      start,
		Limit:      5000, // bounded — see overview.go aggregateByType note
		OrderAsc:   true,
	})
	if err != nil {
		return EventsStats{}, err
	}

	stats := EventsStats{
		FormatVersion: 1,
		GeneratedAt:   end,
		Root:          root,
		Window:        StatsWindow{Start: start, End: end},
		ByType:        map[string]int{},
		ByActor:       map[string]int{},
	}

	pathCounts := map[string]*PathStat{}
	hourlyCounts := map[time.Time]int{}

	for _, e := range evs {
		stats.ByType[string(e.Type)]++
		actor := events.ExtractActor(e.PayloadJSON)
		if actor != "" {
			stats.ByActor[actor]++
		}
		ps, ok := pathCounts[e.RelPath]
		if !ok {
			ps = &PathStat{Path: e.RelPath}
			pathCounts[e.RelPath] = ps
		}
		ps.Events++
		if e.Timestamp.After(ps.LastAt) {
			ps.LastAt = e.Timestamp
		}
		if actor != "" && !containsStr(ps.Actors, actor) {
			ps.Actors = append(ps.Actors, actor)
		}
		hr := e.Timestamp.UTC().Truncate(time.Hour)
		hourlyCounts[hr]++
	}

	// Top paths by event count, ties broken by latest activity, then path.
	stats.TopPaths = make([]PathStat, 0, len(pathCounts))
	for _, ps := range pathCounts {
		stats.TopPaths = append(stats.TopPaths, *ps)
	}
	sort.SliceStable(stats.TopPaths, func(i, j int) bool {
		if stats.TopPaths[i].Events != stats.TopPaths[j].Events {
			return stats.TopPaths[i].Events > stats.TopPaths[j].Events
		}
		if !stats.TopPaths[i].LastAt.Equal(stats.TopPaths[j].LastAt) {
			return stats.TopPaths[i].LastAt.After(stats.TopPaths[j].LastAt)
		}
		return stats.TopPaths[i].Path < stats.TopPaths[j].Path
	})
	if len(stats.TopPaths) > 10 {
		stats.TopPaths = stats.TopPaths[:10]
	}

	// Hourly buckets in chronological order.
	stats.Hourly = make([]HourlyBucket, 0, len(hourlyCounts))
	for hr, n := range hourlyCounts {
		stats.Hourly = append(stats.Hourly, HourlyBucket{Hour: hr, Events: n})
	}
	sort.SliceStable(stats.Hourly, func(i, j int) bool {
		return stats.Hourly[i].Hour.Before(stats.Hourly[j].Hour)
	})

	stats.Drill = buildStatsDrill(stats)
	return stats, nil
}

func buildStatsDrill(s EventsStats) map[string]string {
	d := map[string]string{
		"by_path":  fmt.Sprintf("sharedwatch events list --root %s --path-glob '<PATH>' --since 24h --format jsonl", s.Root),
		"by_actor": fmt.Sprintf("sharedwatch events list --root %s --payload-key actor --payload-value <ACTOR> --since 24h --format jsonl", s.Root),
		"by_type":  fmt.Sprintf("sharedwatch events list --root %s --type <TYPE> --since 24h --format jsonl", s.Root),
	}
	// Concrete drill entries for the top paths the caller will likely want.
	for _, ps := range s.TopPaths {
		d["path:"+ps.Path] = fmt.Sprintf("sharedwatch events list --root %s --path-glob %q --since 24h --format jsonl", s.Root, ps.Path)
	}
	return d
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
