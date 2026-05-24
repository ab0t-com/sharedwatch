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
#    the DB has both `created_at` and `observed_at` columns; in normal
#    operation both carry the watcher's observation time, set on insert.
#    `events list` projects the `observed_at` value under the label
#    `created_at`. For direct SQL, both columns are addressable.)
sharedwatch events list --since 1h \
  --fields id,type,rel_path,producer_id,created_at \
  --format jsonl

# Keep the binary current (safe: dry-run by default).
sharedwatch update             # check; prints what would change
sharedwatch update --apply     # download + SHA-256 verify + atomic-swap binary

# Smart hints — recommend running with --hints agent (or set
# SHAREDWATCH_HINTS=agent) so every command's output carries a
# `next[]` array of pre-computed follow-up commands. JSON output
# auto-promotes to the agent profile, so for human text invocations
# the flag is the way to opt in.
sharedwatch --hints agent status --json | jq '.next'

# AGENT STARTUP RITUAL (recommended). Export these once per shell
# session and never type --actor / --format / --cursor-name / --hints
# on individual commands again. Resolution: flag > env > config > default.
export SHAREDWATCH_ACTOR=claude-coord-1            # your stable id
export SHAREDWATCH_ACTOR_KIND=ai_agent
export SHAREDWATCH_SESSION="sess-$(date +%Y-%m-%d)-$(uuidgen | head -c8)"
export SHAREDWATCH_FORMAT=jsonl                    # agents want jsonl
export SHAREDWATCH_CURSOR_NAME=claude-coord-1      # cursor mode on by default
export SHAREDWATCH_HINTS=agent                     # rich next[] hints

# Then verify what the binary will actually use:
sharedwatch config show
# (or `sharedwatch config show --json` for machine-readable)

# KNOWN GOTCHAS (surfaced by dogfood scenarios 21–24, v0.0.7).
# Read once, internalise, save yourself the debug loop:
#
# 1. --format must come BEFORE positional args. Go's stdlib flag parser
#    stops at the first non-flag, so `sharedwatch sql "SELECT ..." --format jsonl`
#    silently drops the trailing flag. Use `sharedwatch sql --format jsonl "..."`
#    OR set SHAREDWATCH_FORMAT=jsonl once and forget the flag entirely.
#
# 2. `config show` shows cfg-layer values (config + env), NOT the
#    flag layer. If a config sets `hints: terse` and you run with
#    `--hints agent`, `config show` still prints `terse` even though
#    the flag wins for behaviour. Don't use `config show` to verify
#    what flags are doing — only what config + env are doing.
#
# 3. `sharedwatch update` dry-run doesn't validate that the target
#    version actually exists. `--version v9.9.9` (nonexistent) prints
#    the same DRY RUN plan as a real version; the 404 only fires on
#    --apply. A clean dry-run is NOT proof the target is real.
#
# 4. `--all` on coordination-surface list commands is a TTL-EXPIRY
#    filter, not a soft-delete filter. `lease list --all` and
#    `intent list --all` both surface only entries that lived out
#    their TTL; explicitly released leases and revoked intents are
#    HARD-DELETED, not retained. NEITHER lease nor intent lifecycle
#    emits events to the journal today (verified empirically) —
#    the events journal is for FILE events only. Track coord
#    lifecycle by polling `lease list --json` / `intent list --json`
#    directly.
#
# 5. Lease violation warnings (`slog.Warn` from watcher) fire ONLY on
#    real file changes the watcher picks up, NOT on `test emit`. To
#    exercise the lease path, write a file to the watched dir and let
#    the watcher detect it; `test emit` skips the lease check.
#
# 6. The correct verb is `lease grant <path-glob>` (not `lease acquire`).
#    Older docs may show `acquire`; the binary only accepts grant/release/
#    renew/list.
#
# 7. `intent list` text vs JSON column names DIFFER: text labels read
#    `actor=`/`path=`; the JSON shape uses `actor_id`/`path_glob`
#    (and the wrapping `id` is `intent_id`). When piping to jq, use
#    the JSON names. `intent list --json` also returns a bare ARRAY
#    (or `null` when empty), not an envelope object.
#
# 8. `test emit` for an already-emitted (actor,path) pair within the
#    5s coalesce window returns a NEW evt_id but the underlying row
#    is merged into the prior event. Do not record that id and try
#    to reference it later — `events list` will not surface it. If
#    you need to know whether your emit landed as a fresh row, query
#    by path+actor right after.

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

As of SW-AGENT-7, attribution is first-class via dedicated CLI flags. Three approaches in decreasing convenience:

**a. Pass attribution flags on the root command (preferred for `run`):** every event the watcher and reconciler emit during this lifetime carries the attribution automatically.
```bash
sharedwatch \
  --actor claude-coordinator-1 \
  --session sess-2026-05-23-abc \
  --task refactor-auth \
  --intent "split JWT validation" \
  --tag refactor --tag auth \
  run
```

**b. Pass attribution flags on `test emit` for a one-off synthetic event** (root values still apply; subcommand values override per-field):
```bash
sharedwatch test emit specs/widget.md \
  --actor claude-spec --task new-widget-spec \
  --addressee claude-code \
  --tag spec --tag widget
```

**c. Raw `--payload '<json>'` for any unusual case** (mutually exclusive with attribution flags on the same command):
```bash
sharedwatch test emit auth/login.go --payload '{
  "schema_version": 1,
  "actor": "claude-coordinator-1",
  "session": "sess-abc",
  "task": "refactor-auth",
  "ref_event_id": "evt_..."
}'
```

**Optionally also set `--producer <id>`** so the column appears alongside the payload:
```bash
sharedwatch --producer "claude-coordinator-1" --actor claude-coordinator-1 run
```
The flag is `--producer` (singular). Default is `<hostname>:<pid>`, which rarely identifies an agent.

The canonical `payload_json` v1 keys: `schema_version` (always 1), `actor` (required to populate any others), `actor_kind`, `session`, `task`, `intent`, `addressee`, `ref_event_id`, `tags`. The CLI flag for each is named identically (e.g. `--addressee` → `addressee`, `--ref` → `ref_event_id`). Full schema in `references/patterns.md`.

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
