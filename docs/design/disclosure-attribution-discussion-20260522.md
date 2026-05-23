# sharedwatch — progressive disclosure & attribution

**Date:** 2026-05-22
**Status:** discussion document
**Companion to:** `sharedwatch-multi-agent-discussion-20260522.md`
**Scope:** two related questions about making the journal *usable* for AI agents at scale:
1. Can we give agents a token-efficient *zoom* across granularity levels — coarse summary → drill into details — without forcing them to load everything?
2. Can we know *who* changed what, *for which project/session*, and *with what intent* — and how do we surface that information?

Both are read-only design notes. No code yet.

---

## Part 1 — Progressive disclosure (granularity-of-detail)

### 1.1 The need
LLM agents have a finite context window. A busy shared folder produces hundreds of events per hour. Naively dumping `events list --limit 0` into an agent's prompt:
- burns thousands of tokens per call,
- buries signal in modify-bursts (the same file touched 14 times),
- is identical across consecutive checks (the agent re-reads what it already knows).

A well-shaped journal should let the agent ask *"give me the smallest description of what's happening that still answers my question."* Then *"now zoom into the part that matters."*

This is HTTP HATEOAS, OpenAPI navigation, file-tree expansion in an IDE — same shape. The cheap principle:
- **start coarse.** Counts, top-N, last activity per root.
- **drill in by dimension.** By root, by path subtree, by actor, by type, by time window.
- **stop when the question is answered.** Don't pay for resolution the caller didn't ask for.

### 1.2 What today's surface already supports
| Capability | Where it lives | Granularity it gives |
|---|---|---|
| `digest list` | `internal/db/db.go` ListDigests | Window-rolled prose summaries (L0–L1) |
| `events list --fields …` | `internal/db/events_query.go` | Column projection (L4) |
| `events list --limit … --order …` | same | Time-bounded paging |
| `events list --since-cursor / --cursor-name` | cursor.go | "Since I last looked" — token-bounded |
| `sql "SELECT type, COUNT(*) FROM events GROUP BY type"` | raw.go | Arbitrary aggregation (any level) |
| `schema --format json` | schema.go | Structure discovery — agent self-orients |
| `--path-glob 'auth/**'` | events_query.go | Subtree zoom (L3) |
| `--type` / `--source` / `--status` | same | Dimensional filter |
| `status --json` | app | Mode + counts (L1) |

**Observation:** the pieces of progressive disclosure are already in the box — they just aren't *composed* into a coherent zoom flow with named levels. Today an agent has to invent the same SQL aggregations every time.

### 1.3 Proposed level definitions

| Level | What it shows | Example output shape | Token cost (~ for 24h busy folder) |
|---|---|---|---|
| **L1** Overview | One number per dimension. Per-root pending, last_event_at, churn-24h, mode | `{mode: passive, roots: [{name: auth, pending: 3, last_evt: ..., churn_24h: 142}], total_events_24h: 412}` | ~100 tokens |
| **L2** Rollups | Top-N within a dimension. Top paths by churn, top actors, type histogram, hourly bucketing | `{root: auth, by_type: {modified: 87, created: 31}, top_paths: [...10...], top_actors: [...3...], hourly: [...24...]}` | ~400 tokens |
| **L3** Digests | Window-grouped prose summaries (existing) | `Shared drive digest at 14:32 UTC\n- modified auth/login.go\n- created auth/oauth.go ...` | ~600 tokens |
| **L4** Event projections | One row per event, minimal columns: `id, type, rel_path, actor, ts` | JSONL, 5 keys per row | ~50 tokens/event |
| **L5** Full events | All columns + payload_json | JSONL, all 16 columns | ~200 tokens/event |
| **L6** Content | The file contents at the time of the event | Blob — not stored today | depends |

Agents move *up* (zoom out) when the picture is too noisy, *down* (zoom in) when they need to act.

### 1.4 Endpoint sketches (proposed, not built)

A small set of named endpoints would canonicalize the zoom flow:

```bash
# L1 — overview, top-level
sharedwatch overview --format json
# {
#   "mode": "passive",
#   "since": "24h",
#   "roots": [
#     {"label": "auth", "pending": 3, "last_event_at": "...", "churn": 142},
#     {"label": "billing", "pending": 0, "last_event_at": "...", "churn": 9}
#   ],
#   "drill": {
#     "auth": "events stats --root auth",
#     "billing": "events stats --root billing"
#   }
# }

# L2 — per-root rollup
sharedwatch events stats --root auth --since 24h --format json
# {
#   "root": "auth",
#   "window": {"start": "...", "end": "..."},
#   "by_type": {"file.modified": 87, "file.created": 31, "file.deleted": 24},
#   "by_actor": {"claude-1": 102, "claude-2": 28, "human-mike": 12},
#   "top_paths": [
#     {"path": "auth/login.go", "events": 14, "last_at": "..."},
#     ...
#   ],
#   "drill": {
#     "by_path": "events list --root auth --path-glob '...' --fields ...",
#     "by_actor": "events list --root auth --payload-key actor --payload-value claude-1"
#   }
# }

# L3 — existing
sharedwatch digest show <id>

# L4 — minimal event projection (already supported, propose a curated default)
sharedwatch events list --root auth --fields id,type,rel_path,actor,ts --format jsonl

# L5 — full
sharedwatch events list --root auth --fields '*' --format jsonl

# L6 — content (not yet)
sharedwatch content show <event_id>     # would require content snapshot store
```

Two design notes:

**a. "drill" hints in JSON output.** Each level returns a `drill` object — a map of dimension → the exact command to expand that dimension. Same idea as a hypermedia link relation. An agent doesn't need to memorize the CLI grammar; it follows the link. *This is the highest-leverage idea in this document.* Costs ~50 tokens per response, eliminates a class of "the agent doesn't know how to ask" bugs.

**b. "summarize" hints.** Every L4/L5 output that exceeds a threshold (e.g., >50 rows) should suggest `try overview` or `try events stats --since …` instead. Self-throttling.

### 1.5 Compression strategies (per level)

At each level there are token-saving moves the renderer can make:

- **Group consecutive modifies on the same path.** Instead of 14 lines of `modified auth/login.go`, render `modified auth/login.go (×14, last at 14:32)`. Already partially done by `coalesce`, but coalesce is bounded by a 5s window; a renderer-level grouping can span the whole result set.
- **Suppress quiet ranges.** Instead of `[]` for each empty hourly bucket, emit `quiet 09:00–12:00 (3h)`.
- **Use semantic tags from `payload_json`.** If two events both have `tags: ["refactor"]`, render once with `(refactor ×2)`.
- **Path prefix factoring.** Sequential paths `auth/login.go`, `auth/oauth.go`, `auth/jwt.go` rendered as `auth/{login.go, oauth.go, jwt.go}`. Real token savings on deep trees.
- **Hash-stable elision.** If a path's `content_hash` is the same as last digest's hash, label `(hash unchanged)` rather than re-listing modifications. Catches "saved without changes."

These are renderer concerns, not storage concerns — they don't need a schema change.

### 1.6 Two implementation paths (and the hybrid)

**Path A: build new aggregation endpoints** (`overview`, `events stats`, `events graph`, `content show`).
- Pros: canonical, discoverable, agent-friendly (clear vocabulary).
- Cons: more surface to maintain, divergence risk between endpoints and the SQL escape hatch.

**Path B: don't build endpoints; document SQL recipes.**
- Pros: zero new code; the agent can compose anything.
- Cons: every agent rolls its own; we own the cost of agents writing fragile SQL; no progressive-disclosure standard.

**Hybrid (recommended): ship 3 endpoints, document SQL for the rest.**
- `overview` — L1.
- `events stats --root <x> --since <range>` — L2.
- The existing `events list --fields` and `digest show` cover L3/L4/L5.
- Ship a `docs/AGENT_RECIPES.md` with copy-paste SQL for the long tail (top-paths-touched-by-actor, churn-per-hour, etc.).
- Both endpoints return `drill` hints that point an agent to the next level (or to specific SQL recipes).

That gets us the named zoom levels with minimal new surface, and uses SQL as the principled escape hatch.

### 1.7 Is it possible with today's structure?

**Yes, mostly without schema changes.**

What's already supported:
- L4/L5 — `events list` with `--fields`.
- L3 — `digest show`.
- Any L1/L2 aggregation can be expressed as SQL today via the escape hatch.

What's missing for a clean experience:
- Named `overview` / `events stats` endpoints (~ a day of work).
- Renderer-level grouping/compression (~ a day, format-side only).
- `drill` hints in JSON envelopes (~ half a day, mostly templates).
- Content store for L6 (significant work — separate decision).

**Verdict:** progressive disclosure is achievable as a small, additive layer on top of what exists. The hard part is *committing to the level definitions* so agents (and humans) can trust them as stable contracts.

### 1.8 Tradeoffs to call out
- **Aggregation endpoints can drift from the source of truth.** If `events stats` says 87 modifies but `sql SELECT COUNT…` says 88, that bug will burn somebody at 2am. Tests must cross-check.
- **Compression hides edge cases.** "14 modifies, last at 14:32" loses the per-event timestamps. An agent that *needs* the spread has to drill down. Make sure the `drill` link is exact.
- **More levels = more decisions about defaults.** Pick conservative defaults (`--limit 50` on L4/L5, `--since 24h` on L1/L2) and lean on `drill` to widen.

---

## Part 2 — Attribution: WHO changed WHAT, for WHICH project/session, with WHAT intent

### 2.1 The question, decomposed

"WHO changed what" is actually six entwined questions:

| Question | Concept | Today's coverage |
|---|---|---|
| Which process wrote the bytes? | **Actor** (OS process or agent identity) | Weak — `producer_id` default = `<host>:<pid>` |
| Which logical run is this change part of? | **Session** | None |
| Which workstream / topic? | **Project** / **domain** | None (could be partition by root) |
| What was the change *trying* to accomplish? | **Intent** | None — `payload_json` could hold it |
| What earlier event caused this change? | **Causality** / `ref_event_id` | None |
| Who is this change *for*? | **Addressee** | None |

Today, all six collapse into one weak field (`producer_id`) and one untyped blob (`payload_json`).

### 2.2 The fundamental design choice: cooperative vs inferred

There are two philosophies for attribution:

**Cooperative attribution.** Writers (humans, agents) declare who they are and what they're doing. The system records the declarations. Cheap. Honest if everyone plays along. Worthless if a writer doesn't or lies.

**Inferred attribution.** The system observes the OS and reverse-engineers the actor (which PID wrote the bytes? which agent's command line? which container?). Expensive. Platform-specific. Robust against silent writers.

For sharedwatch the realistic answer is **mostly cooperative, with optional inference as a backstop**. The threat model isn't malicious agents (agents that lie about being someone else); it's *forgotten* attribution (agents that didn't think to declare). Cooperative defaults catch 95%; inference catches the rest.

### 2.3 Twelve ways to surface attribution (ranked by cost/leverage)

Roughly ordered cheap → expensive.

**Direct channels (live on the event itself):**

**1. Documented `payload_json` schema** — cheapest possible win.
Define a v1 schema:
```json
{
  "actor": "claude-1",
  "session": "sess-2026-05-22-abc",
  "task": "refactor-auth",
  "intent": "extracting JWT validation into separate package",
  "addressee": "human-mike",
  "ref_event_id": "evt_abcd...",
  "tags": ["refactor", "auth"]
}
```
Already storable. Zero schema migration. Helpers (`--payload-actor`, `--payload-session`) in the CLI make it a one-line invocation for agents. Validation can be soft (warn on unknown keys).

**2. Promoted columns** — add `actor_id`, `session_id`, `task_id` as real columns.
Pros: indexable, queryable without JSON parsing, type-safe.
Cons: schema migration, column proliferation, opinion lock-in. **Worth it only after #1 has revealed which fields are universally used.**

**3. Sidecar `.meta/` files** — every agent change is accompanied by a small JSON file (e.g., `.meta/auth/login.go.intent.json`).
Pros: the metadata itself becomes an event in the journal. Self-attesting (the file exists in the tree).
Cons: doubles event volume; clutter; agents must remember.

**4. Filename conventions** — `auth/login.go.@claude-1.json`.
Pros: visible at `ls` time.
Cons: ugly, brittle, mixes data and metadata.

**5. Path-based partitioning** — `agents/claude-1/auth/...`.
Pros: free per-actor grouping.
Cons: breaks shared-workspace semantics (multiple agents working on the *same file* is the whole point).

**Side channels (live in the DB, separate from events):**

**6. `actors` table + heartbeat** — `(actor_id, label, last_heartbeat, current_focus_path_glob, capabilities, metadata_json)`.
Live registry of "who's around right now." CLI: `sharedwatch actor heartbeat claude-1 --focus 'auth/**'`. The `status` command surfaces it.
Pros: liveness independent of events; useful for "is X still working?".
Cons: agents have to remember to heartbeat; stale entries need TTL.

**7. `sessions` table** — `(session_id, actor_id, started_at, ended_at, task, parent_session_id)`.
Pros: gives an event a logical container ("this change belongs to refactor-auth session"). Enables "what did session S touch?" queries.
Cons: agents need to start/end sessions explicitly. Or: a session is auto-started on first event from an actor and auto-closed after N minutes of inactivity.

**8. `intents` table — declared BEFORE the edit** — `(intent_id, actor_id, session_id, path_glob, declared_at, expires_at, description)`.
Pros: gives a *forward-looking* attribution. When events arrive on a declared path, they get tagged to the intent automatically. Doubles as a soft lease.
Cons: agents have to think before editing — a behavior shift.

**9. CLI wrapper that auto-tags** — `sharedwatch as claude-1 --session sess-abc --task refactor-auth -- vim auth/login.go`.
The wrapper sets env vars, starts/ends a session, populates `payload_json` on any event arising during the wrapped command's runtime.
Pros: zero behavior change for the inner tool; the agent just changes its invocation.
Cons: only works for changes that fire during the wrapped lifetime; misses async writes.

**10. Environment-variable-driven** — `SHAREDWATCH_ACTOR=claude-1 SHAREDWATCH_SESSION=...`. The watcher reads them — but here's the catch: the watcher doesn't own the writes. *The watcher doesn't know which process wrote which byte.* Env vars on the watcher attribute everything to one actor; useless for multi-agent.
**This approach only works for the synthetic `test emit` path, not the real diff path.**

**11. OS-level attribution** — `fanotify` (Linux) or audit subsystem to map FS event → writing PID → command-line.
Pros: ground truth, doesn't require cooperation.
Cons: Linux-only, root-ish, brittle, deviates from the "no kernel-specific wiring" v1 doctrine.

**12. Lease grants imply attribution** — when actor X holds an advisory lease on path P, any events on P during the lease window are tagged with X.
Pros: lease itself is a useful feature; attribution comes free.
Cons: requires the lease feature, requires actors to acquire leases (cooperative).

### 2.4 Layered adoption plan

A path that lets us start cheap and extend later without re-doing work:

| Layer | What's added | Cost | Cumulative capability |
|---|---|---|---|
| **L0 (today)** | `producer_id` + free-form `payload_json` | none | weak |
| **L1** | Documented `payload_json` schema + helper flags (`--actor`, `--session`, `--task`) | ~0.5d | structured attribution; queryable via existing `--payload-key` filter |
| **L2** | `actors` registry + heartbeat + `status --actors` | ~1d | liveness; "who's around?" |
| **L3** | `sessions` table + auto-session-on-first-event | ~1d | logical run grouping |
| **L4** | `intents` table — declared-before edits | ~1.5d | proactive attribution + soft leases |
| **L5** | OS-level inference for unattributed events (`fanotify`/`audit`) | ~3d, platform-specific | backstop for forgotten attribution |

Recommended commit: L1 + L2 in the next sprint (cheap, high-leverage). L3–L5 only when real workloads create the demand.

### 2.5 Concrete payload schema (L1) — proposed v1

```json
{
  "schema_version": 1,
  "actor": "claude-1",              // stable id; required
  "actor_kind": "ai_agent",         // "human" | "ai_agent" | "automation"
  "session": "sess-abc-2026-05-22", // optional; group of related events
  "task": "refactor-auth",          // optional; human-meaningful work label
  "intent": "extract jwt logic",    // optional; free-text reason
  "addressee": "human-mike",        // optional; "for whom?"
  "ref_event_id": "evt_...",        // optional; causal predecessor
  "tags": ["refactor", "auth"]      // optional; freeform but agreed
}
```

Rules:
- `actor` is the only required key.
- Unknown keys are allowed but ignored by tooling.
- Validation is soft (warn on `schema_version` mismatch; never reject events).
- Existing events with `payload_json = '{}'` are valid (unattributed); queries that filter on `actor` simply skip them.

CLI helpers (no code yet, sketch):
```bash
sharedwatch test emit foo.md \
  --actor claude-1 --session sess-abc --task refactor-auth \
  --addressee human-mike --tag refactor --tag auth
# → payload_json auto-built from the flags

sharedwatch run --actor claude-1 --session sess-abc --task refactor-auth
# → every event produced during this run carries that payload
```

### 2.6 Querying attribution

With the L1 schema in place, all these are one-liners on the existing query engine:

```bash
# Who's active right now?
sharedwatch events list --since 5m --fields actor,rel_path,ts --format jsonl \
  | jq -r .actor | sort -u

# What did claude-1 touch in the last hour?
sharedwatch events list --since 1h \
  --payload-key actor --payload-value claude-1 \
  --fields rel_path,type,ts --format jsonl

# Show me all events for session sess-abc
sharedwatch events list --payload-key session --payload-value sess-abc

# Top actors by churn this week
sharedwatch sql "SELECT json_extract(payload_json,'$.actor') AS actor, COUNT(*) \
                 FROM events WHERE created_at > datetime('now','-7 days') \
                 GROUP BY actor ORDER BY 2 DESC"

# Cross-attribute: addressed-to-me events I haven't seen yet
sharedwatch events list --cursor-name me \
  --payload-key addressee --payload-value human-mike
```

Note `--payload-key/value` already supports flat top-level keys (see `internal/db/events_query.go`). The `--payload-key` filter is post-fetch, but with cursors and limits the cost is bounded.

### 2.7 Failure modes & defenses

| Failure | Defense |
|---|---|
| Agent forgets to set `actor` | Soft-warn at digest time (`X events unattributed in window`). Don't reject. |
| Two agents reuse the same `actor` id silently | Register actors in the `actors` table; warn on collision on heartbeat. |
| Stale `actors` row (agent died) | TTL on heartbeat; `status` shows `stale=true`. |
| Schema drift in `payload_json` | `schema_version` in payload; renderer warns on unknown versions. |
| Lease squatting (agent holds lease forever) | Required TTL on lease; auto-release. |
| Agent lies about being someone else | Out of scope. sharedwatch is not an auth system; that's `fanotify` or `auditd` territory. Document the trust boundary. |

### 2.8 The hardest sub-question: WHICH PROJECT

This is the most subjective. Possibilities, all viable:

1. **Watch-root *is* the project.** Each tracked folder = one project. Simple and convenient.
2. **`task` in payload_json *is* the project.** Lets multiple projects share a root.
3. **A `project` column** — promoted from payload after we know what fields stabilize.
4. **Inferred from path prefix** — `auth/**` = auth project, `billing/**` = billing project. Brittle.
5. **External mapping table** — `path_glob → project_label`. Heavy, but flexible.

**Recommendation:** start with #1 (watch-root) and #2 (`task` in payload) — they're free. Promote to #3 once you know what's actually used.

---

## Part 3 — Putting it together

A coherent v1 story:

> When you, as an agent, want to know what's happening:
>
> 1. Ask sharedwatch for the L1 overview. Get per-root counts, latest activity, mode.
> 2. Follow the `drill` link into a root that has new activity.
> 3. At L2 you see top paths, top actors, type histogram. Follow the link that matches your intent.
> 4. At L4 you stream events as JSONL with the columns you need (`actor`, `rel_path`, `ts`, optionally `payload_json` for intent/session).
> 5. When you act, set `--actor`, `--session`, `--task` so other agents can do the same query and find your work.

The progressive-disclosure layer (Part 1) makes the journal *navigable*. The attribution layer (Part 2) makes the journal *self-describing about who did what*. Together they make sharedwatch a useful coordination substrate for a swarm of agents — not just an FS event capture tool.

---

## Open questions (worth a follow-up)

1. **Do we standardize a `drill` JSON envelope across all structured commands, or only the new aggregation ones?** Standardizing across all is cleaner but a bigger contract surface.
2. **Should `actor` be promoted to a column in v1, or stay in `payload_json`?** Column is queryable without JSON parsing; payload is no migration. Bet: payload now, promote in v2 after observing usage.
3. **Session lifecycle — explicit start/end or implicit auto-on-first-event?** Implicit is friendlier; explicit is auditable. Could support both.
4. **Should `addressee` be a single actor or a list?** Real handoffs sometimes go to multiple agents. Start with single, allow list later.
5. **What's the failure mode when an L1 `overview` aggregation is slow on a 1M-event journal?** Cache the last computed overview in `runtime_state` with a TTL.
6. **Inferred attribution — is the platform-specificity worth it?** Probably no for v1; revisit if "forgotten attribution" is a real customer pain.
