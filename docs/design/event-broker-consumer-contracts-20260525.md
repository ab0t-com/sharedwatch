# sharedwatch — event-broker consumer contracts + use cases

**Date:** 2026-05-25
**Status:** discussion (precedes implementation tasklist `tickets/tasklist_20260525_015121.md`)
**Author:** claude (this session)
**Builds on:** [`event-and-hook-surface-expansion-20260524.md`](./event-and-hook-surface-expansion-20260524.md) §0 — the principle that says **emit liberally, hooks stay narrow**.

This doc records the **consumer-side thinking** that justifies the implementation work in the companion tasklist. It exists so the contract we ship is shaped by real (or realistic) use cases, not by what was convenient to add.

The framing: **the events journal IS a single-host event broker.** Read it that way. Each consumer is a subscriber that pulls via a named cursor; sharedwatch is the broker that durably stores, filters, and serves. The "what we emit and how" decisions in this doc are all framed against that model.

---

## 1. Why the broker framing matters

A naive read of "sharedwatch emits more event types" is "we're adding more stuff to a database." That's true but misses the point.

A broker framing changes the conversation:

- **Subscribers are independent.** New consumers don't require sharedwatch changes. Anyone can write a script, a Go program, a Python agent — they tail the journal with their own cursor and react. No registration with the daemon, no callback API, no plugin loading.
- **The journal is the API.** This was already the project's promise. Filling out the event surface turns that promise from "true for file events" into "true for everything sharedwatch knows about."
- **Schema is a contract.** Once a subscriber relies on `lease.violated` carrying `violator_actor` and `lease_actor`, we can't quietly rename those. Schema discipline becomes load-bearing.
- **Tiers protect users.** A user who runs `emit_profile: minimal` sees the v0.0.x event surface unchanged. A user who runs `verbose` opts into more. Adding new event types is non-breaking because they only land at higher tiers by default.

The **broker properties** the journal already has:

| Property | How we provide it | Notes |
|---|---|---|
| **Topics** | `--type` filter (repeatable) on `events list` | Plus `--source`, `--path-glob`, `--payload-key`/`value` |
| **Durable** | SQLite-backed, retained `retention_days` | Default 30 days |
| **Multi-consumer** | Named cursors (`--cursor-name <id>`) | Independent positions; no consumer interference |
| **Replay-able** | `events cursor reset <name>` to re-read from start; `--since-cursor <token>` for one-shot replay | |
| **Schema-versioned** | `format_version: 1` in payload JSON | Bump on breaking changes |
| **Source-tagged** | `source=watcher\|reconciler\|test\|hook\|...` | New sources added: `daemon`, `mode`, `coord`, `actor`, `retention` |

The **broker properties we explicitly DON'T provide** (and why):

| Missing property | Why we skip it |
|---|---|
| Pub/sub push fan-out | Pull-only is the calm contract; pushing would re-introduce backpressure risk |
| Cross-host transport | Single-host design; multi-host = a different product |
| Exactly-once delivery | Cursor + reconcile re-emits = at-least-once; consumers must be idempotent |
| Dead-letter queue | `events.failed` status is the rough equivalent; no separate queue table |
| Synchronous ACK/NACK | Cursor-advance is the implicit ACK; failed processing means "don't advance" |
| Cross-source ordering | Within a source/root, causal order holds; across sources, no guarantee |

---

## 2. Consumer contracts — what we promise subscribers

For every event type we add, we commit to these guarantees. **These ARE the API.** Anyone writing a subscriber should be able to rely on them indefinitely.

### Stable shape

- **Type name is stable.** Once we ship `lease.violated`, that name is permanent. New behaviour gets a new type name (e.g. `lease.violated.v2`); we don't redefine an existing one.
- **Source is stable.** `source=watcher` always means "the file-watching loop"; `source=daemon` always means "daemon lifecycle"; `source=coord` always means "lease/intent/actor-registry operations". A new conceptual category gets a new source.
- **`format_version` is the canary.** A bump means consumers may need updates. We promise to bump it on breaking payload changes; we promise NOT to bump it on additive changes (new optional keys with `omitempty`).

### Backwards-compatible payload evolution

- **Adding a key** = non-breaking. `omitempty` until consumers are likely to expect it.
- **Renaming a key** = breaking → bump `format_version`. Never silently rename.
- **Removing a key** = breaking → bump `format_version` AND keep the old key emitting in a deprecation window.
- **Changing a value's semantics** (e.g. `exit_code` going from integer to enum) = breaking → bump.

### Delivery guarantees

- **At-least-once** within `retention_days`. The reconcile pass may re-emit observations; the consumer must handle duplicates (use the event `id` as a dedup key).
- **Causal ordering within a source** for a given watch root. `file.created` precedes `file.modified` for the same `(rel_path, watch_root, actor)`. Across sources or roots, ordering is interleaved.
- **No exactly-once.** Period. Don't write a subscriber that relies on it.
- **Retention floor.** Events live at least `retention_days` from emit time. Don't tail with a cursor that's been idle longer than that — you'll skip what was pruned.

### What consumers must do

- **Maintain a cursor**, named uniquely per consumer (`<actor>-<task>` is a good convention). Sharing cursor names = sharing reads.
- **Be idempotent.** Treat each delivery as "process or skip"; never "fail loud and crash."
- **Filter at the source.** `--type` and `--source` are cheap; reading then dropping is wasteful.
- **Don't poll faster than 1Hz.** Project rule. The consumer cadence (5s active, 10min passive) is the daemon's natural pulse.

---

## 3. Subscriber catalog — twelve real consumer use cases

For each: who, what they need, what they subscribe to, what makes their integration cleaner under the new event surface.

### 3.1 Diff-on-change indexer (e.g. for embeddings)

**Who:** an AI/ML pipeline that maintains a vector database of file content. New / modified files need re-embedding; deletes need removal from the index.

**Subscribes to:** `file.created`, `file.modified`, `file.renamed`, `file.deleted`.

**Cursor:** `--cursor-name embedding-indexer`.

**Pattern:**
```bash
sharedwatch events list --cursor-name embedding-indexer \
  --type file.created --type file.modified --type file.renamed --type file.deleted \
  --format jsonl --limit 100 |
while read e; do
  path=$(echo "$e" | jq -r .rel_path)
  type=$(echo "$e" | jq -r .type)
  case $type in
    file.deleted) curl -X DELETE "https://vecdb/v1/$path" ;;
    *) embed "$path" | curl -X PUT "https://vecdb/v1/$path" -d @- ;;
  esac
done
```

**What the event surface gives them that polling doesn't:** cursor-based delivery means a restart picks up exactly where it left off. No "list files newer than timestamp X and hope I got the right X."

**Doesn't want:** hook meta-events, mode changes, coord chatter. Filter at `--type`.

### 3.2 Automated one-way sync to remote store

**Who:** anyone replicating the watched folder to S3 / GCS / a backup volume.

**Subscribes to:** `file.created`, `file.modified`, `file.deleted`, `file.renamed`, plus `reconcile.recovered_threshold` (so a watcher hiccup doesn't leave the remote out of sync).

**Cursor:** `--cursor-name s3-sync-<region>`.

**Pattern:**
```bash
sharedwatch events list --cursor-name s3-sync-us-east-1 \
  --type file.created --type file.modified --type file.deleted --type file.renamed --type reconcile.recovered_threshold \
  --format jsonl |
while read e; do
  type=$(echo "$e" | jq -r .type)
  if [ "$type" = "reconcile.recovered_threshold" ]; then
    # Watcher missed some events; trigger a full diff sync to be safe
    aws s3 sync $WATCH_PATH s3://my-bucket/
  else
    # Per-file delta
    handle-file-event "$e"
  fi
done
```

**Why this needs `reconcile.recovered_threshold`:** without it, the consumer trusts its cursor and would miss events the watcher dropped (rare but real on busy filesystems). The event tells it "the safety net just fired; you might want to re-baseline."

### 3.3 Slack / PagerDuty alerting

**Who:** ops/SRE responsible for keeping the daemon healthy and the data flowing.

**Subscribes to:** `hook.failed`, `events.failed_threshold`, `events.stuck_detected`, `daemon.crashed`, `reconcile.drift_detected`.

**Cursor:** `--cursor-name oncall-pager`.

**Why threshold-gated events matter:** without thresholds, `events.failed_threshold` would fire 1000 times during a real incident — paging on-call 1000 times. The threshold gives a rising-edge trigger that fires once when things go bad and once when they recover.

### 3.4 Multi-agent peer-coordination bot

**Who:** an AI agent that needs to know what other agents are doing in real time.

**Subscribes to:** `intent.declared`, `intent.revoked`, `lease.granted`, `lease.released`, `lease.violated`, `actor.registered`, `actor.went_stale`, `digest.created`.

**Cursor:** `--cursor-name peer-bot`.

**Pattern:** maintains an in-memory peer model — "who's working on what, who's gone quiet, who just stepped on whose lease." Reacts to `lease.violated` immediately (e.g. abort + retry). Uses `intent.declared` to know what's planned before peers start work.

**Critical filter:** the bot should ignore its OWN actions echoing back. Add `--payload-key actor --payload-value '!self'` semantics (today: filter client-side by checking `payload.actor != $MY_ACTOR`).

### 3.5 Compliance / audit archiver

**Who:** compliance team needing an immutable record of every state change.

**Subscribes to:** everything (`emit_profile: all` on the daemon side; subscriber filter = none).

**Cursor:** `--cursor-name audit-archiver`.

**Pattern:** streams every event to an immutable store (S3 Object Lock, append-only ledger DB, write-once-read-many). Cursor never resets; events become evidence.

**What we promise here:** events stay in the journal for `retention_days`, so as long as the archiver runs at least that often, no event is lost. Set the daemon's `emit_profile: all` to make sure even high-frequency events (actor heartbeats) are captured.

### 3.6 Dashboard / observability metrics exporter

**Who:** ops, building dashboards in Grafana / Datadog / a custom UI.

**Subscribes to:** `mode.changed`, `digest.created`, `retention.ran`, `reconcile.ran`, `events.failed_threshold`, `events.stuck_detected`.

**Cursor:** `--cursor-name metrics-exporter`.

**Pattern:** aggregates event arrivals over fixed windows (1 min, 5 min, 1 hour), exports counts to the metrics backend. The events are metadata-light; the cost of subscribing is minimal.

**A real Tier-3 plugin idea:** `sharedwatch-to-prometheus` — Go binary that subscribes to these events and exposes `/metrics`. ~200 LOC. Lives in a separate repo because it's a different concern.

### 3.7 CI/CD trigger

**Who:** a build system that should run when "meaningful work" lands in the watched folder.

**Subscribes to:** `digest.created` (the natural batching point), `file.created --path-glob "specs/**"` (specific path trigger).

**Cursor:** `--cursor-name ci-trigger`.

**Pattern:** uses `digest.created` for "general activity" triggers (debounced via the digest's natural batching). Uses path-globbed `file.created` for "specific file landed" triggers (e.g. a new spec file).

**Why both:** the digest signal is the right primitive for batch builds; the path-filtered file signal is the right primitive for spec-triggered workflows where latency matters.

### 3.8 Watchdog / health monitor

**Who:** the thing that pages on-call when sharedwatch itself goes down.

**Subscribes to:** `daemon.started`, `daemon.crashed`, `events.stuck_detected`, `mode.changed`.

**Cursor:** `--cursor-name health-watchdog`.

**Pattern:** correlates `daemon.started` events against expected uptime. A gap implies a crash that wasn't recorded (e.g. SIGKILL'd). Pairs with the next-startup's `daemon.crashed` event (which we emit when stale-lock detection sees a prior daemon didn't clean up).

### 3.9 Disaster-recovery replicator

**Who:** infra team running a standby instance for failover.

**Subscribes to:** everything except `hook.*` (those are the standby's hooks, not the primary's).

**Cursor:** `--cursor-name dr-replicator`.

**Pattern:** every event replayed against the standby's local state. When primary fails over, standby takes over with continuity. Requires deterministic ordering — which is why §2's causal-ordering-within-source promise matters here.

### 3.10 Cost-attribution analyzer

**Who:** cloud ops attributing storage cost to teams.

**Subscribes to:** `file.created`, `file.modified` with `--path-glob` per team.

**Cursor:** per-team-named cursor (`--cursor-name cost-team-design`, `cost-team-eng`, etc.).

**Pattern:** sums `file_size` over time per team, exports to billing. Multi-tenant by cursor.

### 3.11 Auto-diff plugin (the user's example)

**Who:** a third-party tool that wants to show "what changed in file X."

**Subscribes to:** `file.modified --path-glob 'src/**/*.go'` (or any user-defined glob).

**Cursor:** `--cursor-name diff-plugin-<workspace>`.

**Pattern:**
```bash
sharedwatch events list --cursor-name diff-plugin-default \
  --type file.modified --path-glob 'src/**/*.go' \
  --format jsonl |
while read e; do
  path=$(echo "$e" | jq -r .path)
  hash=$(echo "$e" | jq -r .content_hash)  # when --hash on is set
  prior_hash=$(redis-cli GET "lastseen:$path")
  if [ "$hash" != "$prior_hash" ]; then
    git diff --no-index "/cache/$path" "$path" | post-to-some-ui
    redis-cli SET "lastseen:$path" "$hash"
  fi
done
```

**What the new event surface gives them:** they can rely on `content_hash` being present in the payload (when `--hash on`) — closes the existing gap where hash was computed but not always reachable from a cursor read.

### 3.12 Plugin / extension framework (the broker payoff)

**Who:** anyone wanting to extend sharedwatch without contributing upstream.

**Examples** (all live in separate repos, none require changes to sharedwatch):
- `sharedwatch-to-prometheus` — metrics exporter (see 3.6)
- `sharedwatch-to-jaeger` — turn `mode.changed` + `digest.created` into spans
- `sharedwatch-to-git-autocommit` — batches file events into git commits, fires on `digest.created`
- `sharedwatch-mcp-bridge` — exposes the journal as MCP tools so Claude / other LLMs can query it natively
- `sharedwatch-replay` — debug tool that replays a cursor through a test consumer for development

**The pattern:** all of these are subscribers. They use the standard `events list --cursor-name <plugin> --type ... --format jsonl` shape. There is no plugin API to learn, no Go ABI to track, no security boundary to verify. The journal IS the plugin API.

---

## 4. Implications for the implementation

The consumer catalog above drives several design decisions in the tasklist:

### 4.1 Must-have for v0.1.1 (SW-AGENT-30)

These are the event types where consumers in §3 have clear, immediate use:

- `digest.created` — 3.7, 3.8, 3.6, 3.4 all rely on it; closes the symmetry gap (every other operation emits an event)
- `daemon.started` / `daemon.stopping` / `daemon.crashed` — 3.8, 3.9
- `mode.changed` — 3.4, 3.6, 3.8
- `reconcile.ran` + `reconcile.drift_detected` + `reconcile.recovered_threshold` — 3.2, 3.3, 3.6
- `retention.ran` — 3.6
- `events.failed_threshold` + `events.stuck_detected` — 3.3, 3.8
- `lease.granted` / `.released` / `.renewed` / `.expired` / `.violated` — 3.4
- `intent.declared` / `.revoked` / `.expired` — 3.4
- `actor.registered` / `.went_stale` / `.removed` — 3.4

### 4.2 Should-have at verbose tier

These show up in fewer subscribers but matter for the audit / DR use cases (3.5, 3.9):

- `actor.heartbeat_received` (chatty — verbose-only by default)
- `mode.ttl_extended` (every active-mode emit extends; verbose-only)
- `snapshot.taken` / `snapshot.failed` (per-cycle, ops-only)
- `db.error` (rare but high-severity)

### 4.3 Consumer-side ergonomics (drive doc work, not code)

- **Self-filtering** (3.4) — `--payload-key actor --payload-value <id>` already works; the gotcha is "ignore my OWN actions" needs `!=` semantics. Note as future enhancement; not blocking.
- **Cursor naming convention** — recommend `<actor>-<task>` or `<plugin>-<workspace>` in the broker doc.
- **Filter recipes** — provide canonical filter expressions per consumer type in `docs/guides/event-broker.md`.

### 4.4 Configuration shape (best-practice decision)

Per the principle reframe in `event-and-hook-surface-expansion-20260524.md` §0, four tiers with per-class overrides + threshold knobs:

```yaml
# Daemon side — controls what gets emitted into the journal.
emit_profile: standard            # default; minimal | standard | verbose | all
emit_overrides:                   # optional, fine-tuning
  actor_heartbeats: true          # opt INTO verbose-tier class
  reconcile_per_cycle: false      # opt OUT of standard-tier class
emit_thresholds:                  # optional, threshold-gated event tuning
  events_failed: 50               # fire events.failed_threshold when count crosses 50
  events_stuck: 10                # fire events.stuck_detected when count crosses 10
  reconcile_drift: 25             # fire reconcile.drift_detected when recovered > 25
```

Env vars:
- `SHAREDWATCH_EMIT_PROFILE=standard`
- `SHAREDWATCH_EMIT_OVERRIDE=actor_heartbeats=true,reconcile_per_cycle=false` (comma-separated)
- Thresholds: operator-only, not exposed via env. (Avoid over-engineering.)

Root flags:
- `--emit-profile <tier>`
- `--emit-override <class>=<bool>` (repeatable)

`config show` surfaces:
- resolved profile
- map of overrides
- map of thresholds
- **derived per-class enable list** so operators can verify what'll actually emit

This config shape **was the user's explicit guidance**: easy to use, easy to toggle, smart defaults, no over-engineering.

### 4.5 What we DO NOT change

Per the principle reframe and the user's explicit instruction "don't remove any features we currently have, extend them":

- `--on-digest` shipped in SW-AGENT-29 — unchanged. Still the calm shell-hook for digests.
- File events (`file.created/.modified/.deleted/.renamed`) — unchanged emission behaviour. They live at `emit_profile: minimal`, so even users on the lowest tier still get the v0.0.x baseline.
- `events.SourceWatcher`, `events.SourceReconciler`, `events.SourceTest`, `events.SourceHook` — unchanged. We ADD new sources (`daemon`, `mode`, `coord`, `actor`, `retention`) — we don't reassign existing ones.
- All existing cursor / filter / format semantics — unchanged. New event types compose with existing tooling for free.
- Retention behaviour — unchanged. All events (existing + new) prune at `retention_days`.

---

## 5. Risks + mitigations

Worth thinking through before code:

| Risk | Mitigation |
|---|---|
| Verbose-tier emits flood the journal during a busy period and bury file events in noise | Default tier is `standard`; verbose is opt-in. Filters at consumer side (`--source`, `--type`) make narrowing cheap. |
| Threshold-gated events fire repeatedly during a sustained incident (paging spam) | Rising-edge semantics: only emit when the count crosses the threshold upward from below, not every reconcile cycle while the count stays above. |
| Schema drift over time as we add fields | `format_version: 1` floor + the §2 backwards-compat rules. Code review checks payload changes against this. |
| Consumer writes a tight poll loop and bypasses the 1Hz floor | Already an operational rule (in the Skill). Could add a daemon-side rate limit on cursor reads, but probably overkill. |
| Cursor races with retention prune (cursor falls behind > retention_days, events vanish before consumer sees them) | Document the gotcha in the broker guide. Real audit consumers should poll more often than retention; one-shot replays should use `--since-cursor <token>` not a long-idle named cursor. |
| Multi-root setups have ambiguous per-root vs daemon-wide events (e.g. `daemon.started` is daemon-wide; `reconcile.ran` is per-root) | Convention: events that ARE per-root carry `watch_root` in payload; events that are daemon-wide leave it empty. Document in the broker guide. |

---

## 6. Recommended decisions on the OQs from the prior doc

Per the user's "best-practice engineering without over-engineering" directive:

- **OQ-1 (explicit `digest.created` event):** **YES.** Symmetry payoff is large; cost is one `InsertEvent` call after `InsertDigest` commits. Many consumers (3.7, 3.8, 3.6, 3.4) rely on it.
- **OQ-2 (separate `emit_thresholds` map):** **YES.** Keeps the toggle concern (boolean overrides) separate from the tuning concern (numeric thresholds). Three thresholds only: `events_failed`, `events_stuck`, `reconcile_drift`. Don't generalise to "configurable threshold for any event type" — that's over-engineering.
- **OQ-3 (per-class vs per-event override granularity):** **per-class for v1.** Classes are small enough that all-or-none per class is fine. Revisit if users actually request per-type. Class-to-type map is the central authority.
- **OQ-4 (per-root emit profiles):** **uniform for v1.** Per-root multiplies config complexity for no demonstrated demand. If a user asks, add later as `emit_profile_per_root: {code: verbose, docs: standard}`. Until then, one knob.

---

## 7. Why this isn't over-engineering

Worth saying explicitly because the §3 catalog is long and the §4 config shape adds knobs.

- **Each event type is single-digit LOC** — one constant, one emit call. Adding 20 event types is ~200 LOC of mechanical work.
- **The tier mechanism is one small package** — `internal/events/profile.go` with a class-to-tier map and a `ShouldEmit(type, profile, overrides) bool` function. Maybe 80 LOC + 30 tests.
- **Config plumbing is ~40 LOC** in 4 places (config.go field, file.go loader, env_glue.go, main.go flags) — mirrors what `OnDigest` already does.
- **No consumer breaks.** Adding event types is purely additive. Existing tooling works. Existing tests still pass.

The over-engineering trap would be:
- ❌ Building a plugin loading system (won't do)
- ❌ Building per-event-type configuration with regex matching (per-class is enough)
- ❌ Building a separate event-bus abstraction layer (SQLite IS the bus)
- ❌ Building exactly-once delivery semantics (at-least-once is the contract; consumers handle dedup)
- ❌ Exposing every internal counter as a tunable threshold (three is enough)

What we ARE building is the smallest viable expansion of the existing journal to cover the consumer catalog in §3. That's appropriately scoped.

---

## 8. Forward-looking notes (not in scope, but worth flagging)

These come up naturally in the broker framing but are NOT part of SW-AGENT-30:

- **Pluggable storage backend** — today SQLite; could be Postgres for multi-host. Don't build.
- **Cross-host event federation** — gRPC/NATS bridge to mirror events between sharedwatch instances. Don't build.
- **Schema registry** — formal JSON schema files for each event type, version-tracked. Worth doing eventually; not now.
- **Consumer registry** — daemon-side awareness of who's tailing what, for "what cursors exist" reporting. `events cursor list` already covers most of this.
- **Hook composability beyond shell** — e.g. structured hook configs in YAML with multiple actions. The shell wrapper-script pattern (`--on-digest /path/to/script.sh`) is already the supported way to chain.

If any of these surface as real user requests later, they get their own discussion docs.

---

## 9. Cross-references

- Principle: [`event-and-hook-surface-expansion-20260524.md`](./event-and-hook-surface-expansion-20260524.md) §0
- Implementation tasklist: [`../../tickets/tasklist_20260525_015121.md`](../../tickets/tasklist_20260525_015121.md) (this doc's companion)
- Existing hook surface: [`hooks-discussion-20260524.md`](./hooks-discussion-20260524.md) + [`../guides/on-digest-hooks.md`](../guides/on-digest-hooks.md)
- Coord-events background: [`intent-events-discussion-20260524.md`](./intent-events-discussion-20260524.md) (SW-AGENT-24 — now folded into SW-AGENT-30's scope)
- Cursor gotchas: [`cursor-filter-change-discussion-20260524.md`](./cursor-filter-change-discussion-20260524.md) (SW-AGENT-28 — separate ticket; orthogonal)
