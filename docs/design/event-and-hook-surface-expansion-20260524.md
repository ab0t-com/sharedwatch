# sharedwatch — event + hook surface expansion roadmap

**Date:** 2026-05-24
**Status:** design discussion (not a decision, not a ticket — yet)
**Author:** claude (this session)
**Companions:**
  - [`hooks-discussion-20260524.md`](./hooks-discussion-20260524.md) — the discussion that produced SW-AGENT-29 (`--on-digest`)
  - [`intent-events-discussion-20260524.md`](./intent-events-discussion-20260524.md) — Q1 (the coord-events stream) deferred from SW-AGENT-23
  - [`cursor-filter-change-discussion-20260524.md`](./cursor-filter-change-discussion-20260524.md) — SW-AGENT-28 candidate

We shipped `--on-digest` in v0.1.0. The user asked the natural next question: "what other hooks should we add?" This doc thinks about it deeply, because the right answer isn't a list — it's a principle that determines what should be a hook, what should be a journal event, and what should stay where it is.

---

## 1. The problem

Right now sharedwatch produces many signals an integrator might want to react to, and they live in **wildly different places**:

| Signal | Where it lives today | Reachable how? |
|---|---|---|
| `file.created` / `.modified` / `.deleted` / `.renamed` | Events journal | `events list --cursor-name <me>` ✓ |
| Reconciler-recovered events | Events journal (source=`reconciler`) | Same ✓ |
| Digest created | Digests table + new `hook.completed`/`hook.failed` events | `digest list` OR `events list --type hook.completed` ✓ |
| Lease violation (watcher saw a foreign write inside a held lease) | `slog.Warn` log line only | **Log scraping. Painful.** |
| Mode transition active↔passive | Runtime state row | `status --json` polling only — no signal |
| Active TTL expiry | Implicit in mode transition above | Same |
| Daemon startup / shutdown | `slog.Info` log lines | Log scraping |
| Retention prune (events / digests / snapshots / actors / intents / leases) | Silent — no output | Inspect counts over time, or compare `events list` results |
| Events stuck in `processing` state | `events recover-stuck` reports on demand | Pull only |
| Failed events accumulating | `events list --status failed` | Pull only |
| Intent declared / revoked | `intents` table | `intent list` polling only |
| Lease granted / released / renewed | `leases` table | `lease list` polling only |
| Actor heartbeat / actor went stale | `actors` table | `status --actors` polling only |
| Snapshot taken / snapshot failed | Internal; no surface | Invisible |

The pattern is clear: **file events are the only signal class that's a first-class journal citizen.** Every other thing the daemon does is either invisible, log-scraped, or pull-polled. That's the gap.

---

## 2. The architecture choice — hooks vs journal events

When someone says "I want to react to X happening," there are two ways to give them that:

### (A) Hook — flag wired to a shell command

```bash
sharedwatch run --on-X 'curl -X POST ...'
```

**Pros**
- Cheap to use: one flag, no consumer code
- Stand-alone — no DB, no cursor state to maintain
- Familiar to anyone who's used git hooks / systemd ExecStartPost / cron

**Cons**
- Fire-and-forget — if your hook misses one fire, that signal is lost
- Single consumer per daemon (you can fan out via a wrapper script, but it's manual)
- Adds CLI surface area; each hook is a new flag and a new doc section
- Doesn't scale across machines (the hook runs on the daemon's host)

### (B) Journal event — row in the events table, queried via cursor

```bash
sharedwatch events list --cursor-name my-watcher --type X --json
```

**Pros**
- **Multi-consumer**: any number of independent processes can tail with their own cursors
- **Durable**: missed events accumulate; restart picks up where you left off
- **Historical**: queryable across time, joinable with other event types
- **Composable**: filters, projections, payload-key narrowing all work uniformly
- **Existing tooling**: every doc, recipe, and skill already covers the cursor pattern

**Cons**
- Setup cost: writing a tail loop is ~10 lines of bash vs. one flag
- Latency: depends on consumer poll cadence (~1Hz minimum per project rules)
- Storage: events accumulate until retention prunes them (this is also a *pro* for audit)

### The principle

> **Journal events are the architectural choice. Hooks are a UX shortcut for cases where shell-pipe ergonomics beat a tail loop.**

This matters because the project's central promise is "the journal IS the integration surface." Hooks are a *layer on top* of the journal — they handle the cases where the layer is worth it, not a replacement for it.

The right ordering for any new signal is:
1. **First**, put it in the journal as an event type with a clean payload.
2. **Then** decide whether a hook shortcut is worth the additional surface.
3. **Default to "no"** on the hook unless the signal is high-traffic enough that "write a tail loop" is genuinely worse UX than "type a flag."

This inverts what users initially ask for ("can I have a hook on X?") into the more durable question ("can X land in the journal so I can react to it any way I want?").

---

## 3. Catalog of proposed journal event additions

Each entry below: the event type, what triggers it, the payload shape, who would use it, and what becomes possible once it's in the journal. **Source** is the new `events.Source` value to add (some reuse `daemon`, `mode`, etc. — needs decision).

### 3.1 Lifecycle — when the daemon starts, stops, or restarts

```
daemon.started     source=daemon  payload: {version, pid, watch_paths[], mode, started_at}
daemon.stopping    source=daemon  payload: {reason: "sigterm"|"sigint"|"explicit-stop", pid}
daemon.crashed     source=daemon  payload: {prior_pid, recovered_at, prior_lock_age}
                                  (written on the NEXT startup when a stale lock is detected)
```

**Who uses it / why:**
- **Ops / SRE:** "When did the daemon last restart?" Pull this from the journal instead of `journalctl`. Easy to graph uptime, restart frequency.
- **Service registry:** Wire `--on-startup` to register with Consul / etcd; `--on-shutdown` to deregister.
- **Watchdogs:** Detect crashes by `daemon.crashed` events appearing in the journal.
- **AI agents:** Know whether the daemon has been continuously running since your last visit (cursor crosses zero `daemon.started` events = continuous; sees one = something restarted).

**Compatible with calm contract:** Yes — 2 fires per daemon lifetime, less for a long-running process. No backpressure risk.

### 3.2 Mode transitions — active vs. passive

```
mode.changed       source=mode  payload: {from: "passive", to: "active", trigger: "explicit"|"ttl-expired"|"event-burst", ttl?: "30m"}
mode.ttl_extended  source=mode  payload: {expires_at, extension: "5m"}
```

**Who uses it / why:**
- **AI agents:** Adjust your poll cadence to match the daemon's. Active mode = team is working = poll faster; passive = quiet = back off.
- **Dashboards:** Light up a "team is busy" indicator. Trivial to wire as a heatmap of mode-active periods over a day.
- **Cost optimisation:** If you have a poll-driven downstream system (e.g. spending API tokens to query the journal), match its cadence to sharedwatch's mode.
- **Compliance:** Track when the system was actively monitored vs. idle.

**Compatible with calm contract:** Yes — at most a few fires per hour in normal usage. Naturally rate-limited by the mode-transition logic.

### 3.3 Reconcile pass observability

```
reconcile.ran                source=reconciler  payload: {duration_ms, events_recovered, snapshots_taken}
reconcile.drift_detected     source=reconciler  payload: {watch_root, missing_count, threshold}
                                                 (only emitted when missing_count > threshold)
reconcile.recovered_threshold source=reconciler payload: {events_recovered, threshold, period}
                                                 (per-pass; only when recovered > N)
```

**Who uses it / why:**
- **Ops:** "Is my watcher catching everything?" If `reconcile.drift_detected` fires regularly, the watcher path is unhealthy (filesystem quirks, missed inotify events, very high event rate). It's a real diagnostic signal.
- **AI agents (advanced):** Use `reconcile.ran` durations to detect when the daemon is under load.
- **Tuning:** If `reconcile.recovered_threshold` fires often, your `--coalesce-window` may be too long for your workload — events are getting missed by the watcher path and only caught on the periodic reconcile.

**Compatible with calm contract:** Yes — one event per reconcile cycle (default 30 min). Threshold-gated variants only emit on notable conditions.

### 3.4 Failure surface — stuck, failed, retried

```
events.stuck_detected   source=daemon  payload: {count, oldest_age_secs}
                                       (emitted on every reconcile cycle when > 0)
events.failed_threshold source=daemon  payload: {count, threshold}
                                       (only when failed-event count crosses N upward)
events.retried_batch    source=daemon  payload: {count, after_failures}
                                       (when `events retry` requeues N events)
```

**Who uses it / why:**
- **On-call alerting:** Page when failed events accumulate past N (indicates a stuck consumer or a poison-pill event).
- **Self-healing pipelines:** Watch for `events.stuck_detected`; auto-trigger `events recover-stuck` after a threshold.
- **SLO tracking:** Track failure rates as a derived metric.

**Compatible with calm contract:** Yes — threshold-gated to avoid noise. The reconciler is the natural emit site since it runs the recovery check anyway.

### 3.5 Retention pass evidence

```
retention.ran   source=daemon  payload: {events_pruned, digests_pruned, snapshots_pruned, actors_pruned, intents_pruned, leases_pruned, duration_ms, retention_days}
```

**Who uses it / why:**
- **Compliance:** "Data was actually deleted at the expected interval." Auditable evidence that retention is happening.
- **Cost:** Track storage reclaim over time.
- **Debug:** When events go missing, check `retention.ran` to see if they were pruned vs. lost.

**Compatible with calm contract:** Yes — one per reconcile cycle.

### 3.6 Coordination surface (overlaps with deferred SW-AGENT-24)

The discussion already exists in `intent-events-discussion-20260524.md`. Briefly:

```
intent.declared   source=intent  payload: {actor, path_glob, task, intent_text, ttl_seconds, expires_at}
intent.revoked    source=intent  payload: {intent_id, actor, reason: "explicit"|"expired"}
intent.expired    source=intent  payload: {intent_id, actor}
                                 (emitted by reconcile when TTL passes)

lease.granted     source=lease   payload: {actor, path_glob, lease_id, ttl_seconds, expires_at, exclusive}
lease.released    source=lease   payload: {lease_id, actor}
lease.renewed     source=lease   payload: {lease_id, actor, new_expires_at, renewal_count}
lease.expired     source=lease   payload: {lease_id, actor}
lease.violated    source=watcher payload: {violator_actor, lease_actor, lease_id, path_glob, observed_event_id}
```

**Who uses it / why:**
- **AI agents:** Single biggest win. Right now agents can't subscribe to peer coordination — they poll. With these events, an agent can tail "any intent or lease activity in the last hour" with one cursor and react to peer plans in real time.
- **Lease violation alerts:** Today this is just a `slog.Warn`. As a journal event, agents can hook on `lease.violated` directly. AI agent A holds a lease on `auth/**`; agent B writes there; agent A reacts (abort, retry, escalate).
- **Workflow visibility:** Humans get a real-time view of "who is doing what" via the journal.

**Compatible with calm contract:** Yes — frequency is bounded by the rate at which agents declare/grant/revoke. Lease violations are bounded by the lease grant rate.

### 3.7 Actor registry

```
actor.registered      source=actor  payload: {actor, kind, focus, first_seen_at}
                                    (first heartbeat for a new actor_id)
actor.went_stale      source=actor  payload: {actor, last_heartbeat, stale_since_secs}
                                    (when reconcile detects past ActorTTL threshold)
actor.removed         source=actor  payload: {actor, last_heartbeat, removed_after_secs}
                                    (when reconcile prunes past 2× ActorTTL)
```

**Who uses it / why:**
- **Multi-agent coordination:** Know who's joined the system. Coordinator agents can adjust their delegation pattern as collaborators come and go.
- **Capacity planning:** "How many agents are typically active during business hours?"
- **Audit:** Track which agents touched the system over time.

**Compatible with calm contract:** Yes — first heartbeat per actor, then stale + removed events at their natural cadence (typically minutes, not seconds).

### 3.8 Cursor activity (questionable — see §5)

```
cursor.filter_changed  source=daemon  payload: {cursor_name, prior_filter_signature, new_filter_signature}
```

**Who uses it / why:**
- SW-AGENT-28 — the documented filter-change gotcha. If we implement detection, the warning could land here.
- Reviewers / auditors who want to know which agents are changing their query scope.

**Compatible with calm contract:** Maybe — frequency depends on agent behaviour. Could be very chatty if multiple agents reuse cursor names sloppily.

---

## 4. Proposed hook shortcuts (after the journal additions land)

These are the cases where shell-pipe ergonomics beat a tail loop. They're all wrappers over signals that exist in the journal per §3.

### 4.1 Tier 1 — clear value, low surface, fits the calm contract

**`--on-mode-change <cmd>`**

Shell command fired when the daemon transitions between `active` and `passive`. The mode-change event's JSON is delivered on stdin.

**Hooks-vs-tail comparison:** The tail-loop version is roughly:
```bash
sharedwatch events list --cursor-name mode-tail --type mode.changed --format jsonl |
  while read event; do echo "$event" | jq -c .payload_json | xargs -I {} sh -c '...' {}; done
```
Versus:
```bash
sharedwatch run --on-mode-change 'sh -c "..."'
```

The hook saves ~10 lines of bash for the most common case. **Worth shipping.**

**Recipes users would write:**
- `--on-mode-change 'echo $(jq -r .to) > /run/sw-mode'` — let other scripts read a flat file for "current mode"
- `--on-mode-change 'systemctl restart noisy-agent.service'` — restart something that needs to know about state change
- `--on-mode-change 'if [ "$(jq -r .to)" = active ]; then notify-send "team-active"; fi'` — desktop notify on team-active transitions only

**Cost:** ~30 LOC + 4-6 tests, mirrors `--on-digest` plumbing precisely. Half-day including dogfood.

---

**`--on-startup <cmd>` / `--on-shutdown <cmd>`**

Fired once at daemon start (after lock acquisition, before the main loop) and once at shutdown (before WaitForHooks, before lock release). Stdin: same lifecycle event JSON as the journal entry.

**Hooks-vs-tail comparison:** Tail-loop versions are awkward because they require the daemon to be running to read them, and the shutdown event arrives just before the daemon dies. The hook is the natural shape here.

**Recipes:**
- `--on-startup 'curl -s consul/register -d @-'` — register with service discovery
- `--on-startup 'curl -X POST https://watchdog/heartbeat'` — tell a watchdog "I'm alive"
- `--on-shutdown 'curl -s -X DELETE consul/dereg/$(jq -r .pid)'` — clean deregistration
- `--on-shutdown 'echo "stopped: $(jq -r .reason)" | mail -s sw-down ops@'` — email on every stop

**Cost:** ~50 LOC (two new flags, two call sites: lock acquisition and pre-WaitForHooks). Half-day.

---

### 4.2 Tier 2 — narrower audience, but the case is real

**`--on-failed-events <cmd> --on-failed-events-threshold <N>`** (default N=10)

Fires once per reconcile cycle when the count of `failed`-status events crosses the threshold upward. Stdin: `{count, threshold, oldest_failed_id, oldest_failed_age_secs}`.

**Hooks-vs-tail comparison:** The tail-loop version requires writing a debouncing accumulator — track the count, compare to a threshold, fire on rising edge. That's complex enough that the hook is worth shipping.

**Recipes:**
- `--on-failed-events 'curl pagerduty -d @-'` — page on accumulation
- `--on-failed-events 'sharedwatch events retry --max-retries 3'` — auto-recover with limit
- `--on-failed-events 'echo "ALERT $(date)" >> /var/log/sw-alerts.log'` — alerting log

**Cost:** ~80 LOC (rising-edge detection + threshold flag + tests). 1 day.

---

**`--on-lease-violation <cmd>`**

Fires when the watcher detects a file change inside an active lease held by a different actor than the writer. Stdin: lease violation event JSON.

**Hooks-vs-tail comparison:** AI agents specifically would want this — when a peer steps on their work, they want to react immediately, not on their next poll. For agents the latency saving is real (single-digit ms vs. seconds).

**Recipes (agent-specific):**
- `--on-lease-violation './abort-and-retry.sh'` — react to a lost race
- `--on-lease-violation 'jq -c .payload_json | curl coordinator-bot -d @-'` — escalate to a peer-discovery bot
- `--on-lease-violation 'echo "VIOLATION: $(jq -r .payload_json.violator_actor) wrote to $(jq -r .payload_json.path_glob)" >&2'` — verbose stderr for human debugging

**Cost:** ~40 LOC (the watcher already detects the violation; just need to fire the hook from that detection site). Bundle with the lease journal events. 1 day total.

---

### 4.3 Tier 3 — single-use but trivial to add (defer until asked)

**`--on-reconcile-drift <cmd>`** — fires on `reconcile.drift_detected`. Operational alert for "watcher is missing events." Threshold-gated.

**`--on-actor-stale <cmd>`** — fires on `actor.went_stale`. Coordinator alerting "this agent stopped checking in."

These add value but the audience is narrower than Tier 2. Build only on a request.

---

## 5. What we deliberately won't build

Users will ask for these. Important to have principled rejections documented so the answer isn't "we just haven't gotten to it" — the answer is "we considered it, and here's why no."

### 5.1 `--on-event <cmd>` (per-file-event hook)

The single most-likely request. Refuse, because:

- **Push-shape latency coupling.** A `make` save burst that writes 200 files in 5 seconds would fire 200 hooks. Even if each is fast, the consumer (which fires the hook) blocks on subprocess setup.
- **Fork-bomb risk.** A misconfigured hook (e.g. one that writes to the watched dir) creates a loop. Per-event hooks make this nearly impossible to guard against.
- **Defeats the journal-as-API pattern.** If you need per-event reactions, the right answer is a cursor tail. We've worked hard to make that pattern good; bypassing it is regression.
- **The `--on-digest` shape already covers ~80% of the use cases users *think* they want per-event for** — most "I need to react to file changes" really means "I need to react when *meaningful* activity has happened," which is exactly what a digest signals.

The exception clause: if a user demonstrates a concrete use case that **fundamentally** requires per-event latency AND can't be solved by a cursor tail, we reconsider. This has not happened in dogfood; SW-AGENT-29 was driven by a real user ask, so the bar is "show me the use case."

### 5.2 `--webhook <url>` (native HTTP POST)

Refuse. `--on-digest 'curl -X POST ...'` is strictly more flexible:
- Custom headers (auth tokens, content-type, idempotency keys)
- Retry policy (`curl --retry 3 --retry-delay 5`)
- TLS pinning, client certs, proxy settings
- Debugging (`-v`, output redirection)

A built-in webhook would have to re-implement each of these as flags or fall short on them. The curl recipe gets all of it for free. Document the recipe; don't build the flag.

### 5.3 Plugin system / Go plugin loading

Refuse. Massive machinery (binary compatibility, ABI versioning, isolation) for a single-host daemon. The hook surface IS the extensibility mechanism — keep it shell-shaped.

### 5.4 `--on-content-matches <regex> <cmd>` or any content-routing inside the hook system

Refuse. Filtering belongs in the consumer (the shell command). If the hook's command is `case ... esac`-heavy, that's normal shell programming. Adding filter syntax to the flag itself doubles the surface area for marginal gain.

### 5.5 `--on-cursor-advance <cmd>` and other intra-system signals

Refuse. Cursor activity is a property of the consumer, not the daemon. Per-advance hooks would create observer-effect feedback loops (the hook fires on every read, including reads that look for the hook's effects).

### 5.6 Synchronous "veto" hooks (`--on-digest-pre <cmd>` that can fail the digest)

Refuse. The consumer never waits on a hook. Veto power means blocking, which breaks the calm contract. If you need digest creation to be conditional, do the work in your hook and let it observe the digest you already produced — fire your "veto" by deleting it after the fact (e.g. `--on-digest 'check-things || sharedwatch digest archive $(jq -r .id)'`).

---

## 6. Composition — single flag per hook, or unified dispatcher?

Today: one flag per hook (`--on-digest`). If we add 3-5 more, that's still tractable in `--help`. If we ever expand past ~8 hook types, switch to a unified `--hook <event>:<cmd>` form:

```bash
sharedwatch run \
  --hook digest:'notify-send ...' \
  --hook mode-change:'echo $(jq -r .to) > /run/sw-mode' \
  --hook lease-violation:'./alert.sh'
```

The unified form scales better but is less discoverable (a user reading `--help` sees `--hook` instead of an enumerated list). The trade-off is acceptable around 8+ types.

**Recommendation:** Stick with per-hook flags through v0.2.x. Reconsider at v0.3.

There's also a YAML stanza option:
```yaml
hooks:
  on_digest: "notify-send ..."
  on_mode_change: "..."
  on_failed_events:
    command: "..."
    threshold: 50
```

The YAML form already works for the `on_digest:` key via the existing config loader. Extending it to new hook types is automatic.

---

## 7. Composability + idempotency considerations

Worth thinking about now so we don't paint ourselves into corners:

- **Chain via shell**: `--on-digest 'a && b'` is the supported way to chain. Don't build a chain primitive.
- **Idempotency**: hooks CAN fire twice for the same logical event (re-emit on restart, retry edge cases). Commands should be idempotent or use the meta-event's id as a dedup key. Document this for the curl-to-webhook case especially.
- **Ordering**: today only one hook type exists, so no ordering question arises. When we add `--on-mode-change` AND `--on-digest`, both could fire close together. They run in separate goroutines; **no ordering guarantee between hook types**. Document.
- **Concurrency**: multiple instances of the same hook type can run concurrently (two digests created during a busy active period, each fires a hook). The current implementation handles this via `App.hooksWG`. Document that hooks should be re-entrant.

---

## 8. Roadmap suggestion

Concrete ordering of what to build next, with rough scope estimates. **All are recommendations, not commitments.**

### v0.1.1 — `--on-mode-change` + `mode.changed` event

- New `events.TypeModeChanged` constant.
- Emit on every mode transition (`runtime.SetActive` / `SetPassive` / TTL expiry detection).
- New `--on-mode-change` flag + plumbing (mirrors `--on-digest`).
- Update `docs/guides/on-digest-hooks.md` to cover both hooks (rename the file?).
- **Estimate:** half-day. Smallest meaningful expansion.

### v0.1.2 — SW-AGENT-24 (coord-events stream)

- All lease.* and intent.* events into the journal.
- `lease.violated` (the slog.Warn) becomes a real event.
- Pure additions; no behaviour change for existing consumers.
- No new hooks (yet) — agents can tail the journal.
- **Estimate:** 1.5–2 days including dogfood.

### v0.1.3 — Lifecycle hooks

- `daemon.started` / `daemon.stopping` / `daemon.crashed` events.
- `--on-startup` / `--on-shutdown` hook shortcuts.
- Stale-lock detection wiring for `daemon.crashed`.
- **Estimate:** 1 day.

### v0.2.0 — Failure + observability surface (minor bump because of breadth)

- `events.stuck_detected`, `events.failed_threshold`, `events.retried_batch` events.
- `retention.ran` event.
- `reconcile.ran`, `reconcile.drift_detected`, `reconcile.recovered_threshold` events.
- `--on-failed-events --on-failed-events-threshold <N>` hook.
- `--on-lease-violation` hook (depends on v0.1.2).
- **Estimate:** 3–4 days; this is the "fill the gaps" pass.

### v0.2.1 — Actor registry events

- `actor.registered` / `actor.went_stale` / `actor.removed` events.
- No hooks (assume cursor tail is enough until requested).
- **Estimate:** half-day.

### Open / deferred

- Per-event hooks: ❌ won't build.
- Webhook flag: ❌ won't build.
- Plugin system: ❌ won't build.
- Unified `--hook <name>:<cmd>` syntax: defer to v0.3 unless flag count grows.
- `--on-reconcile-drift`, `--on-actor-stale`: build on demand.

---

## 9. Open questions

Before any of §8 ships:

- **Q1: Should the new event types reuse existing sources (`source=daemon`) or get their own (`source=mode`, `source=actor`, etc.)?**
  Argument for unified `source=daemon`: simpler filter. Argument for per-class sources: easier to scope filters via `events list --source mode --since 1d`. **Lean per-class** — small surface cost, real query convenience.

- **Q2: Threshold-gated events — should the threshold be hard-coded or configurable?**
  E.g. `reconcile.drift_detected` fires when missing > threshold. Hard-coding 10 is fine for v1; if users want to tune we add `--reconcile-drift-threshold N`. Defer the config knob until asked.

- **Q3: Hook flag naming — `--on-X` consistently, or `--hook-X` for symmetry, or eventually unified `--hook X:cmd`?**
  Recommendation: stay with `--on-X` for individual hooks (matches `--on-digest`); introduce `--hook X:cmd` ONLY if we ever ship 8+ hooks.

- **Q4: Lifecycle event ordering — when exactly does `daemon.started` land vs. when do existing `reconcile.ran` events fire after startup?**
  Both happen in `App.Run`. Convention: `daemon.started` writes BEFORE `Reconcile.RunNow`. This way a cursor tailing `--type daemon.started` always sees startup before any work the daemon did in that run.

- **Q5: Should hook meta-events themselves cascade into other hooks?**
  Concrete: if `--on-digest` runs a hook, that hook emits a `hook.completed` event. Should `--on-event` (if it existed) fire on `hook.completed` events too? Recommendation: no — meta-events about meta-events is a feedback-loop risk. If we ever shipped per-event hooks (we won't), they'd skip `source=hook` events by default.

---

## 10. Summary recommendation

1. **Fill the journal first.** Most users asking "can you add a hook for X?" really benefit more from "X is now an event you can tail." This is a one-time cost that scales to all future cases.

2. **Add `--on-mode-change` as the next hook** (v0.1.1, half-day). It's the highest-leverage marginal expansion: small implementation, fits the existing pattern precisely, useful for both human operators and AI agents.

3. **Defer per-event hooks indefinitely.** Document why. They're the most-requested wrong answer.

4. **Stay shell-shaped.** No HTTP client, no plugins, no content filtering inside the flag. The hook is a syntactic shortcut over a shell command — that's its job.

5. **Reconsider the architecture if the flag count climbs past 5.** Until then, per-hook flags are fine.

The principle worth holding to: **the journal is the integration surface. Hooks are a UX shortcut on top of it, not a replacement for it.** Everything in §3 belongs in the journal. Everything in §4 is optional sugar.
