# sharedwatch — multi-agent product discussion

**Date:** 2026-05-22
**Status:** discussion document (not a decision, not a plan)
**Purpose:** think clearly about what the product is, what data we collect, what we're missing for the multi-AI-agent-on-a-shared-folder use case, and what to build next.

This document is grounded in the code as of master (v0.7.3-ish). Where I make a claim about behavior, it comes from a specific file. Where I speculate about future state, I flag it.

Related prior work:
- [`../reports/engineering-report-20260520.md`](../reports/engineering-report-20260520.md) — engineering map
- [`../reports/pmm-report-20260520.md`](../reports/pmm-report-20260520.md) — positioning
- [`../agent/agent-fit-20260520.md`](../agent/agent-fit-20260520.md) — the multi-agent fit analysis
- [`../../tickets/ticket-agent-event-access-20260520_035113.md`](../../tickets/ticket-agent-event-access-20260520_035113.md) — SW-AGENT-1 (events query surface) — landed
- [`../../tickets/ticket-multi-folder-watching-20260520_101921.md`](../../tickets/ticket-multi-folder-watching-20260520_101921.md) — SW-AGENT-3 (multi-folder) — open

---

## 1. What "the passive pull" actually returns

The passive pull is *two distinct surfaces*, and conflating them has been a recurring source of confusion:

### 1a. The **human surface** — `digest`
- `sharedwatch digest list` → newest digest IDs + a one-line summary
- `sharedwatch digest show <id>` → a prose `summary_text` plus a window range
- Rendering today (see `internal/digest/render.go`) is a flat list of `- <type> <rel_path> (<timestamp>)` lines wrapped in a header.
- Digest is a **rolled-up batch**: one digest = many events consumed in one cycle. The batch ceiling is `max_batch_size` (default 100).
- A digest carries no per-event structure — it's prose. Consuming it programmatically means re-parsing strings.

### 1b. The **agent surface** — `events list` / `sql` / `schema`
- `events list` is the structured journal — JSON/JSONL/CSV/text output, server-side cursors, glob/type/source/status/path/payload filters (`internal/db/events_query.go`).
- `sql` is the read-only escape hatch (SELECT/WITH/EXPLAIN allowed; everything else needs `--write`).
- `schema` is `sqlite_master` + `PRAGMA table_info()` exposed as JSON, so an agent can ask "what columns exist?" once and write queries against them forever (`internal/db/schema.go`).
- **This is the surface that matters for AI agents.** Anything we say about multi-agent coordination should assume agents use `events list`, not `digest`.

---

## 2. What data structures are on offer right now

From `internal/db/db.go`'s migration plus the column added by SW-AGENT-5:

### `events` (the journal — load-bearing)
| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | `evt_<16-hex>` |
| `type` | TEXT | `file.created` / `file.modified` / `file.deleted` / `file.renamed` |
| `path` | TEXT | absolute path at observation time |
| `rel_path` | TEXT | path relative to watch root |
| `old_path` | TEXT NULL | populated for renames |
| `source` | TEXT | `watcher` / `reconciler` / `test` |
| `status` | TEXT | `pending` → `processing` → `processed` / `failed` / `suppressed` |
| `retry_count` | INTEGER | bumped on `MarkEventsFailed` |
| `created_at` | TEXT | RFC3339Nano — server insert time |
| `observed_at` | TEXT | RFC3339Nano — when the watcher saw it |
| `processed_at` | TEXT NULL | when consumer marked it processed |
| `file_size` | INTEGER | bytes |
| `mtime` | TEXT NULL | RFC3339Nano filesystem mtime |
| `content_hash` | TEXT NULL | SHA-256 hex, only if `HashEnabled` and size ≤ `HashMaxSize` |
| `coalesced_into` | TEXT NULL | id of the surviving event this one merged into |
| `payload_json` | TEXT | free-form JSON blob, default `{}` |
| `producer_id` | TEXT | `<hostname>:<pid>` by default; user-settable |

Two indexes on `(status, created_at)` and `(rel_path, status, created_at)` plus the producer index. Cursors land on `(created_at, id)` for stable iteration.

### `digests`
- `id`, `created_at`, `window_start`, `window_end`, `mode`, `event_count`, `summary_text`, `status` (`pending` / `read` / `archived`).
- One row per consume cycle. Summary is prose.

### `snapshots`
- Full folder snapshot serialized as JSON (`snapshot_json`), keyed by `(source)`, bounded to the latest 5 per source.
- Each snapshot is `{TakenAt, Files: map[relPath]→{RelPath, Path, Size, MTime, Hash}}`. See `internal/catalog/snapshot.go`.

### `runtime_state`
- KV. Stores the active/passive `mode.Runtime` blob — current mode, last consume time, snapshot hash, etc.

### `cursors`
- `(name PK, created_at_nano, last_id, updated_at)`. Named cursors so each reader has its own bookmark.

### What's discoverable
- `sharedwatch schema --format json` returns the full live DDL plus column metadata. **An agent can self-discover the journal shape.** This is the load-bearing capability that makes everything else composable.

---

## 3. Snapshot granularity — what's actually captured?

The watcher does **not capture file contents.** It captures *attributes that fingerprint the file*:

**Captured per file:**
- relative path, absolute path
- size in bytes
- modification time (kernel mtime, UTC, nanosecond-precision)
- SHA-256 content hash *only* if `HashEnabled` and size ≤ `HashMaxSize` (default 1 MB)

**NOT captured:**
- file contents (no blob store)
- content diffs / patches
- POSIX permissions, owner/group, ACLs
- inode number (so we can't see hard links as the same file)
- symlink target
- directory-level metadata
- xattrs / extended attributes
- access time
- creation time (Linux's btime)

**Snapshot grain:** *whole-folder snapshot*, JSON-serialized, retained as the latest 5. The watcher's polling loop diffs `current_snapshot` against `latest_stored_snapshot` to produce events. The 30-minute reconciler does the same thing as a heartbeat against drift.

**The "duplicates beat misses" doctrine** (from `README.md` operational notes): the system is built to over-emit rather than under-emit. The reconciler re-runs the same diff every 30m so anything the live watcher missed gets caught — at the cost of occasional duplicate events.

---

## 4. The "30-min ping" question — what already exists, what doesn't

You asked about a 30-min ping. **Sharedwatch already does this.** Several cadences:

| Loop | Default | Configurable | What it does |
|---|---|---|---|
| Watcher tick (passive) | 10 min | `passive_interval` | Snapshot + diff + enqueue |
| Watcher tick (active) | 5 sec | `active_interval` | Same, faster |
| Active mode TTL | 30 min | `active_ttl` | Auto-revert to passive after inactivity |
| Reconcile | 30 min | `reconcile_interval` | Drift-catcher; re-diff + enqueue missed events |
| Coalesce window | 5 sec | `coalesce_window` | Burst suppression on same-path events |

**What's already there:**
- The 30-min reconcile **is** a ping — it walks the tree, diffs, and enqueues recovery events.
- You can run `sharedwatch reconcile now` to fire it on-demand.
- Mode flips: `sharedwatch mode active --ttl 30m` to enable fast-cadence; auto-decays.

**What's *not* there (and you may want):**
- A **scheduled status probe** that emits a heartbeat event regardless of whether files changed — useful for an upstream observer to detect "the watcher itself is alive." Currently no liveness event is emitted unless files change.
- A **per-root cadence**. Today, intervals are global. An agent watching `/inbox/from-human/` may want 5s cadence while `/archive/` runs at 1h.
- **Cron-style schedules.** Today, intervals are durations (every N), not crontab expressions. Probably overkill but worth naming.
- A **`--probe`** subcommand that runs one tick + one reconcile + reports timings, for monitoring integrations.

---

## 5. Multi-folder support — does it need a new table?

**Short answer: no new table. Three new columns + four new indexes + per-root correctness invariants.** The detailed plan is already in [`../../tickets/ticket-multi-folder-watching-20260520_101921.md`](../../tickets/ticket-multi-folder-watching-20260520_101921.md) (SW-AGENT-3). Highlights below for the discussion:

### Schema delta (additive, idempotent)
```sql
ALTER TABLE events     ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';
ALTER TABLE digests    ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';
ALTER TABLE snapshots  ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_events_root_created ON events(watch_root, created_at);
CREATE INDEX IF NOT EXISTS idx_events_root_relpath ON events(watch_root, rel_path, status, created_at);
```

### Why no separate `roots` table?
- Label → path is config, not state. Restart-time data; doesn't need durability.
- Adding a registry table buys nothing today and creates a sync-with-config bug surface.
- If we ever want per-root policy (quotas, retention, ignore patterns), then a `roots` table becomes worth it — but that's not v0.8.

### The load-bearing changes are NOT schema, they're invariants
The ticket identifies the real risks:
- **Coalesce must be root-scoped.** Two `README.md`s in different roots must not merge.
- **Rename detection must be root-scoped.** A delete in root A + create in root B with matching size/mtime must not become a phantom rename.
- **Snapshot lookup must be `(source, watch_root)` keyed.** Forgetting this silently uses one root's snapshot for another → whole-tree phantom create/delete cascade.
- **Cold-start emission per root.** First scan of a new root emits `file.created` for everything in it.

### What multi-folder enables
- Coordinator agents watching N workstream folders in one query.
- Multi-inbox agents (`/inbox/from-human/`, `/inbox/from-agent-x/`, `/inbox/from-ci/`).
- Cross-root cursor over a unified event stream.
- Per-root status (`pending`, `last_event_at`) for "which roots are hot right now?"

This is the **single biggest leverage feature** for the multi-AI-agent use case. Nothing else moves the needle as much.

---

## 6. Are we collecting enough information?

For "what changed and when," **yes**. For "what's happening in this shared folder across multiple AI agents," **no** — there are concrete gaps.

### What's well-covered today
- File identity, size, mtime, optional content hash → "did this file change?"
- Event causality (which event coalesced into which) → "is this one logical change or three?"
- Status lifecycle → "has anyone consumed this yet?"
- Reader bookmarks via named cursors → "where was I up to?"
- Producer attribution via `producer_id` → *technically* "who wrote this," but…

### What's under-served
1. **Producer identity is a single string, not a structured actor.**
   `producer_id` defaults to `<hostname>:<pid>` — meaningless when multiple agent processes share a host. An agent that *does* set a meaningful ID still can't express "this change came from sub-task `task-abc` of agent `claude-coordinator-1`." Today this gets squeezed into `payload_json`, which has no schema.
2. **`payload_json` has no contract.**
   It's a free-form blob. Two agents producing events will pick incompatible shapes. Querying it (`PayloadKey/PayloadValue` post-filter in `EventFilter`) only supports flat top-level keys with exact-string match. No documentation says what should go in there.
3. **No content delta.**
   We capture *that* a file changed (and optionally its new hash). We don't capture *how* it changed. An agent that wants to react to "did this file gain a new test case?" must re-read the file.
4. **No intent / addressee / causality.**
   "Why did this file change?" "Was this change intended for agent X to react to?" "What event caused this?" All unrepresented.
5. **No actor liveness / focus.**
   No way for an agent to declare "I'm alive, working on `/auth/`." No way for another agent to ask "is anyone else looking at this folder?"
6. **No leases.**
   Two agents writing the same file produce two `file.modified` events with no coordination. There's no advisory "I'm editing this for the next 5 min, hold off."
7. **No read receipts.**
   Cursors track *one* reader's position. There's no "has agent X seen event N?" for a publishing agent to verify its handoff was consumed.
8. **No content snapshots.**
   Hash + size lets you fingerprint a moment. It doesn't let you replay "what did the file look like at 14:32?"

The first two are cheap to address (schema for `payload_json`, structured actor). The next four reshape the product. The last two are workload-dependent.

---

## 7. What questions can agents answer today?

| Question | Answerable today? | How |
|---|---|---|
| "What changed in the last hour?" | ✅ | `events list --since 1h --format jsonl` |
| "What's new since I last looked?" | ✅ | `events list --cursor-name my-agent` |
| "Which files have the most churn?" | ✅ | `sql "SELECT rel_path, COUNT(*) FROM events GROUP BY rel_path ORDER BY 2 DESC"` |
| "Show me only deletes in `auth/`" | ✅ | `events list --type file.deleted --path-glob 'auth/**'` |
| "Who created this file?" | ⚠️ | only via `producer_id`, which defaults to `<host>:<pid>` and rarely identifies the agent |
| "Has agent X been active?" | ⚠️ | works only if agent X set a stable `producer_id` |
| "Which files did agent X touch?" | ⚠️ | same caveat |
| "Did agent X read the handoff agent Y published?" | ❌ | no read-acks |
| "Are two agents about to clobber each other?" | ❌ | no lease/lock primitive |
| "Show me file contents at the time of event N" | ❌ | hash/size only |
| "Tell me when X changes (push)" | ❌ | pull-only — agents must poll |
| "Which root is currently busiest?" | ❌ (until SW-AGENT-3 lands) | needs per-root rollup |
| "What does the journal *say it contains*?" | ✅ | `schema --format json` |
| "Reconstruct the full state of `/auth/` 20 min ago" | ⚠️ | replay events forward, but content not stored |

---

## 8. Missing features for the intended business problem

The stated problem: **multi-AI agents working on the same folder system and not knowing what each other is doing.**

That problem decomposes into four sub-problems:
1. **Awareness** — "what just happened?" — *already solved* by `events list`
2. **Attribution** — "who did it, on whose behalf, why?" — *partially solved*; `producer_id` exists but is one weak string
3. **Coordination** — "stop, I'm editing this" / "wait for me" / "I'm ready, your turn" — *not solved*
4. **Conversation** — "agent → agent message tied to a change" — *not solved*

Feature gaps ranked by leverage:

| Gap | What it unlocks | Cost | Recommend? |
|---|---|---|---|
| Multi-folder (SW-AGENT-3) | Awareness across N folders in one query | ~1.5d | **YES, do first** |
| Documented `payload_json` schema (`{actor_id, task_id, intent, addressee, ref_event_id, …}`) | Structured attribution + causality without schema migration | ~0.5d (docs + helpers) | **YES, cheap win** |
| `actors` table (`actor_id`, `label`, `last_heartbeat`, `current_focus_path_glob`) + `actor heartbeat` CLI | Liveness, "who's alive on this folder?" | ~1d | **YES** |
| `leases` table + `lease grant <path-glob> --ttl 5m` + advisory checks in event stream | Soft coordination — "I'm editing this" | ~1.5d | Maybe — depends on workload |
| Per-actor `seen` table (`actor_id, last_event_id_seen`) | Read receipts → "has X consumed my handoff?" | ~0.5d | Maybe |
| `messages` table for inter-agent comms (referenced from events) | Conversation channel decoupled from files | ~1d | Maybe — could also just be a file convention |
| Content snapshot store (blob keyed by `content_hash`, behind `--capture-content` flag) | Replay, diff, content-aware queries | ~2d + disk cost | Workload-dependent |
| Long-poll / SSE `events watch` subcommand | Push-style API for agents that don't want to poll every 5s | ~1d | Maybe — only if active mode isn't enough |
| Schema versioning + `format_version` in JSONL envelope | Lets us evolve without breaking consumers | ~0.5d | **YES, before more agents adopt it** |
| Per-root retention / quotas | Prevent one root from crowding others out of the queue | ~0.5d (after SW-AGENT-3) | Maybe |

**The smallest set of changes that materially solves the stated problem:**
1. Multi-folder (SW-AGENT-3) — awareness across the agent's workspace
2. Documented `payload_json` schema — structured attribution and causality
3. `actors` table + heartbeat — liveness
4. Schema versioning — protect the contract before adoption

Everything else can wait until usage tells us which gap actually hurts.

---

## 9. How is this different from python `watchdog`?

| Capability | sharedwatch | watchdog (Python) | inotify / fsnotify |
|---|---|---|---|
| Push vs pull | **Pull** (digests / queries) | Push (callbacks) | Push |
| Restart durability | **Yes** (SQLite journal) | No (in-memory) | No |
| Coalesce noisy bursts | **Built-in** (5s window) | DIY in your handler | DIY |
| Reconcile / drift recovery | **Built-in** (30m re-diff) | None | None |
| Latency | 5s–10min (passive), 5s (active) | sub-millisecond | sub-millisecond |
| Mechanism | Polling (portable, predictable) | inotify (Linux) / FSEvents (mac) / kqueue (BSD) / polling fallback | inotify only |
| Multi-reader | **Yes** (WAL + named cursors) | No (one process owns the handler) | No |
| Agent-facing query surface (`events list`, `sql`, `schema`) | **Yes** | No (it's a library — you build this) | No |
| Loss model | "Duplicates beat misses" — over-emits with reconcile safety net | Best-effort; events lost on crash | Best-effort + queue-overflow drops |
| Footprint | Single Go binary, ~10 MB | Python lib (you write the daemon) | Kernel-only or `inotifywait` |
| Cross-host | No | No | No |

**The fundamental difference:** watchdog is a **library** — you build the daemon, the persistence, the coalesce window, the reconciliation, and the query layer yourself. sharedwatch is an **application** with all those decisions baked in and a durable journal that survives restarts and is shared across readers (especially LLM agents).

Different positioning:
- **Use watchdog when** you want to write Python that *reacts* (callback handler in your process) to FS events with sub-ms latency, and you control the whole loop.
- **Use sharedwatch when** you want a calm, durable *journal* that survives restarts and that many independent readers (including agents and humans) can each query at their own cadence, with structured attribution and cursors.

Related-but-different tools worth naming:
- **`inotifywatch` / `entr` / `watchman`** — fire-and-forget shell triggers; no journal.
- **`auditd` / `fanotify`** — kernel-level audit; very accurate, root-required, no application-level semantics.
- **`fsnotify` (Go library)** — what sharedwatch *could* use under the hood for sub-second push, but currently doesn't (v1 is polling-only — deliberate simplification).
- **`git-watcher` / file-system-as-event-bus patterns** — same shape as sharedwatch; usually homegrown.
- **CRDTs / Yjs / Automerge** — solve content merge; orthogonal to "what happened?"
- **Inbox/queue services (NATS, Redis Streams, Kafka)** — designed for messaging, not file change capture. If the multi-agent system grows past one host, the move is *not* to extend sharedwatch — it's to publish events from sharedwatch *into* one of these.

---

## 10. Other questions worth asking before the next sprint

These are the "I want someone to think about this" questions, not necessarily things to build.

### Product
- **What is the product at v1?** A CLI? A daemon-with-API? A library others embed? A SaaS multi-tenant cross-host queue?
- **Who's the buyer / user?** Solo agent-builder using it for their own coordination? Team running an agent swarm? Platform vendor embedding it in an agent SDK?
- **Pricing model?** OSS forever? Paid features (cross-host queue, web UI, hosted, dashboards)? Per-seat? Per-event?
- **What's the "killer demo"?** Two LLM agents working on the same folder without stepping on each other — show that on day one or it's a thing nobody believes until they hit the pain.

### Schema / contract
- **`format_version` in every JSONL envelope.** Once agents read sharedwatch output, the schema is a public contract. Lock it now.
- **Are we sure `producer_id` should be a single string?** Once agents start populating it differently, migrating is hard.
- **Is `payload_json` a forever-blob, or should it be JSON-schema-validated?**

### Operational
- **Bounded queue + drop policy.** Retention prunes by days. A long active period can blow up the queue to 1M+ rows. What's the drop policy? Oldest-first? By status?
- **Cold-start cost.** First reconcile against a 100k-file tree emits 100k `file.created` events into batches of 100. Does that UX feel right?
- **What happens when SQLite WAL gets large?** Need a checkpoint policy story.
- **What's the failure mode if the disk fills?** Today: `InsertEvent` errors, watcher logs, work stops. Is that recoverable cleanly?

### Trust / privacy
- **Trust boundary.** A malicious producer (or just a misbehaving agent) can write garbage events with `test emit`. Is there an authentication story?
- **Privacy.** Path names leak structure; content hashes leak fingerprints. `--no-hash` exists, but for sensitive trees you may want path-component redaction too.
- **Audit log of *queries*.** Today we audit *file changes*. Who's been reading the journal? Not captured.

### Integration
- **Git-aware mode.** Many watched folders are git repos. Could group events by commit, suppress events during in-progress commits, attribute to commit author.
- **Symlink / bind-mount semantics.** Not specified. A symlink loop today would walk the file system to exhaustion.
- **Cross-tree rename handling.** A `mv /tracked/a /untracked/b` is currently a delete. A `mv /untracked/a /tracked/b` is currently a create. Is that the desired semantics?

### Multi-agent specifically
- **Right digest format for an LLM consumer.** Prose today. Probably wants structured JSON with grouped events, top-N actors, top-N paths, semantic tags, and a "what's new for me since cursor X" framing.
- **Topology questions.** N agents, 1 sharedwatch, 1 folder vs N agents, 1 sharedwatch, M folders vs N agents, M sharedwatchs, K folders. The current design assumes case 1 (and SW-AGENT-3 extends to case 2). Case 3 is multi-host territory — explicit non-goal in v1.
- **What happens when two agents have the same `--cursor-name`?** Today: they share it and "race" past each other's reads. Should the CLI warn?

---

## 11. Concrete recommendations — three time horizons

Calibrate by what you actually want to learn.

### One-week leverage
1. **Land SW-AGENT-3 (multi-folder)** — the highest-leverage agent-facing change.
2. **Document a `payload_json` v1 schema** — `{actor_id, task_id, intent, addressee, ref_event_id, tags}` — with helpers in the CLI (`--payload-actor`, `--payload-task`).
3. **Add `format_version: 1` to every structured output envelope** — protect the contract.
4. **Add a `producer_id` setter** — `sharedwatch run --actor coordinator-1 --task task-abc`. Avoids hand-editing `payload_json`.

### Three-week leverage
5. **`actors` table + `sharedwatch actor heartbeat <id>` CLI + `status --actors`** — liveness as a first-class concept.
6. **Per-root retention / quotas** (riding on SW-AGENT-3).
7. **Structured digest output** — `digest show <id> --format json` returns grouped events + top actors + top paths instead of prose.
8. **Bounded queue policy** — `max_pending_events` with drop-oldest behavior + warn-on-overflow.

### Three-month leverage (only after real usage)
9. **Leases** — only if the workload shows real clobbering.
10. **Content blob store** — only if "what was the file?" comes up repeatedly.
11. **Read receipts** — only if "did X see Y's handoff?" becomes a real workflow.
12. **Push surface** — `events watch` SSE/long-poll, only if 5s active interval isn't enough.

Things to **not** build until forced:
- Cross-host / network queue (publish to NATS/Redis instead).
- Hot reconfiguration of roots at runtime (restart is fine).
- Web UI (CLI + structured output covers it for agents; humans can have a thin TUI later).
- Per-root mode (mode is a property of the consumer's attention, not the folder).

---

## 12. Risks worth naming

- **Positioning collision.** "Calm, pull-based" sells to humans. Multi-agent coordination sometimes wants near-real-time. The active-mode design covers it, but the README pitch may need a second voice for the agent audience.
- **`payload_json` without a schema becomes a tarpit.** Once a few agents start putting different shapes in there, cleanup is painful. Document the contract before adoption, not after.
- **Polling cost on large trees with hashing on.** SHA-256-ing every file under 1 MB on every tick is fine for small trees, expensive for 100k+ files. Per-file hash caching keyed on `(path, size, mtime)` would help.
- **The "duplicates beat misses" doctrine has a downside for agent consumers.** An agent that reacts to every event will sometimes react twice. The journal needs to make idempotency obvious (event ID + content hash is enough — but document it).
- **Single-process, single-host is the wall.** Growth into team-scale ends here. The right answer isn't to extend sharedwatch over the network — it's to keep sharedwatch local and *publish* its events to whatever the team uses for messaging.
- **Schema-as-contract.** Every column we expose via `events list --format jsonl` is now in someone's prompt template. Versioning matters.

---

## 13. Cheat sheet — what's there today (one-page reference)

```
Cadences:
  watcher tick: 10m passive / 5s active
  reconcile:    30m
  coalesce:     5s window
  active TTL:   30m

Snapshot granularity:
  per-file:  rel_path, abs path, size, mtime, optional SHA-256
  per-tree:  JSON blob, latest 5 retained per source
  NOT captured: contents, perms, owner, xattrs, symlink target,
                inode, btime, atime, ACLs

Tables:
  events       — the journal (16 cols + 3 indexes)
  digests      — rolled-up batches (8 cols)
  snapshots    — JSON snapshots per source
  runtime_state — KV (mode, last runs)
  cursors      — named reader bookmarks

Event types:        file.created  file.modified  file.deleted  file.renamed
Event status:       pending → processing → processed | failed | suppressed
Digest status:      pending → read → archived
Event sources:      watcher | reconciler | test

Agent surface:
  events list  --since --until --type --source --status --path-glob
               --limit --order --format text|json|jsonl|csv
               --fields  --since-cursor  --cursor-name  --no-advance
  events cursor (list / reset / set / encode / decode)
  sql          (read-only by default; --write for mutation)
  schema       (live DDL + column metadata as JSON)

Multi-folder: NOT YET (SW-AGENT-3 open) — when landed, adds:
  watch_root column on events / digests / snapshots
  --root <label>=<path>  (definition)
  --root <label>         (filter)
```

---

## Open invitations for the next round of thinking

- What's the *one* multi-agent workflow we'd want to demo at v1, and does the current schema actually serve it?
- Is `payload_json`-with-a-schema enough, or do we need actor / lease / message as first-class tables?
- Where does sharedwatch end and "the team's actual messaging system" begin? Whatever the seam is, naming it is the v1 product decision.

— end of document —
