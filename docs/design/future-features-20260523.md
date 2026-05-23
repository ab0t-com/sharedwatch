# sharedwatch — predicted future feature requests (next 12 months)

**Date:** 2026-05-23
**Author:** claude (post-v0.8.0)
**Status:** speculative product backlog — predictions, not commitments
**Purpose:** anticipate the feature requests an AI-agent customer base is likely to file once they've used v0.8.0 in production for a quarter. Helps the team triage incoming requests against an already-considered baseline.

## How this list was built

Each feature represents a predicted *complaint* from an agent or its operator after real use of v0.8.0. The ranking weights three things:

- **Usage frequency** — how often will the missing feature bite someone, per day?
- **Need urgency** — when it bites, how painful is its absence? Is there a workaround?
- **Design-profile fit** — does the feature match sharedwatch's identity (calm, pull-based, single-host, durable, additive)? Features that conflict with the ethos rank lower even if demanded.

Each entry includes the **predicted verbatim complaint** the agent or operator would file. If the complaint sounds familiar already, treat it as a near-term priority.

## At a glance

| # | Feature | Usage | Need | Fit | Effort | Notes |
|---|---|---|---|---|---|---|
| 1 | Hot reconfig of roots (no restart) | very high | high | ⚠ medium | ~2d | the "obvious" papercut |
| 2 | `events watch` push surface (long-poll/SSE) | very high in fleets | high | ⚠ medium | ~1.5d | bends "pull-only" but realistic |
| 3 | Stale-lock auto-detection | universal post-crash | high | ✓ perfect | ~0.5d | already filed; v0.8.1 |
| 4 | Per-actor seen markers (read receipts) | high in handoffs | high | ✓ good | ~1d | closes "did peer actually see it?" |
| 5 | `conflicts check <path-glob>` pre-edit query | high per agent edit | high | ✓ perfect | ~0.5d | one-shot intent+lease+recent combo |
| 6 | `session show <id>` walker | high in debug | medium | ✓ good | ~0.5d | session-scoped event chain |
| 7 | Causal-chain traversal (`events chain <id>`) | medium in debug | high when needed | ✓ good | ~1d | walk ref_event_id forward + back |
| 8 | First-class `--tag <t>` filter | high | medium | ✓ good | ~0.5d | `tags[]` doesn't work with --payload-key today |
| 9 | Content replay / blob store | medium | high when wanted | ⚠ low (storage) | ~2-3d | the "what did the file look like?" |
| 10 | Actor.focus filter shortcut | medium per agent | medium | ✓ good | ~0.5d | `--for-actor X` = `--path-glob $X.focus` |
| 11 | `make dogfood` target | weekly in CI | medium | ✓ perfect | ~0.5d | already filed |
| 12 | Multi-cursor union view | niche, fleet-coordinators | medium | ✓ good | ~1d | "what's new across my N cursors" |
| 13 | Publisher adapters (NATS / Redis Streams / webhook) | high when outgrowing 1 host | high then | ⚠ low (multi-host) | ~2d each | bridges the single-host wall |
| 14 | Per-actor event budgets / rate-limit | low until first runaway | high once needed | ✓ good | ~1d | defensive |
| 15 | Trace mode (`--trace-actor X` / `--trace-path P`) | niche, debug | high when needed | ✓ good | ~0.5d | per-actor or per-path verbose logging |

Total estimated work: ~17 engineer-days across 15 features. Realistically, 4–6 of these will land in any given quarter depending on actual demand signal.

---

## 1. Hot reconfig of roots — no restart required

**Predicted complaint:** *"Every time I want to add a new workspace folder to the daemon I have to kill `sharedwatch run` and restart it. That kills my cursor positions on inflight queries and creates a coordination gap. Can I just `sharedwatch root add <label>=<path>` while the daemon is running?"*

**Why the rank:** Multi-agent coordinators add and drop workspaces *daily* — when a new task spawns, a new root is needed. Restart-on-change is the single biggest UX papercut for any multi-folder user.

**Design-profile tension:** We explicitly listed "hot reconfiguration of root list at runtime" as out-of-scope in SW-AGENT-3. Reason: "restart is fine, standard pattern." That reasoning was right for the single-host calm-tool ethos but wrong for the multi-agent coordinator use case. As soon as v0.8.0 hits a real fleet this will be the #1 ask.

**Shape:**
```bash
sharedwatch root add <label>=<path>     # adds and starts watching
sharedwatch root remove <label>          # stops watching; events stay in journal
sharedwatch root list                    # already exists as `roots` / `status --json | jq .roots`
```
Implementation: daemon reads config + maintains a roots-set in `runtime_state`; a new control endpoint or a polled `runtime_state` field tells the watcher loop to add/remove a root. Snapshot lookup is already keyed `(source, root)`, so addition is a clean cold-start; removal just stops emitting (kept rows are untouched).

**Risks:** symlink/path-overlap edge cases get more exposure when paths change at runtime. Mitigation: refuse hot-add of a path that overlaps an existing root.

---

## 2. `events watch` push surface (long-poll / SSE)

**Predicted complaint:** *"I'm polling `events list --cursor-name X` every 5 seconds and burning context window on empty responses. Why can't I open a stream and have new events pushed?"*

**Why the rank:** As soon as someone deploys 3+ agents in active mode (5s cadence), the polling tax becomes obvious. Token cost compounds: 5 agents × 12 polls/min × 8h day = 28,800 polls/day with most returning empty.

**Design-profile tension:** The whole pitch is "calm, pull-based." Push contradicts the brand. But the implementation can stay calm — long-poll (5–30s hold) is push-like at the wire but pull at the semantics. Or SSE (Server-Sent Events) which is a single open connection that the agent can keep alive.

**Shape:**
```bash
sharedwatch events watch [filter flags identical to events list] --format jsonl
# blocks; emits one JSONL row per matching event as it lands
# Ctrl-C / EOF / explicit timeout to stop
```
Could be implemented as: `events list` loop with a server-side wait-for-cursor-advance condition variable. Or a thin HTTP endpoint (`POST /events/watch` over a Unix socket) for IPC-friendly use.

**Risks:** breaks the simple-CLI ethos. Mitigation: the new command exits cleanly on signals; remains a single subcommand among many; doesn't change any existing surface.

---

## 3. Stale-lock auto-detection

**Predicted complaint:** *"My `sharedwatch run` crashed and now it won't restart — says the lock is held. I had to manually `rm <data_dir>/sharedwatch.lock`. Shouldn't it detect that the PID in the lock is dead?"*

**Why the rank:** Universal — every operator hits this the first time `run` exits ungracefully. Already filed in the dogfood findings as a v0.8.1 candidate.

**Design-profile fit:** Perfect. A "boring tool that doesn't need babysitting" should self-heal an orphaned lock.

**Shape:** On `run` startup, if the lock file exists and contains a PID that isn't alive (kill(pid, 0) fails with ESRCH), reclaim it with a single warn log line: `reclaimed stale lock from dead PID 12345`. If the PID IS alive, refuse to start (current behaviour).

**Risks:** Race condition between the staleness check and another process starting up. Mitigation: O_EXCL + lock file naming with both PID and start-time, or use flock() instead of a sentinel file.

---

## 4. Per-actor seen markers (read receipts)

**Predicted complaint:** *"I emitted a handoff event for claude-code 20 minutes ago. They've heartbeated since, but I don't know if they actually saw my event or just heartbeated for some other reason. Can I get a per-event acknowledgement?"*

**Why the rank:** Handoff workflows live or die by this. Today the closest answer is "wait for a `ref_event_id` response," but that confirms reaction-not-reading. Different signal.

**Design-profile fit:** Good. Per-actor cursors already exist; a "max event id consumed by actor X" view is a thin layer over them.

**Shape:**
```bash
# Implicit: cursor advance IS the seen marker (already true, just exposed)
sharedwatch events seen --actor claude-code --event evt_abc123
  # → boolean

sharedwatch events seen-by --event evt_abc123 --format jsonl
  # → list of actors whose cursor is past this event
```
Implementation: walk `cursors` table for any cursor whose `(created_at, id)` >= the event's. Bonus: each agent's cursor name encodes the actor by convention (`<actor>-<task>`), so the query is straightforward.

**Risks:** convention-driven (agents that don't use `<actor>-...` cursor names are invisible). Could promote to an explicit `cursors.actor_id` column.

---

## 5. `conflicts check <path-glob>` pre-edit query

**Predicted complaint:** *"Before I edit `auth/login.go` I want to check: any active leases? Any active intents from peers? Any recent events from other actors? That's three separate queries every time. Can I get one?"*

**Why the rank:** Every cooperative agent does this dance before risky edits. Three queries → one query = ~70% token reduction for the "don't step on toes" pattern.

**Design-profile fit:** Perfect. Composes existing primitives; no new state.

**Shape:**
```bash
sharedwatch conflicts check <path-glob> [--actor <self>] [--since 5m] [--format json]
# returns:
{
  "format_version": 1,
  "path_glob": "auth/login.go",
  "leases": [{"lease_id":"...", "actor_id":"claude-X", ...}],
  "intents": [{"intent_id":"...", "actor_id":"claude-Y", ...}],
  "recent_events": [{"id":"...", "actor":"claude-Z", "type":"file.modified", "observed_at":"..."}],
  "summary": "1 lease (claude-X), 0 intents, 2 recent events from {claude-Z}"
}
```
`--actor <self>` excludes the caller's own claims from the response.

**Risks:** none — pure read aggregation. Could even be implemented as a thin wrapper around three existing list calls.

---

## 6. `session show <id>` walker

**Predicted complaint:** *"Session sess-2026-05-22-xyz produced 47 events across 3 paths and 5 hours. I want to see the whole arc in one call — what files, what types, in what order, with what intents — not a raw event dump."*

**Why the rank:** Debug query of choice when a multi-agent flow goes wrong. Today: `events list --payload-key session --payload-value <id>` + jq filtering.

**Design-profile fit:** Good. Session is already a payload key; just dedicated rendering.

**Shape:**
```bash
sharedwatch session show <session-id> [--format text|json]
# text rendering: timeline with one line per event, grouped by path,
#                  with actor + intent + ref_event_id annotations
```

**Risks:** none.

---

## 7. Causal-chain traversal (`events chain <id>`)

**Predicted complaint:** *"This event references `evt_abc123` which references `evt_xyz789` which references… how do I walk the whole causal chain in one call?"*

**Why the rank:** When debugging "why did claude-Z do this?", the answer is a chain of `ref_event_id` hops. Today: N+1 query problem (each event requires a follow-up query for its parent).

**Design-profile fit:** Good. Read-only; composes existing primitives.

**Shape:**
```bash
sharedwatch events chain <event-id> [--direction up|down|both] [--max-depth N]
# returns the parents (--up: follow ref_event_id) and/or children
#   (--down: find events whose ref_event_id equals each event)
```

**Risks:** cycles in the causal graph (an agent that ref's its own ancestor). Mitigation: max-depth + cycle detection.

---

## 8. First-class `--tag <t>` filter on `events list`

**Predicted complaint:** *"`--tag refactor` doesn't work. The skill says tags are conventional but I have to do `sharedwatch sql "SELECT * WHERE json_extract(payload_json, '$.tags') LIKE '%refactor%'"` which is gross and slow."*

**Why the rank:** Tags are documented as first-class in the v1 schema but querying them requires the SQL escape hatch (because `--payload-key tags --payload-value X` only does flat-key string equality, not list-contains semantics).

**Design-profile fit:** Good. Closes a documented-but-unimplemented expectation.

**Shape:**
```bash
sharedwatch events list --tag refactor --tag auth --tag-match all|any
# matches events whose payload_json.tags array contains the supplied tags
# --tag-match: all (default) requires every tag, any matches if at least one
```
Implementation: post-fetch filter in Go after parsing `payload_json.tags`. Could promote to JSON1 `json_each` when we decide to depend on it.

**Risks:** none.

---

## 9. Content replay / blob store

**Predicted complaint:** *"`events list` tells me `auth/login.go` was modified by claude-X at 14:32, hash=abc. I want to see WHAT changed. The file is now at v3; I need v2 to compute the diff. Can sharedwatch store the bytes?"*

**Why the rank:** When agents move from "react to changes" to "reason about changes," content becomes essential. Today the agent has to re-read the file (which is now in a different state).

**Design-profile tension:** Explicitly out of scope today ("sharedwatch does NOT capture file contents"). Storage cost is real — a 10 MB binary modified hourly becomes 240 MB/day. But agents will ask. Strongly.

**Shape:**
```bash
# Opt-in: enable per-config or per-root
sharedwatch run --capture-content [--content-max-size 10MB]

sharedwatch content show <event-id>           # bytes of the file at that event
sharedwatch content diff <event-id-A> <event-id-B>   # text-diff between two snapshots
```
Storage: a `content_blobs(content_hash PK, bytes BLOB, size INT, captured_at TEXT)` table; events reference blobs by `content_hash`. Same hash → dedup. Retention: TTL by age, with a `--keep-latest-N-per-path` knob.

**Risks:** disk cost; agents asking "why is my queue.db 5 GB?" Mitigation: opt-in flag; clear documentation that this is the heavyweight option; retention defaults aggressive.

---

## 10. Actor.focus filter shortcut

**Predicted complaint:** *"My actor has `focus='auth/**'` registered. To filter events to my focus I have to manually pass `--path-glob 'auth/**'` every time. Why doesn't it auto-scope?"*

**Why the rank:** Once actors register a focus, the most common query is "events in my focus area since I last checked." Currently agents have to maintain the glob in their prompt context.

**Design-profile fit:** Good. Conveys the relationship the actor already declared.

**Shape:**
```bash
sharedwatch events list --for-actor claude-X
# Looks up actor's focus glob, applies it as --path-glob automatically.
# Combinable with other filters (--since, --cursor-name, etc).

sharedwatch overview --for-actor claude-X
# Scopes the L1 view to just this actor's focus area.
```

**Risks:** if an actor doesn't have a focus set, the flag is a no-op (warn, then return everything).

---

## 11. `make dogfood` target wrapping the runner

**Predicted complaint:** *"There's a test_dogfood.md with 18 scenarios but no easy way to run them. I had to look at last quarter's branch report to find the runner script."*

**Why the rank:** Already filed as a follow-up. Universal — every release should re-run the dogfood as a regression baseline.

**Design-profile fit:** Perfect. Aligns with the "boring tool" ethos of CI-friendly automation.

**Shape:**
```bash
make dogfood                  # runs all 18 scenarios, dumps to ./dogfood-artifacts/
make dogfood SCEN=9           # runs just one
```
The runner script already exists at `/tmp/sharedwatch-dogfood/run_all.sh`; folding it into the Makefile + committing it to `scripts/dogfood/` is the work.

**Risks:** none — purely additive.

---

## 12. Multi-cursor union view

**Predicted complaint:** *"I have 5 named cursors (one per task) and want to see 'what's new across all of them' in one call. Today I have to read each cursor separately."*

**Why the rank:** Coordinator agents managing many sub-tasks will ask. Niche but predictable. May overlap with overview if the coordinator is willing to drop the per-task scoping.

**Design-profile fit:** Good. Reuses existing cursor primitive.

**Shape:**
```bash
sharedwatch events list --cursor-name-prefix claude-X-  
# matches all cursors starting with the prefix; returns events newer than the
# EARLIEST cursor position so each cursor's slice is covered
# advances each matching cursor on read (or --no-advance to peek all)

sharedwatch events cursor union <name1> <name2> ...
# explicit list form; same advance semantics
```

**Risks:** advance semantics get subtle when cursors are at different positions. Document carefully; default to no-advance.

---

## 13. Publisher adapters (NATS / Redis Streams / generic webhook)

**Predicted complaint:** *"We outgrew one host. We have 3 sharedwatch instances on 3 machines. How do agents see events across all of them?"*

**Why the rank:** The wall sharedwatch's design explicitly hits. We told ourselves the answer was "publish to a real queue instead." This feature makes that one CLI flag rather than a custom integration.

**Design-profile tension:** Conflicts with "single-host." But it's the principled out — sharedwatch stays local, just publishes outward. Agents subscribing to the queue can see cross-host events.

**Shape:**
```bash
# In config or on `run`:
sharedwatch run --publish nats://localhost:4222 --publish-topic 'sharedwatch.events'
sharedwatch run --publish redis://localhost:6379 --publish-stream 'sharedwatch:events'
sharedwatch run --publish-webhook https://internal/sharedwatch-collector
```
Each adapter receives a fixed envelope (the JSON form of `events.Event`) per event after it's committed locally. At-least-once semantics; offsets tracked in `runtime_state`.

**Risks:** scope creep — once we ship one adapter we'll be asked for more. Mitigation: start with NATS (the simplest), document the wire format precisely, let community contribute others.

---

## 14. Per-actor event budgets / rate-limit

**Predicted complaint:** *"claude-runaway-bot emitted 50,000 events in 10 minutes. The journal grew 100 MB; the consumer fell behind; other agents couldn't get their handoffs through. Can I cap per-actor emission?"*

**Why the rank:** Low usage until the first runaway, then critical. Defensive feature.

**Design-profile fit:** Good. Boring infrastructure protection.

**Shape:**
```bash
# Config:
# actor_event_budget_per_minute: 100   # global default
# actor_event_budgets:
#   claude-runaway-bot: 10             # tighter for known noisy actor

# When an actor exceeds the budget, subsequent emits are:
#   - dropped + counted (default)
#   - or marked status='throttled' (alternative)
# In either case, a slog.Warn fires.
```

**Risks:** the actor whose work IS legitimately bursty gets throttled. Mitigation: per-actor budgets, status-marker (not drop) by default, conservative global default.

---

## 15. Trace mode (`--trace-actor X` / `--trace-path P`)

**Predicted complaint:** *"Something is wrong with how claude-A's events are flowing. Can I get verbose debug logs just for claude-A without flooding the journal with debug from everyone?"*

**Why the rank:** Niche — comes out during debugging, then turned off. Universal pain point for "single agent in a fleet is misbehaving" investigations.

**Design-profile fit:** Good. Operator-facing observability; doesn't bleed into the journal data.

**Shape:**
```bash
sharedwatch run --trace-actor claude-A
sharedwatch run --trace-path 'auth/**'
sharedwatch run --trace-actor claude-A --trace-path 'auth/**'   # both
```
Implementation: at event-emit and event-claim points, if the event matches either trace selector, emit a `slog.Debug` line with full event context regardless of the global log level.

**Risks:** none.

---

## Things deliberately NOT on this list (and why)

- **Real concurrency control / OS-level locks.** Leases are advisory by design; promoting to hard locks would require kernel hooks or a coordination service. Out of character.
- **Web UI.** No agent needs it. Humans currently use `digest show`; a thin TUI could come if a customer asks, but it doesn't predict.
- **Multi-tenancy / authentication.** Different product. sharedwatch trusts the local filesystem.
- **Cross-host federation (built-in, not via publisher).** The publisher adapters (#13) cover this through external queues; building a full cross-host federation inside sharedwatch contradicts the single-host doctrine.
- **GUI for the digest.** The text form is fine; an HTML render would be cosmetic.
- **Plugin system.** Premature abstraction. If we get 5+ adapters worth of demand, then consider.
- **Encrypted-at-rest journal.** Filesystem-level encryption (LUKS) covers it. Application-level encryption adds complexity for marginal value at this scale.

---

## How to use this list

- **Pre-roadmap**: when a request lands, check if it's already on this list. If yes, the rank tells you the team's prior on importance. If no, that's a real surprise worth attending to.
- **Quarterly planning**: top 5 + any "predicted" complaint that has actually been filed = strong v0.9.x candidates.
- **Customer conversations**: when a customer says "wouldn't it be great if X," check the rank. If X is high-ranked, you have prior thinking ready; if it's low, gather more signal before promising.
- **Re-review every 6 months**: ranks will shift as actual usage data comes in. Promote anything whose predicted complaint shows up >3 times in real tickets; demote anything that never gets requested.

The point of the list is not to commit. It's to refuse to be surprised.

---

## Appendix: chat-message summary (kept for context)

The following is the concise summary produced alongside the doc itself, kept here as a quick-reference epilogue. The detail in the per-feature sections above is authoritative; this appendix is the elevator-pitch view.

### The ranked list at a glance

| # | Feature | Predicted complaint (short) | Usage × Need × Fit |
|---|---|---|---|
| 1 | **Hot reconfig of roots** | "Why do I have to restart to add a workspace?" | very-high × high × ⚠ medium |
| 2 | **`events watch` push surface** | "I'm burning context on empty 5s polls" | very-high (fleets) × high × ⚠ medium |
| 3 | **Stale-lock auto-detection** | "Crash leaves orphaned lock; manual `rm` needed" | universal × high × ✓ perfect |
| 4 | **Per-actor seen markers** | "Did claude-X actually SEE my handoff?" | high × high × ✓ good |
| 5 | **`conflicts check <path-glob>`** | "Three queries every time I want to edit safely" | high × high × ✓ perfect |
| 6 | **`session show <id>` walker** | "Walking session X requires 5 queries + jq" | high × medium × ✓ good |
| 7 | **Causal-chain traversal** | "Walking ref_event_id chains is N+1 queries" | medium × high × ✓ good |
| 8 | **First-class `--tag <t>` filter** | "Tags are documented but I have to use SQL" | high × medium × ✓ good |
| 9 | **Content replay / blob store** | "I need to see what the file LOOKED LIKE at event N" | medium × high × ⚠ low (storage) |
| 10 | **Actor.focus filter shortcut** | "Why doesn't `--for-actor X` auto-scope?" | medium × medium × ✓ good |
| 11 | **`make dogfood` target** | "I can't find the dogfood runner" | weekly (CI) × medium × ✓ perfect |
| 12 | **Multi-cursor union view** | "5 named cursors, one merged view please" | niche × medium × ✓ good |
| 13 | **Publisher adapters (NATS/Redis/webhook)** | "We outgrew one host — how do agents see cross-host?" | high (when triggered) × high × ⚠ low (multi-host) |
| 14 | **Per-actor event budgets** | "claude-runaway-bot just emitted 50k events" | low until first runaway × high then × ✓ good |
| 15 | **Trace mode (`--trace-actor` / `--trace-path`)** | "One agent is misbehaving; need debug just for it" | niche × high (when needed) × ✓ good |

### Design-profile tensions surfaced

Three of the top 15 conflict with the original design profile, by varying degrees:

- **#1 Hot reconfig** — we explicitly said "restart is fine, standard pattern." That stance was right for v1's calm-tool ethos but wrong for the multi-agent coordinator use case. This will be the most-asked feature within weeks of v0.8.0 deployment.
- **#2 Push** — bends "pull-only" but the long-poll/SSE implementation can stay calm at the semantics layer. Reframe: not "interrupt me" but "park the call until something arrives."
- **#13 Publisher adapters** — conflicts with "single-host" but is the principled out: sharedwatch stays local; publishes outward; the team's distributed substrate (NATS/Redis) carries the events to other hosts.

The other 12 are pure additive plays that match the design profile cleanly.

### Things deliberately NOT on the list (rationale documented)

- Real concurrency control / OS locks (leases are advisory by design)
- Web UI (no agent needs it; digest covers human needs)
- Multi-tenancy / auth (different product)
- Cross-host federation built-in (publisher adapters cover this)
- Plugin system (premature)
- Encrypted-at-rest journal (LUKS covers it)

### How to use the list

- **Pre-roadmap**: when a request lands, check the rank as the team's prior.
- **Quarterly planning**: top 5 + any actually-filed complaint = strong v0.9 candidates.
- **Customer conversations**: high-ranked items get a thoughtful "we've considered this"; low-ranked items get "tell me more" before we promise.
- **Re-review every 6 months**: promote anything whose predicted complaint shows up 3+ times in real tickets; demote anything no one asks for.
