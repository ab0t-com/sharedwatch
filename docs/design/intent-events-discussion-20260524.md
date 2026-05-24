# Intent events + coalesce phantom-id + JSON envelope consistency — discussion (2026-05-24)

**Origin:** dogfood scenarios 33 (intent lifecycle) and 34 (cross-actor coalesce regression) surfaced three deeper-design questions on top of the small UX fixes (help-text) that already shipped.

This doc captures the open questions, sketches options, and proposes a recommended direction for each. It is a discussion artefact, not a decision — the actual call belongs to the user.

---

## Q1 — Should `intent declare` / `intent revoke` emit events?

### Observed

- `lease grant` / `lease release` emit `lease.granted` / `lease.released` events into the journal.
- `intent declare` / `intent revoke` do **not** emit any events.
- Result: a peer agent watching the journal (`events list --cursor-name ...`) sees lease activity but is blind to intent activity. To know "what is anyone planning right now?" they must poll `intent list --json`, which is a fundamentally different access pattern from the rest of the coordination surface.

### Why this matters

The whole point of intents is *coordination signalling before the work happens*. If the canonical "watch what others are doing" interface (events journal) doesn't carry intent signals, the feature is half-built — the storage is there but the discoverability isn't.

### Options

**(A) Emit `intent.declared` / `intent.revoked` events.** Symmetric with leases. One row per intent declare and one per revoke. Payload includes actor, path_glob, task, intent text, ttl.

**(B) Status quo + document.** Tell agents "intents are poll-only; leases are stream-able." Cheaper but locks in an asymmetry that will keep surfacing in agent confusion.

**(C) Hybrid — emit only on declare, not revoke.** Argument: a revoked intent is a non-event (it was withdrawn). Counter-argument: peer agents that already saw the declare need to know it's gone, exactly like a released lease.

### Recommendation

**(A) Emit both.** Symmetry with leases is the right invariant: every coordination-surface change appears in the journal. Implementation is ~30 LOC (add two `events.Emit` calls in the existing declare/revoke handlers, payload mirrors the lease event shape). One-shot ticket worth filing — call it **SW-AGENT-23 — intent.* events parity with lease.* events**.

---

## Q2 — `test emit` post-coalesce phantom id

### Observed

`test emit src/x.go --actor alpha` when an event from the same `(actor, src/x.go)` exists within the 5s coalesce window:
- Generates a new `evt_id` (e.g. `evt_fa15...`)
- Prints it: `emitted evt_fa15... src/x.go`
- But the row never lands in the DB — the coalesce path merges into the prior event's id.

Agent recording the printed id and later doing `events list --id evt_fa15...` finds nothing — and has no obvious way to diagnose why.

### Why this matters

Phantom ids violate a fundamental output-contract expectation: **`test emit <path>` returning an id implies that id exists**. Either the contract should be "this id is what *would have been* inserted" (then say so), or the contract should be "this is the persisted id" (then return the coalesced id).

### Options

**(A) Return the coalesced (prior) event id.** Surface the actual persisted row. Subtle: the `emitted` verb is now slightly inaccurate (no new row was created — the existing one was touched). But the id is *correct*.

**(B) Detect coalesce and print `coalesced into evt_xxx` instead of `emitted evt_yyy`.** Most explicit. Slightly more output. Best DX.

**(C) Status quo + document.** Cheapest. We just documented it in the gotcha list (scenario 34) — but that puts the burden on every agent to remember.

### Recommendation

**(B).** Explicit and actionable: `test emit` returns either `emitted <new_id>` (fresh row) or `coalesced into <prior_id>` (merged). Agent code that checks for the "coalesced" prefix can branch correctly; agent code that doesn't will at least see the right id when it parses the trailing token. Implementation is small: the coalesce path is already detectable in `events.Insert` — surface a sentinel return value or boolean and branch the println.

File alongside Q1 as **SW-AGENT-24 — test emit coalesce-aware output**.

---

## Q3 — JSON output envelope consistency

### Observed

| Endpoint | JSON shape | Empty shape |
|---|---|---|
| `events list --format jsonl` | one object per line | (no output) |
| `status --json` | `{ ...object... }` | (always populated) |
| `schema --format json` | `{ ...object... }` | (always populated) |
| `intent list --json` | bare array `[{...},{...}]` | bare `null` |
| `lease list --format jsonl` | one object per line | (no output) |
| `events stats --json` | `{ ...object... }` | populated with zeros |

`intent list --json` is the **only** endpoint that returns a bare array (and bare `null` for empty). Every other list-like endpoint either uses JSONL (one object per line) or wraps in an envelope object.

### Why this matters

Inconsistency forces every consumer (agent, script, doc, hint provider) to special-case intent. The output contract is otherwise tight enough that `jq` recipes generalise; this is the one exception.

### Options

**(A) Move `intent list --json` to JSONL** (matching `events list`, `lease list`). Empty result = no output. Most consistent for list-style endpoints. Breaking change for anyone parsing it as a single JSON value.

**(B) Wrap in envelope: `{"intents":[...], "count":N}`.** Consistent with `status --json`. Empty → `{"intents":[], "count":0}`. Also breaking, but cleaner for callers that want a single `jq` invocation.

**(C) Leave alone.** Pretend the inconsistency isn't there. We just documented it (scenario 33 gotcha) — same cheap-vs-correct trade-off as Q2(C).

### Recommendation

**(A) JSONL.** It matches the other two list endpoints (events, lease) exactly, so agent muscle memory ports across. It is a breaking change — but `intent list` is a new and lightly-used surface, the dogfood loop is the only known consumer, and the cost of fixing it now scales as O(callers we know of) which is 0 right now and grows only.

Bundle with Q1/Q2 — these are all small enough to land in one v0.0.9-class release and they all touch the same family of "intent surface polish + coordination-event parity." File as **SW-AGENT-23/24/25** or, more parsimoniously, one combined ticket: **SW-AGENT-23 — intent surface parity (events + JSONL + coalesce-aware emit)**.

---

## Net recommendation

One combined ticket — SW-AGENT-23 — that:
1. Emits `intent.declared` / `intent.revoked` events (Q1-A)
2. Changes `test emit` to print `coalesced into <prior_id>` when the coalesce path triggers (Q2-B)
3. Switches `intent list --json` to JSONL (Q3-A)

Each is small (sub-50 LOC), all three share testing infrastructure, and shipping them together avoids re-doing the dogfood scenarios for each. Bumps to v0.0.9; CHANGELOG entry covers all three.

If the user prefers minimum-viable: only ship Q1 (intent events). Q2/Q3 stay documented as gotchas and ship later when an actual user hits them.

---

## What was already shipped this round (NOT covered by this discussion)

The following were small enough to fix in-place during the dogfood pass and are already on `main`:

- `intent declare --help` now shows the `<path-glob>` positional in its usage line (custom `fs.Usage`).
- `intent revoke --help` no longer treats `--help` as a positional id; returns the usage string and exits 0.
- The three gotcha bullets above are now in `docs/agent/agent-system-prompt-20260522.md` and `Skills/sharedwatch-client/SKILL.md` startup ritual.

The discussion above covers only what was **deferred** because it warrants a design call rather than a one-line patch.
