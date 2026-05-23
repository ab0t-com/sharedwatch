# Heartbeats, intents, leases — cooperative coordination

How to use the three advisory primitives that ship alongside attribution v1. None of them are enforced by the filesystem — they are *cooperative signals* that well-behaved agents respect.

## Contents
1. The three primitives, ranked by strictness
2. Heartbeats — liveness
3. Intents — "I plan to edit X"
4. Leases — "I'm editing X now"
5. Decision tree
6. Failure modes

## 1. The three primitives, ranked by strictness

| Primitive | Meaning | Strictness | When to use |
|---|---|---|---|
| **heartbeat** | "I am alive and focused on X" | weakest | Always — keeps the actor registry honest |
| **intent** | "I plan to edit X within the next N min" | soft | Before a multi-step refactor; before a non-trivial edit |
| **lease** | "I am editing X right now; please don't clobber" | strongest | During the active edit window |

All three are **cooperative**. The FS still lets anyone write anywhere. The signals are what well-behaved peers consult before acting.

## 2. Heartbeats — liveness

```bash
sharedwatch actor heartbeat <actor-id> \
  --focus '<optional-path-glob>' \
  --kind ai_agent
```

- Registers `<actor-id>` in the `actors` table with an updated `last_heartbeat` timestamp.
- Optional `--focus` describes what the actor is currently working on.
- TTL is implicit (5 min default). Past that, `status --actors` flags the actor `stale=true`.

**Cadence:** at most once per minute. Heartbeats are cheap but additive — a fleet of 10 agents heartbeating every 5 s is 120 events/min of noise. Once a minute is more than enough for liveness.

**Query:**
```bash
sharedwatch status --actors --format json
# {
#   "actors": [
#     {"actor":"claude-1","focus":"auth/**","last_heartbeat":"...","stale":false},
#     {"actor":"claude-2","focus":"billing/**","last_heartbeat":"...","stale":true}
#   ]
# }
```

## 3. Intents — "I plan to edit X"

```bash
sharedwatch intent declare 'auth/login.go' \
  --actor claude-me-1 \
  --task refactor-auth \
  --ttl 10m \
  --intent "extracting JWT validation"
```

- Inserts an intent row visible to all actors. Auto-expires at TTL.
- Other agents running `intent list --path-glob 'auth/**'` see your declaration and can decide whether to proceed.
- Intents do NOT block writes. They are a forward-looking *advisory*.

**When to declare:** before a multi-step refactor that will touch one or more files over minutes. Lets a peer who is *also* about to refactor see your declaration and back off (or coordinate).

**Querying intents:**
```bash
sharedwatch intent list --path-glob 'auth/**' --format json
sharedwatch intent list --actor claude-me-1
```

**Cancelling early:**
```bash
sharedwatch intent revoke <intent-id>
```

## 4. Leases — "I'm editing X now"

```bash
# Acquire
LEASE_ID=$(sharedwatch lease grant 'auth/login.go' \
            --actor claude-me-1 --ttl 5m --format json \
            | jq -r .lease_id)

# Do the edit ...

# Release
sharedwatch lease release "$LEASE_ID"
```

- A lease is a stronger advisory than an intent. The CLI surfaces a warning if a write event arrives on a leased path from a different actor.
- Leases have **required TTL**. Maximum is 1 hour. Default 5 min. Auto-released on expiry.
- Lease grant is **non-blocking** — if another actor already holds a lease on a conflicting glob, your grant succeeds but the response indicates the conflict:
  ```json
  {"lease_id":"lse_...","granted":true,"conflict_with":["lse_..."]}
  ```
  Decide whether to proceed or coordinate.
- `lease grant` with `--exclusive` fails if any conflicting lease exists.

**Listing:**
```bash
sharedwatch lease list --root auth                  # all leases in a root
sharedwatch lease list --path-glob 'auth/login.go'   # all leases on a path
sharedwatch lease list --actor claude-me-1           # all leases held by me
```

**Always release.** Leases are TTL-bounded as a backstop, but a stale lease until expiry is friction for peers. Release explicitly when done.

## 5. Decision tree

| Situation | Use |
|---|---|
| Starting a session | `actor heartbeat` (then continue heartbeating every ~1 min) |
| About to plan a multi-step refactor | `intent declare 'auth/**' --ttl 30m` |
| About to make a single non-trivial edit | `lease grant 'auth/login.go' --ttl 5m` |
| Quick fix, low conflict risk | Nothing — proceed |
| Want to know if peer is around | `status --actors --format json` |
| Want to know if peer plans to touch X | `intent list --path-glob '<X>'` |
| Want to know if peer is touching X right now | `lease list --path-glob '<X>'` |

## 6. Failure modes

| Failure | Defense |
|---|---|
| Agent crashes holding a lease | TTL auto-releases. Cap at 1h prevents indefinite freeze. |
| Lease squatting (agent renews forever) | Renewal counter; status flags leases renewed >3 times. |
| Two agents grant lease at the same instant | Both succeed; `conflict_with` field surfaces. Higher-priority actor (lower lexicographic actor id, by convention) wins; the other releases. |
| Intent left without follow-through | Auto-expires at TTL. No persistent state. |
| Heartbeat from a different host with same actor id | First-write-wins for the heartbeat row, but `status --actors` warns of conflict. Treat as identity error; agent should pick a unique id. |
| Lease/intent on a path that doesn't exist yet | Allowed — useful for "I'm about to CREATE this file." |
| Non-cooperative actor ignores all signals | The signals don't enforce. Document the trust boundary. For hard guarantees, you need OS-level locking or a real concurrency-control story. |

## Cost model — how cheap is this?

Each heartbeat/intent/lease is one DB row. Even a fleet of 50 agents:
- 50 actors × 60 heartbeats/hour = 3000 rows/hour
- Plus ~5 leases per active actor × 50 = 250 rows/hour
- Plus ~1 intent per task × 50 tasks/hour = 50 rows/hour

Total: ~3300 rows/hour. Compared to a busy event journal (often 1000s/hour), this is noise. The actors table is auto-pruned (rows past TTL × 2 are removed); intents and leases are auto-pruned past expiry.

## What this is NOT

- **A locking system.** The FS still allows concurrent writes. If you need actual mutual exclusion, use a different tool (Redis locks, lockfiles in the watched folder, etc.).
- **A scheduling system.** Intents are "I plan to," not "queue me to run at." There's no deferred execution.
- **A messaging system.** For agent-to-agent messages, use events with `addressee` set — that's what `payload_json.addressee` is for.

If you reach for leases to solve a problem these primitives weren't designed for, you'll fight the design. Reach for a purpose-built tool instead.
