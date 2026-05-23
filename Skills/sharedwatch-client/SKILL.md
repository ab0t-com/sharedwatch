---
name: sharedwatch-client
description: Use sharedwatch today as an AI agent — query a local filesystem event journal, follow named cursors across calls, project event columns, escape to raw read-only SQL, and identify your own writes via payload_json so peer agents can find them. Use when (1) the working directory contains a sharedwatch DB (look for $XDG_DATA_HOME/sharedwatch/queue.db, ./queue.db, or a `sharedwatch` binary on PATH or under ./sharedwatch/.bin/), (2) the user mentions sharedwatch, watched folders, shared-drive activity, or wants situation awareness of recent file changes, (3) coordinating with peer agents in a shared workspace and needing to see what they did, (4) needing "what's new since I last checked" with a stable cursor, or (5) publishing work for other agents to discover through the journal.
---

# sharedwatch-client (current-state operating skill)

Operate as an AI agent against the sharedwatch instance that exists on the local host today. The journal is local-only, pull-based, durable across restarts, and shared across many readers. Query it deliberately, identify your own writes via convention, and stay within the rate ceilings.

This skill describes how to use sharedwatch **as it ships today** (around v0.7.x). For the future-state vocabulary (multi-folder `--root` flags, `overview` / `events stats` endpoints, first-class `--actor` flags, actor registry, leases), use the `sharedwatch-client-future` skill instead.

## Mental model

- An **event** is one observed file change with a unique id and a status lifecycle (`pending → processing → processed | failed | suppressed`). Types: `file.created`, `file.modified`, `file.deleted`, `file.renamed`.
- A **digest** is a window of events rolled into prose. Digests are for humans; agents almost always want raw events.
- A **cursor** is a named server-side bookmark. Use cursors instead of `--since` for repeat queries — they advance atomically across calls.
- **You identify yourself** by writing structured JSON into `payload_json` and (optionally) setting a stable `producer_id`. Peers find your work by filtering on those fields.

## First action — confirm sharedwatch is present

Before any query, confirm sharedwatch is actually installed:

```bash
command -v sharedwatch >/dev/null 2>&1 \
  || test -x ./sharedwatch/.bin/sharedwatch \
  || test -x ./sharedwatch \
  || echo "sharedwatch not found — ask the user where it is"
```

If absent, stop and ask. Never invent a path.

## Five commands to memorize

These five cover ~95% of agent needs.

```bash
# 1. Where are we? — one-shot situation snapshot
sharedwatch status --json

# 2. The journal — your most common query.
#    The CLI exposes the timestamp column as `created_at`. (Internally
#    the DB has both `created_at` and `observed_at`; `events list`
#    surfaces the server-insert time as `created_at`.)
sharedwatch events list --since 1h \
  --fields id,type,rel_path,producer_id,created_at \
  --format jsonl

# 3. "What's new since I last looked?" — idempotent across calls
sharedwatch events list --cursor-name <your-actor-id> \
  --limit 50 --format jsonl

# 4. Self-discovery — run ONCE per session, cache the result
sharedwatch schema --format json

# 5. Arbitrary aggregation — read-only by default
sharedwatch sql "SELECT type, COUNT(*) FROM events
                 WHERE created_at > datetime('now','-1 day')
                 GROUP BY type"
```

Full CLI surface lives in `references/commands.md`. Copy-paste SQL recipes in `references/sql-recipes.md`.

## Identify yourself in events

There is **no first-class `--actor` flag yet** (it is on the roadmap — see `sharedwatch-client-future`). Today, use one of three approaches:

**a. Run sharedwatch with a stable `producer_id`** so the column is meaningful:
```bash
# Sets events.producer_id for all events emitted during this run.
# The flag is `--producer` (singular), set on the root command before the subcommand.
sharedwatch --producer "claude-coordinator-1" run
```
The default is `<hostname>:<pid>`, which rarely identifies an agent.

**b. Tag synthetic events with structured `payload_json`**:
```bash
sharedwatch test emit foo.md --payload '{
  "schema_version": 1,
  "actor": "claude-coordinator-1",
  "session": "sess-2026-05-22-abc",
  "task": "refactor-auth",
  "intent": "extracting JWT logic into separate package",
  "addressee": "human-mike",
  "tags": ["refactor", "auth"]
}'
```

**c. Announce a real file write** (you wrote the file by other means, then publish):
```bash
# 1. Write the file with whatever tool you have
echo "..." > "$WATCH/auth/login.go"

# 2. Wait briefly so the watcher tick picks it up, OR force a reconcile
sharedwatch reconcile now

# 3. The watcher's auto-detected event will have empty payload_json.
#    To add attribution, emit a paired synthetic event referencing it:
sharedwatch test emit auth/login.go --payload '{
  "schema_version": 1,
  "actor": "claude-coordinator-1",
  "session": "sess-abc",
  "task": "refactor-auth",
  "ref_event_id": "evt_..."
}'
```

The canonical `payload_json` v1 keys: `schema_version`, `actor`, `session`, `task`, `intent`, `addressee`, `ref_event_id`, `tags`. Defined in `references/patterns.md`.

## Agentic patterns — compact list

| Pattern | When | Key call |
|---|---|---|
| **initial-orientation** | session start | `status --json` then per-source counts |
| **peer-handoff-check** | before editing a shared file | `events list --path-glob '<target>' --since 1h` |
| **continuous-monitoring** | reacting to a stream (use sparingly) | `events list --cursor-name <stable> --format jsonl` in a slow loop |
| **publish-and-attribute** | after writing a file | `test emit <path> --payload '<json>'` |
| **did-peer-respond** | after a handoff | `events list --payload-key ref_event_id --payload-value <id>` |

Full descriptions and worked examples in `references/patterns.md`.

For quick scripted execution of the initial-orientation flow, run `scripts/orient.sh`. To wrap a shell session with attribution helpers, source `scripts/identify.sh`.

## Hard rules — never break

- **Never poll faster than 1 Hz.** The consumer cadence is 5 s (active) or 10 min (passive). Faster polling burns tokens with no benefit.
- **Never write to the watched folder without identifying yourself.** Unattributed events are noise that other agents must skip.
- **Never use `sharedwatch sql --write` without explicit user approval.** It edits the journal.
- **Never assume `producer_id` is meaningful** unless an agent set it intentionally. Default = `<host>:<pid>`. Prefer `payload_json.actor`.
- **Never depend on `digest summary_text` format.** It is prose for humans and may change.
- **Never enumerate every event.** Always pass `--limit` and `--since`. Narrow with `--path-glob`, `--type`, or other filters.

## Pocket decision tree

| Situation | Command |
|---|---|
| Just started a session | `sharedwatch status --json` (then drill if needed) |
| "What's new since I last looked?" | `events list --cursor-name <stable>` |
| "What happened to this specific file?" | `events list --path-glob '<exact-or-glob>' --since 24h` |
| "What did a peer agent do?" | `events list --payload-key actor --payload-value <peer>` |
| "I'm publishing work for a peer" | `test emit <path> --payload '{...}'` |
| "I need an aggregate that isn't a built-in" | `sharedwatch schema` first, then `sharedwatch sql` |
| "The journal is too big to skim" | start at `status`, narrow with `--path-glob` and `--since` |

## When NOT to query

- You are inside a tight reasoning loop. Do not poll between steps.
- You only need the file's current contents — open the file directly.
- Nothing in the workspace can have changed since your last query.
- You have no peer agents and no coordination need.

## Gotchas — review once before first use

Compact list; full edge-case catalog in `references/gotchas.md`.

- **Coalesce window (5 s)**: two modifies to the same path inside 5 s collapse into one row.
- **Reconcile duplicates**: the 30-min reconcile re-emits anything the watcher missed; you may see the same logical change once from `source=watcher` and once from `source=reconciler`.
- **Cursor races**: two agents sharing a cursor name pass each other's reads. Use `<actor>-<task>` to scope.
- **Filter-change warning**: reusing a cursor with a different filter scope silently skips events. New scope = new cursor name.
- **No content captured**: sharedwatch records *that* a file changed and (optionally) its SHA-256. Read the file for bytes.
- **1-second mtime resolution** on some filesystems. Don't rely on ordering within the same tick.

## Where to look next

| Question | File |
|---|---|
| Full CLI flag reference, every subcommand, exit codes | `references/commands.md` |
| Extended pattern descriptions with worked examples | `references/patterns.md` |
| Copy-paste SQL queries for common aggregations | `references/sql-recipes.md` |
| Edge cases (coalesce, reconcile duplicates, cursor races) | `references/gotchas.md` |
| Failure recovery (`events retry`, `events recover-stuck`, drift) | `references/recovery.md` |
| `config.yaml` keys, defaults, and flag-only fields | `references/config-file.md` |
| Exact JSON/JSONL/CSV/text shapes for streaming and batch parse | `references/output-envelopes.md` |
| Run initial-orientation in one shell call | `scripts/orient.sh` |
| Wrap an attributed `test emit` (set actor/session/task/...) | `scripts/identify.sh` |
