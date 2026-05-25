# sharedwatch — event-broker user guide

**Available in:** v0.1.1+ (SW-AGENT-30). Older binaries only emit file events + hook meta-events.
**Status:** stable.
**Companion docs:**
- [`docs/design/event-and-hook-surface-expansion-20260524.md`](../design/event-and-hook-surface-expansion-20260524.md) — the principle ("emit liberally, hooks stay narrow")
- [`docs/design/event-broker-consumer-contracts-20260525.md`](../design/event-broker-consumer-contracts-20260525.md) — the consumer catalog this guide implements against
- [`docs/guides/on-digest-hooks.md`](./on-digest-hooks.md) — the `--on-digest` shell-hook surface (orthogonal)

This guide is for **operators and integrators** who want sharedwatch's full state-change stream — not just file events. As of v0.1.1, sharedwatch emits ~25 event types covering daemon lifecycle, mode transitions, coordination (lease + intent), actor registry, retention, failure thresholds, and snapshots. Subscribers tail the journal with named cursors; sharedwatch is the broker.

---

## 1. The mental model

> **The events journal IS a single-host event broker.**

- **Topics** — filter by `--type`, `--source`, `--path-glob`, `--payload-key/--payload-value`
- **Durable** — events retained `retention_days` (default 30)
- **Multi-consumer** — named cursors (`--cursor-name <id>`); each consumer's position is independent
- **Replay-able** — `events cursor reset <name>` re-reads from start
- **Schema-versioned** — `format_version: 1` in payloads; bump on breaking changes
- **Source-tagged** — `source=watcher|reconciler|test|hook|daemon|mode|coord|actor|retention`

What we **don't** provide: pub/sub push fan-out (pull-only is the calm contract), cross-host transport, exactly-once delivery (use the event `id` as a dedup key), cross-source ordering.

---

## 2. Configuring what gets emitted

Three knobs, standard resolution chain (**flag > env > config > default**):

```yaml
emit_profile: standard            # minimal | standard | verbose | all
emit_overrides:                   # optional: opt classes in/out
  actor_heartbeats: true          # opt INTO an all-tier class at lower tier
  reconcile_per_cycle: false      # opt OUT of a verbose-tier class
emit_thresholds:                  # optional: tune the rising-edge events
  events_failed: 50               # fire events.failed_threshold when count > 50
  events_stuck: 10                # fire events.stuck_detected when count > 10
  reconcile_drift: 25             # fire reconcile.drift_detected when recovered > 25
```

Flags: `--emit-profile <tier>`, `--emit-override <class>=<bool>` (repeatable).
Env: `SHAREDWATCH_EMIT_PROFILE`, `SHAREDWATCH_EMIT_OVERRIDE=k=v,k=v` (thresholds are operator-only, no env).

Verify what the daemon resolved:

```bash
sharedwatch config show --json | jq '.effective | {emit_profile, emit_overrides, emit_thresholds, emit_effective_classes}'
```

`emit_effective_classes` is the derived truth table — every known class with an explicit `true`/`false` after profile + overrides resolve. It's the operator-verification surface.

### The four tiers

| Tier | What it adds | Use case |
|---|---|---|
| **minimal** | `file.*`, `hook.*` only | v0.0.x backwards compat; storage-constrained envs |
| **standard** *(default)* | + digest, daemon lifecycle, mode.changed, retention, failure thresholds, coord (lease/intent), actor lifecycle | ~95% of integration cases |
| **verbose** | + reconcile.ran, mode.ttl_extended, actor.went_stale, snapshot.taken | operational observability |
| **all** | + actor.heartbeat_received | full audit / replay |

---

## 3. The event catalog (v0.1.1)

All shipped event types, grouped by source. Names are stable contracts — once shipped, never renamed.

### `source=watcher` / `source=reconciler` (file events — minimal tier)

| Type | Triggered by | Tier |
|---|---|---|
| `file.created` | new file detected | minimal |
| `file.modified` | file content/mtime changed | minimal |
| `file.deleted` | file removed | minimal |
| `file.renamed` | rename detected (paired create+delete by size+mtime) | minimal |

### `source=hook` (SW-AGENT-29; --on-digest)

| Type | Triggered by | Tier |
|---|---|---|
| `hook.completed` | --on-digest subprocess exit 0 | minimal (gated by --on-digest flag) |
| `hook.failed` | --on-digest timeout / nonzero / spawn error | minimal (same) |

### `source=daemon` (lifecycle + failure thresholds)

| Type | Triggered by | Tier |
|---|---|---|
| `digest.created` | every digest INSERT commit | standard |
| `daemon.started` | lock acquired, before main loop | standard |
| `daemon.stopping` | deferred during graceful shutdown | standard |
| `daemon.crashed` | next-startup stale-lock detection | standard |
| `events.failed_threshold` | per reconcile cycle when failed count > threshold | standard |
| `events.stuck_detected` | per reconcile cycle when processing-stuck count > threshold | standard |
| `events.retried_batch` | (future) after `events retry` requeues N | standard |

### `source=mode`

| Type | Triggered by | Tier |
|---|---|---|
| `mode.changed` | active↔passive transition | standard |
| `mode.ttl_extended` | active TTL bumped while already active | verbose |

### `source=reconciler` (observability)

| Type | Triggered by | Tier |
|---|---|---|
| `reconcile.ran` | per reconcile cycle (with duration_ms + events_recovered + snapshots_taken) | verbose |
| `reconcile.drift_detected` | per-root when recovered > `emit_thresholds.reconcile_drift` | standard |
| `snapshot.taken` | per root per cycle, after SaveSnapshot succeeds | verbose |

### `source=retention`

| Type | Triggered by | Tier |
|---|---|---|
| `retention.ran` | per reconcile cycle, with pruned counts per resource | standard |

### `source=coord` (lease + intent lifecycle)

| Type | Triggered by | Tier |
|---|---|---|
| `lease.granted` | after `lease grant` succeeds | standard |
| `lease.released` | after `lease release` | standard |
| `lease.renewed` | after `lease renew` | standard |
| `lease.expired` | per reconcile cycle, before pruning | standard |
| `lease.violated` | watcher detects foreign write inside an active lease | standard |
| `intent.declared` | after `intent declare` | standard |
| `intent.revoked` | after `intent revoke` | standard |
| `intent.expired` | per reconcile cycle, before pruning | standard |

### `source=actor`

| Type | Triggered by | Tier |
|---|---|---|
| `actor.registered` | first heartbeat for a new actor_id | standard |
| `actor.removed` | per reconcile cycle when age > 2× ActorTTL (just before prune) | standard |
| `actor.went_stale` | per reconcile cycle when age > 1× ActorTTL but < 2× | verbose |
| `actor.heartbeat_received` | every heartbeat | all |

---

## 4. Consumer recipes

Six high-leverage patterns. Drop them into a script, cron, systemd timer, or your daemon of choice.

### 4.1 Slack notification on every digest

```bash
sharedwatch events list --cursor-name slack-digest --type digest.created --format jsonl |
while read e; do
  echo "$e" | jq -c .payload_json | jq -r 'fromjson | "{\"text\":\"sharedwatch: \" + .summary + \"\"}"' |
  curl -s -X POST -H "Content-Type: application/json" -d @- "$SLACK_WEBHOOK_URL"
done
```

### 4.2 PagerDuty alert on failure thresholds

```bash
sharedwatch events list --cursor-name oncall-pager \
  --type events.failed_threshold --type events.stuck_detected --type daemon.crashed \
  --format jsonl |
while read e; do
  echo "$e" | jq -c '{routing_key: "YOUR_KEY", event_action: "trigger", payload: .}' |
  curl -s -X POST -H "Content-Type: application/json" -d @- https://events.pagerduty.com/v2/enqueue
done
```

### 4.3 Multi-agent peer-coordination bot

```bash
# Watch for peer activity in real time. Filter out own actor's actions via payload-key.
sharedwatch events list --cursor-name peer-bot \
  --type intent.declared --type intent.revoked \
  --type lease.granted --type lease.released --type lease.violated \
  --type actor.registered --type actor.removed \
  --format jsonl |
while read e; do
  payload=$(echo "$e" | jq -r .payload_json)
  actor=$(echo "$payload" | jq -r '.actor // "(unknown)"')
  [ "$actor" = "$MY_ACTOR_ID" ] && continue  # skip own echoes
  on-peer-event.sh "$(echo "$e" | jq -r .type)" "$payload"
done
```

### 4.4 Compliance archive (every event → S3)

```bash
# emit_profile: all on the daemon side so even actor.heartbeat_received is captured.
sharedwatch --emit-profile all run &

# Subscriber: cursor-based at-least-once, every event to S3 with retention.
sharedwatch events list --cursor-name audit-archiver --format jsonl |
while read e; do
  id=$(echo "$e" | jq -r .id)
  echo "$e" | aws s3 cp - "s3://audit-bucket/$(date -u +%Y/%m/%d)/${id}.json"
done
```

### 4.5 CI/CD trigger when "meaningful work" lands

```bash
# digest.created is the natural batching point. Skip per-file events.
sharedwatch events list --cursor-name ci-trigger --type digest.created --format jsonl |
while read e; do
  digest_id=$(echo "$e" | jq -r '.payload_json | fromjson | .id')
  event_count=$(echo "$e" | jq -r '.payload_json | fromjson | .event_count')
  [ "$event_count" -lt 5 ] && continue  # skip trivially-small digests
  make ci-build DIGEST_ID="$digest_id"
done
```

### 4.6 Watchdog ("did sharedwatch restart unexpectedly?")

```bash
# Look for daemon.crashed in the last hour — means the previous instance
# didn't release its lock cleanly (SIGKILL, panic, hard reboot).
crashes=$(sharedwatch events list --type daemon.crashed --since 1h --format jsonl | wc -l)
if [ "$crashes" -gt 0 ]; then
  echo "ALERT: $crashes daemon crash(es) in last hour"
  sharedwatch events list --type daemon.crashed --since 1h --json |
    jq '.payload_json | fromjson | {prior_pid, prior_lock_age}'
fi
```

---

## 5. Consumer contracts (what we promise)

For every event type we ship, integrators can rely on:

- **Stable type names.** `lease.violated` will always be `lease.violated`. New behaviour gets a new name; we don't redefine.
- **Stable sources.** `source=coord` always means "lease/intent lifecycle action". A new conceptual category gets a new source.
- **Backwards-compatible payload evolution.** New keys added with `omitempty` until consumers are likely to expect them. Renames or removes bump `format_version`.
- **At-least-once delivery.** Reconcile may re-emit; consumers must be idempotent (use event `id` as dedup key).
- **Causal ordering within a source** for a given watch root. Across sources or roots, ordering is interleaved.
- **Retention floor.** Events live at least `retention_days` from emit time.

What we explicitly **don't** promise:
- Cross-source ordering
- Exactly-once delivery
- Synchronous availability (pull-style; expect up to 1Hz polling latency)
- Cross-host coordination

---

## 6. Patterns to follow

- **Cursor naming**: `<consumer>-<workspace>` or `<actor>-<task>`. Sharing a cursor name = sharing reads.
- **Filter at the source.** `--type` and `--source` are cheap. Reading-then-dropping is wasteful.
- **Don't poll faster than 1 Hz.** Project rule. Active mode's natural cadence is 5s; passive is 10min.
- **Be idempotent.** Treat each delivery as "process or skip"; never "fail loud and crash."

---

## 7. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Tail returns no `mode.changed` events | `emit_profile: minimal` or override `mode_change=false` | Check `config show --json | jq '.effective.emit_effective_classes.mode_change'`. Should be `true`. |
| `actor.heartbeat_received` flooding the journal | Default `emit_profile=all` shouldn't be on; OR override is forcing it | `--emit-profile standard` (default) suppresses; or `--emit-override actor_heartbeat=false` |
| Cursor falls behind retention; events vanish | Consumer was idle > `retention_days` | Run the consumer more often than the retention boundary; for one-shot replays use `--since-cursor <token>` instead of named cursor |
| `events.failed_threshold` fires every reconcile cycle while problem persists | v1 emits per-cycle (not rising-edge) | Acceptable for ~30-min cadence; debounce on consumer side, or wait for SW-AGENT-31 rising-edge work |
| `digest.created` arrives BEFORE the matching `hook.completed` | By design — digest emit is sync from consumer; hook fires async after | Ordering documented; consumers should join by `digest_id` not by arrival order |
| Multi-root daemon: which events carry `watch_root`? | Per-root events (file.*, lease.violated, snapshot.taken, reconcile.drift_detected) do. Daemon-wide events (daemon.started, retention.ran, etc.) leave it empty. | Filter with `--root <label>` for per-root events; ignore `--root` for daemon-wide |

---

## 8. Reference

| Topic | Where |
|---|---|
| Principle reframe | `docs/design/event-and-hook-surface-expansion-20260524.md` §0 |
| Consumer catalog (12 use cases) | `docs/design/event-broker-consumer-contracts-20260525.md` |
| Hook subsystem (orthogonal) | `docs/guides/on-digest-hooks.md` |
| Implementation tasklist | `tickets/tasklist_20260525_015121.md` |
| Source: ShouldEmit central authority | `src/internal/events/profile.go` |
| Source: per-emit-site wiring | `src/internal/app/app.go` + `src/internal/reconcile/reconcile.go` + `src/internal/watcher/service.go` |
| CHANGELOG entry | `src/CHANGELOG.md` under "SW-AGENT-30" |
