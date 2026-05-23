# Progressive disclosure — levels, drill hints, compression

How to navigate a busy journal token-efficiently using the L1/L2/L3 zoom flow.

## Contents
1. The six levels at a glance
2. L1 — `overview`
3. L2 — `events stats`
4. L3 / L4 / L5 — `events list` and `digest show`
5. L6 — content (not yet)
6. The `drill` hint contract
7. Compression strategies
8. When to drill, when to stop

## 1. The six levels at a glance

| Level | What | Endpoint | Token cost (busy 24h) |
|---|---|---|---|
| L1 | Counters per dimension | `overview` | ~100 |
| L2 | Top-N within a dimension | `events stats` | ~400 |
| L3 | Prose digest of a window | `digest show <id>` | ~600 |
| L4 | One row per event, minimal columns | `events list --fields id,type,rel_path,actor,ts` | ~50/event |
| L5 | One row per event, all columns + payload | `events list --fields '*'` | ~200/event |
| L6 | File contents at the time of an event | `content show <event_id>` *(future, requires blob store)* | depends |

Move **up** when the picture is too noisy; **down** when you need to act.

## 2. L1 — `overview`

```bash
sharedwatch overview --format json
```

Returns:
```json
{
  "schema_version": 1,
  "mode": "passive",
  "since": "24h",
  "roots": [
    {"label": "auth",    "pending": 3, "last_event_at": "...", "churn": 142},
    {"label": "billing", "pending": 0, "last_event_at": "...", "churn": 9}
  ],
  "total_events_24h": 412,
  "active_actors": ["claude-1", "claude-2", "human-mike"],
  "drill": {
    "auth":     "sharedwatch events stats --root auth",
    "billing":  "sharedwatch events stats --root billing",
    "by_actor": "sharedwatch sql \"SELECT actor, COUNT(*) FROM events WHERE created_at > datetime('now','-1 day') GROUP BY actor\""
  }
}
```

Use as the first call in every agent session. Decide from this output whether you need to drill.

## 3. L2 — `events stats`

```bash
sharedwatch events stats --root auth --since 24h --format json
```

Returns:
```json
{
  "schema_version": 1,
  "root": "auth",
  "window": {"start": "...", "end": "..."},
  "by_type": {"file.modified": 87, "file.created": 31, "file.deleted": 24},
  "by_actor": {"claude-1": 102, "claude-2": 28, "human-mike": 12},
  "top_paths": [
    {"path": "auth/login.go",   "events": 14, "last_at": "...", "actors": ["claude-1", "claude-2"]},
    {"path": "auth/oauth.go",   "events":  9, "last_at": "...", "actors": ["claude-1"]}
  ],
  "hourly": [{"hour": "2026-05-22T13:00", "events": 14}, ...],
  "drill": {
    "by_path":    "sharedwatch events list --root auth --path-glob '<P>' --since 24h",
    "by_actor":   "sharedwatch events list --root auth --payload-key actor --payload-value <X>",
    "by_type":    "sharedwatch events list --root auth --type <T>"
  }
}
```

`events stats --root <X>` requires `--root`. Forces the agent to pick scope — the natural progressive-disclosure motion. If you really want everything, use `overview` (L1).

## 4. L3 / L4 / L5

L3 — `digest show <id>` — prose, for humans or rough framing. Agents rarely use.

L4 — minimal projection:
```bash
sharedwatch events list \
  --root auth --since 1h \
  --fields id,type,rel_path,actor,created_at \
  --format jsonl
```

L5 — full event:
```bash
sharedwatch events list \
  --root auth --since 1h \
  --fields '*' \
  --format jsonl
```

L5 is expensive; only when you need the full `payload_json` for every event (e.g., cross-referencing `ref_event_id`s).

## 5. L6 — content (future)

```bash
sharedwatch content show <event_id>
```

Returns the file content at the time of that event. Requires a content blob store (not in v1). Until shipped, read the file directly; if it has changed since the event, fall back to the `content_hash` to verify the version.

## 6. The `drill` hint contract

Every L1/L2 output includes a `drill` map. Keys are dimensions; values are *exact commands* the agent should run to expand that dimension. Two rules:

1. **Treat `drill` values as the source of truth for CLI grammar.** Don't memorize flags; copy the command.
2. **Substitute `<P>`, `<X>`, `<T>` placeholders** with the actual value before running. The hint shows the shape; you fill in the specific.

The `drill` mechanism is the highest-leverage idea in the disclosure design: it eliminates a class of "agent doesn't know how to ask" bugs.

## 7. Compression strategies

Aggregate outputs apply these automatically:

- **Group consecutive modifies on the same path.** Instead of 14 lines of `modified auth/login.go`, render `modified auth/login.go (×14, last at 14:32)`.
- **Suppress quiet ranges.** `[]` becomes `quiet 09:00–12:00 (3h)`.
- **Use semantic tags.** Two events both tagged `["refactor"]` render once with `(refactor ×2)`.
- **Path prefix factoring.** `auth/login.go`, `auth/oauth.go`, `auth/jwt.go` render as `auth/{login.go, oauth.go, jwt.go}`.
- **Hash-stable elision.** If `content_hash` is unchanged between two events on the same path, label `(hash unchanged)` rather than re-listing.

These are renderer concerns. You see the compressed form; the underlying data isn't lost — the `drill` hint takes you to the un-compressed source.

## 8. When to drill, when to stop

Drill when:
- L1 shows a root with non-zero pending or recent activity related to your task.
- L2 shows a `top_paths` entry that matches your target file/glob.
- The `actor` distribution surprises you (unknown peer is active).
- A handoff event you're waiting for might be hidden in the noise.

Stop drilling when:
- You can name the thing you'd act on. (You have your answer.)
- The aggregate already gives you the answer (e.g., `by_type` shows the activity is irrelevant deletions).
- Token budget for this query is exhausted (default ~2k tokens per query is plenty).

**One round of L1 → L2 → L4 is usually enough.** If you're going L4 → L5 → SQL → SQL, you've probably lost the thread — re-orient at L1.
