---
name: sharedwatch-client-future
description: Use sharedwatch after the multi-folder, progressive-disclosure, and attribution work tickets ship — `--root <label>=<path>` definitions and `--root <label>` filters, the `overview` and `events stats` aggregation endpoints with `drill` hypermedia hints, first-class `--actor`/`--session`/`--task` flags that populate a structured payload_json v1 schema, the `actors` registry with heartbeats, declared `intents`, and advisory `leases`. Use when (1) the deployed sharedwatch version supports the `overview` or `events stats` subcommands (or its `version` >= 1.0), (2) the workspace contains a `watch_roots:` config list or `--root <label>=<path>` invocations are in use, (3) `sharedwatch status --json` exposes a `roots[]` array or an `actors[]` array, (4) you are coordinating in a multi-agent fleet where the agent-fit features have shipped, or (5) you need to declare intent before editing or hold an advisory lease on a path.
---

# sharedwatch-client-future (post-ticket operating skill)

Operate as an AI agent against sharedwatch after the agent-fit work tickets (SW-AGENT-3 multi-folder + progressive-disclosure aggregations + attribution v1 + actor/intent/lease features) have shipped.

This skill describes how the client surface **will** behave once those features land. If the deployed binary doesn't yet support these commands, fall back to the `sharedwatch-client` skill — most patterns work in degraded form against today's CLI by replacing aggregation endpoints with `sql` queries.

## What changed vs the current-state skill

| Concern | Current (`sharedwatch-client`) | Future (this skill) |
|---|---|---|
| Multi-folder | Single `--watch-path` only | `--root <label>=<path>` definitions + `--root <label>` filters |
| Aggregation | DIY with `sharedwatch sql` | `overview` (L1), `events stats` (L2) endpoints with `drill` hints |
| Attribution | `payload_json` blob + free-form `--payload` | `--actor`, `--session`, `--task`, `--intent`, `--addressee`, `--ref`, `--tag` flags on every emit |
| Liveness | None | `actors` registry + `sharedwatch actor heartbeat` + `status --actors` |
| Pre-edit coordination | None | `sharedwatch intent declare <path-glob> --ttl 5m` |
| Locking | None | `sharedwatch lease grant <path-glob> --ttl 5m` (advisory) |
| Output drill | Manual SQL composition | Every aggregate output carries a `drill` map → next-level command |

## Mental model (additions)

- A **root** is a labeled watched directory. `auth`, `billing`, `inbox/from-human` are labels — short, stable, agent-prompt-safe. Paths drift; labels don't.
- An **actor** is a registered participant — usually an AI agent identity, possibly a human. Actors heartbeat; stale actors are auto-flagged.
- An **intent** is a declared upcoming edit on a path-glob, with a TTL. Other actors see it as soft coordination signal.
- A **lease** is an advisory, time-bounded "I'm editing this" claim. Not enforced by the FS — but visible to cooperative peers.
- A **drill hint** is a `drill` field in any aggregate JSON output that points to the next-finer query for each dimension. Follow it instead of memorizing CLI grammar.

## First action — confirm you're in the future world

```bash
sharedwatch overview --format json 2>/dev/null \
  || echo "future-state commands not available — use the sharedwatch-client skill instead"
```

If `overview` exits non-zero or with an unknown-subcommand error, the binary predates this skill. Fall back.

## Five+ commands to memorize

```bash
# 1. L1 — across-roots overview, ~100 tokens
sharedwatch overview --format json

# 2. L2 — per-root rollup, ~400 tokens
sharedwatch events stats --root <label> --since 24h --format json

# 3. L3/L4 — event projections with multi-root and structured attribution
sharedwatch events list \
  --root <label> \
  --since 1h \
  --fields id,type,rel_path,actor,session,created_at \
  --format jsonl

# 4. "What's new since I last looked?" — cursor + per-root scope
sharedwatch events list --cursor-name <actor>-<task> --root <label> --format jsonl

# 5. Self-discovery (once per session, cache)
sharedwatch schema --format json
sharedwatch roots --format json
```

Plus the new actor/intent/lease commands:

```bash
# Identify yourself (heartbeat the actor registry)
sharedwatch actor heartbeat <actor-id> --focus 'auth/**'

# Declare intent before a non-trivial edit
sharedwatch intent declare 'auth/login.go' --ttl 5m --actor <id>

# Hold an advisory lease
sharedwatch lease grant 'auth/login.go' --ttl 5m --actor <id>
sharedwatch lease release <lease-id>
sharedwatch lease list --root auth
```

Full details for each surface area in the references below:
- `references/multi-root.md` — root labels, filter vs definition, default behavior
- `references/disclosure.md` — L1/L2/L3 levels, drill hints, when to zoom
- `references/attribution-v1.md` — payload_json v1 schema, flag → key mapping
- `references/leases.md` — intent vs lease vs heartbeat, cooperation model
- `references/migrations.md` — what to change in agents written against the current-state skill

## Identify yourself — first-class flags

Every emit/run command accepts the v1 attribution flags directly. No more hand-rolled `--payload '{...}'` JSON.

```bash
# Run sharedwatch under an actor identity
sharedwatch run \
  --actor claude-coordinator-1 \
  --session sess-2026-05-22-abc \
  --task refactor-auth

# Or emit a single attributed event
sharedwatch test emit auth/login.go \
  --actor claude-coordinator-1 \
  --session sess-abc \
  --task refactor-auth \
  --intent "split JWT validation" \
  --addressee claude-code \
  --ref evt_a1b2c3 \
  --tag refactor --tag auth
```

These flags populate the conventional payload keys defined in `references/attribution-v1.md`. Querying remains `--payload-key actor --payload-value <x>`; the table additionally has a promoted `actor`/`session` column once the migration lands (check `schema --format json` to confirm).

## Agentic patterns — updated

| Pattern | Multi-root form | New steps |
|---|---|---|
| **initial-orientation** | `overview` first, then `events stats --root <pick>` | Single L1 call replaces three SQL aggregations |
| **peer-handoff-check** | `events list --root <X> --path-glob '<P>' --since 1h --payload-key actor --payload-value <peer>` | Also: `intent list --path-glob '<P>'` to see declared upcoming edits |
| **continuous-monitoring** | Unified or per-root cursor | Cursor naming convention: `<actor>-<task>` for unified, `<actor>-<task>-<root>` per-root |
| **publish-and-attribute** | Flags replace hand-built JSON | Use `--actor` etc. directly on `run` and `test emit` |
| **did-peer-respond** | `events list --payload-key ref_event_id --payload-value <id>` | Unchanged; works across roots by default |
| **drill-from-overview** | NEW | Take a `drill` hint from L1 → L2 → L4 |
| **lease-before-edit** | NEW | `lease grant <path> --ttl 5m`; on release, your edit completes |
| **declare-intent** | NEW | `intent declare <path-glob> --ttl 10m` before a multi-step refactor |

Pattern details in `references/disclosure.md` (drill) and `references/leases.md` (lease/intent/heartbeat).

## Hard rules — still apply, with additions

- **Single-folder is the default.** If no `--root` is configured AND no `--root <label>=<path>` is passed, the system behaves identically to single-root. Don't add multi-root complexity unless the workspace requires it.
- **`--root foo=/path` defines; `--root foo` filters.** The `=` is the disambiguator. Don't pass a definition to a read command.
- **Always release leases.** Either set a tight TTL or call `lease release <id>` when done. Stale leases freeze peers.
- **Heartbeat at most once per minute.** Heartbeats are cheap but additive — fleet of 10 agents heartbeating every 5 s is 120 events/min of pure noise.
- **The `actor` you set in flags should match the `actor` in your heartbeat.** Mismatch = identity confusion in queries.

All the current-state rules (never poll faster than 1 Hz, never `sql --write` without approval, etc.) still apply.

## Pocket decision tree (updated)

| Situation | Command |
|---|---|
| Just started in a multi-root workspace | `overview --format json` |
| One root looks hot — drill in | `events stats --root <X> --since 1h --format json` |
| About to edit something risky | `intent declare '<path-glob>' --ttl 5m`, then check `lease list --path-glob '<path>'` |
| Want exclusive editing window | `lease grant '<path>' --ttl 5m` |
| Pulling new events for one root | `events list --root <X> --cursor-name <actor>-<task>-<X>` |
| Who is alive right now? | `sharedwatch status --actors --format json` |

## Where to look next

| Question | File |
|---|---|
| Multi-root rules, label grammar, defaults table | `references/multi-root.md` |
| L1/L2/L3 levels, drill hints, compression strategies | `references/disclosure.md` |
| Full payload_json v1 keys, flag→key mapping, validation | `references/attribution-v1.md` |
| Intent vs lease vs heartbeat, cooperation model, edge cases | `references/leases.md` |
| What to change in agents written against `sharedwatch-client` | `references/migrations.md` |
