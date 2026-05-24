package hints

import "fmt"

// init wires the built-in providers. Adding hints for a new command is
// one Register call here — handlers don't need to know about other
// providers.
func init() {
	Register("status", providerStatus)
	Register("roots", providerRoots)
	Register("digest list", providerDigestList)
	Register("digest show", providerDigestShow)
	Register("events stats", providerEventsStats)
	Register("events list", providerEventsListCursor)
	Register("init", providerInit)
	Register("version", providerVersion)
	Register("overview", providerOverview)
	Register("stop", providerStop)
}

// providerStop suggests restarting the daemon after a successful stop.
// Always emits the same single hint — no state required.
func providerStop(_ Context) []Hint {
	return []Hint{{
		Name:    "restart_daemon",
		Command: "sharedwatch run",
		Reason:  "restart the watcher + consumer + reconcile loop",
	}}
}

// providerStatus suggests the most likely next action based on queue
// state. Multi-root setups gain per-root drill suggestions.
func providerStatus(ctx Context) []Hint {
	var out []Hint
	if ctx.Pending > 0 {
		out = append(out, Hint{
			Name:    "consume_pending",
			Command: "sharedwatch consume",
			Reason:  fmt.Sprintf("process the %d pending event(s) into a digest", ctx.Pending),
		})
	}
	if ctx.Failed > 0 {
		out = append(out, Hint{
			Name:    "drain_failed",
			Command: "sharedwatch events retry",
			Reason:  fmt.Sprintf("requeue the %d failed event(s) for another consume pass", ctx.Failed),
		})
	}
	if ctx.UnreadDigests > 0 {
		out = append(out, Hint{
			Name:    "read_digests",
			Command: "sharedwatch digest list",
			Reason:  fmt.Sprintf("list the %d unread digest(s)", ctx.UnreadDigests),
		})
	}
	for _, r := range ctx.Roots {
		if r.Pending == 0 {
			continue
		}
		out = append(out, Hint{
			Name:    "drill_root_" + r.Label,
			Command: fmt.Sprintf("sharedwatch events list --root %s --since 1h --format jsonl", r.Label),
			Reason:  fmt.Sprintf("drill into root '%s' (%d pending)", r.Label, r.Pending),
		})
	}
	return out
}

// providerRoots emits one drill hint per *labelled* root. Single-root
// setups (empty Label) get a generic "see recent events" hint without the
// invalid `--root ` filter — bare label can't be used as a filter.
func providerRoots(ctx Context) []Hint {
	out := make([]Hint, 0, len(ctx.Roots))
	for _, r := range ctx.Roots {
		if r.Label == "" {
			out = append(out, Hint{
				Name:    "drill_default_root",
				Command: "sharedwatch events list --since 1h --format jsonl",
				Reason:  "recent events in the default watch folder",
			})
			continue
		}
		out = append(out, Hint{
			Name:    "drill_root_" + r.Label,
			Command: fmt.Sprintf("sharedwatch events list --root %s --since 1h --format jsonl", r.Label),
			Reason:  fmt.Sprintf("recent events in root '%s'", r.Label),
		})
	}
	return out
}

// providerDigestList suggests showing the newest 1–2 digests.
func providerDigestList(ctx Context) []Hint {
	out := make([]Hint, 0, 2)
	for i, d := range ctx.RecentDigests {
		if i >= 2 {
			break
		}
		out = append(out, Hint{
			Name:    "show_digest_" + d.ID,
			Command: fmt.Sprintf("sharedwatch digest show %s", d.ID),
			Reason:  fmt.Sprintf("read digest %s (%d events, %s)", d.ID, d.EventCount, d.Status),
		})
	}
	return out
}

// providerDigestShow suggests archiving after the read.
func providerDigestShow(ctx Context) []Hint {
	if ctx.ShownDigestID == "" {
		return nil
	}
	return []Hint{{
		Name:    "archive_digest_" + ctx.ShownDigestID,
		Command: fmt.Sprintf("sharedwatch digest archive %s", ctx.ShownDigestID),
		Reason:  "mark this digest archived once you've acted on it",
	}}
}

// providerEventsStats narrows queries by the observed top type / actor.
func providerEventsStats(ctx Context) []Hint {
	if ctx.StatsRoot == "" {
		return nil
	}
	var out []Hint
	if ctx.StatsTopType != "" {
		out = append(out, Hint{
			Name:    "narrow_by_top_type",
			Command: fmt.Sprintf("sharedwatch events list --root %s --type %s --format jsonl", ctx.StatsRoot, ctx.StatsTopType),
			Reason:  fmt.Sprintf("show events of the most-common type ('%s') in root '%s'", ctx.StatsTopType, ctx.StatsRoot),
		})
	}
	if ctx.TopActor != "" {
		out = append(out, Hint{
			Name:    "narrow_by_top_actor",
			Command: fmt.Sprintf("sharedwatch events list --root %s --payload-key actor --payload-value %s --format jsonl", ctx.StatsRoot, ctx.TopActor),
			Reason:  fmt.Sprintf("show events from the most-active actor ('%s') in root '%s'", ctx.TopActor, ctx.StatsRoot),
		})
	}
	return out
}

// providerEventsListCursor suggests follow-ups for cursor-mode reads.
func providerEventsListCursor(ctx Context) []Hint {
	if !ctx.HasCursor {
		return nil
	}
	if ctx.CursorReturned == 0 {
		return []Hint{{
			Name:    "check_status",
			Command: "sharedwatch status",
			Reason:  "no new events since the last cursor read; check daemon state",
		}}
	}
	if ctx.CursorReturned >= 50 {
		return []Hint{{
			Name:    "summarise_via_stats",
			Command: "sharedwatch overview --format json",
			Reason:  "many events returned; consider summarising before drilling further",
		}}
	}
	return nil
}

// providerInit suggests starting the watcher with the resolved path.
func providerInit(ctx Context) []Hint {
	if ctx.WatchPath == "" {
		return []Hint{{
			Name:    "start_watcher",
			Command: "sharedwatch run",
			Reason:  "start the watcher + consumer + reconcile loop",
		}}
	}
	return []Hint{{
		Name:    "start_watcher",
		Command: fmt.Sprintf("sharedwatch --watch-path %s run", ctx.WatchPath),
		Reason:  "start the watcher with the path you just initialised",
	}}
}

// providerVersion suggests update --apply only when a newer release exists.
func providerVersion(ctx Context) []Hint {
	if !ctx.NewerExists {
		return nil
	}
	return []Hint{{
		Name:    "apply_update",
		Command: "sharedwatch update --apply",
		Reason:  fmt.Sprintf("a newer release (%s) is available", ctx.Latest),
	}}
}

// providerOverview generalises the drill map previously inlined in
// internal/app/overview.go. It generates a stable core (by-type / recent /
// failed) and, when top actors / types are populated, per-actor /
// per-type narrowing commands.
func providerOverview(ctx Context) []Hint {
	out := []Hint{
		{
			Name:    "events_recent",
			Command: "sharedwatch events list --since 1h --format jsonl",
			Reason:  "all events in the last hour, JSONL for piping",
		},
		{
			Name:    "events_by_type",
			Command: `sharedwatch sql "SELECT type, COUNT(*) FROM events GROUP BY type"`,
			Reason:  "histogram of event types across the journal",
		},
	}
	if ctx.Failed > 0 {
		out = append(out, Hint{
			Name:    "events_failed",
			Command: "sharedwatch events list --status failed --format jsonl",
			Reason:  fmt.Sprintf("inspect the %d failed event(s)", ctx.Failed),
		})
	}
	for _, r := range ctx.Roots {
		out = append(out, Hint{
			Name:    "root_" + r.Label,
			Command: fmt.Sprintf("sharedwatch events list --root %s --since 1h --format jsonl", r.Label),
			Reason:  fmt.Sprintf("recent events in root '%s'", r.Label),
		})
	}
	for _, actor := range ctx.TopActors {
		if actor == "" {
			continue
		}
		out = append(out, Hint{
			Name:    "actor_" + actor,
			Command: fmt.Sprintf("sharedwatch events list --payload-key actor --payload-value %s --since 1h --format jsonl", actor),
			Reason:  fmt.Sprintf("recent events attributed to '%s'", actor),
		})
	}
	for _, t := range ctx.TopTypes {
		if t == "" {
			continue
		}
		out = append(out, Hint{
			Name:    "type_" + t,
			Command: fmt.Sprintf("sharedwatch events list --type %s --since 1h --format jsonl", t),
			Reason:  fmt.Sprintf("recent events of type '%s'", t),
		})
	}
	return out
}
