# Intent events + coalesce phantom-id + JSON envelope consistency — discussion (2026-05-24)

**Origin:** dogfood scenarios 33 (intent lifecycle) and 34 (cross-actor coalesce regression) surfaced three deeper-design questions on top of the small UX fixes (help-text) that already shipped.

This doc captures the open questions, sketches options, and proposes a recommended direction for each. It is a discussion artefact, not a decision — the actual call belongs to the user.

---

## Q1 — Should the coordination surface (lease + intent) emit events?

### Observed (corrected 2026-05-24 after empirical verification)

- **NEITHER** `lease grant`/`lease release` **NOR** `intent declare`/`intent revoke` emit events to the journal.
- My initial framing claimed leases emit events for parity with intents — that was wrong. Empirical check (`COUNT(*) FROM events` before/after lease grant + release on an empty DB): zero events. The lease grant handler at `src/cmd/sharedwatch/main.go:1084` only calls `InsertLease`; no `InsertEvent`.
- The events journal is for **FILE events only** today (`file.created/.modified/.deleted/.renamed`, plus `reconcile.*`).
- Result: there is currently **no way to subscribe to coordination signals** via the journal. A peer agent that wants to know "who declared what intent recently?" or "who acquired what lease?" must poll the dedicated `intent list` / `lease list` endpoints — fundamentally different from the cursor-based stream model used for file events.

### Why this matters

The whole point of intents is *coordination signalling before the work happens*. If the canonical "watch what others are doing" interface (events journal) doesn't carry intent signals, the feature is half-built — the storage is there but the discoverability isn't.

### Options

**(A) Emit events for the full coordination surface** — add `lease.granted`, `lease.released`, `lease.renewed`, `intent.declared`, `intent.revoked` as new event types. Single, consistent design: every coord-surface change appears in the journal as well as in its dedicated table. Cursor-streaming agents now get coordination visibility for free. ~80–100 LOC.

**(B) Status quo + document clearly.** Cheap. Locks in poll-only access for the coordination surface, but matches how the system actually behaves today.

**(C) Emit only on declare/grant (not revoke/release).** Halfway. Argument: revoke/release are "withdrawals" that don't need a stream-side signal. Counter-argument: peer agents subscribed to the cursor stream need to know when a lease frees up or an intent is withdrawn, otherwise they have stale state.

**(D) Build a separate "coord-events" stream** — a second journal-like view that aggregates lease+intent state changes without polluting the file-events stream. More machinery, but cleaner separation. Probably overengineered for current scale.

### Recommendation

**(A), or defer with (B) for now.** This is a bigger question than I initially framed it — adding coordination events touches multiple subcommands, cursor consumers, hint providers, and JSON envelopes. It's a real design call that probably warrants its own ticket (~3–5 hours of work + dogfood) rather than being bundled with the small intent fixes below. **Recommended split**: defer Q1 to its own ticket (call it SW-AGENT-24 — coord-events stream), ship Q2 + Q3 now under SW-AGENT-23 (intent surface fixes). The user can decide whether Q1 is worth the larger investment.

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

## Net recommendation (revised after Q1 reframing)

**Split into two tickets:**

- **SW-AGENT-23 — intent surface fixes (small, ship now in v0.0.9):**
  - Q2: change `test emit` to print `coalesced into <prior_id>` when the coalesce path triggers
  - Q3: switch `intent list --json` to JSONL (matching `events list`/`lease list`)
  - Both are sub-50 LOC, no design risk, and close the gotchas surfaced by dogfood scenarios 33/34.

- **SW-AGENT-24 — coord-events stream (larger, defer):**
  - Q1: decide whether `lease.granted/released/renewed` + `intent.declared/revoked` should land in the events journal so agents can subscribe via cursor.
  - This is a real design question that affects multiple subcommands, cursor consumers, hint providers, and the agent-facing skill docs. It deserves its own deliberation and isn't ready to ship without thinking through:
    - Should coord events live in the same `events` table or a sibling table?
    - Do they participate in coalesce? (Probably no — coord events are distinct user actions.)
    - Do cursors filter them by default? (Backwards compat: probably yes — opt-in.)
  - File a separate discussion + ticket; do not bundle.

If the user wants minimum-viable: skip Q1 entirely; ship Q2+Q3 as SW-AGENT-23/v0.0.9; revisit coord-events only if an actual user asks.

---

## What was already shipped this round (NOT covered by this discussion)

The following were small enough to fix in-place during the dogfood pass and are already on `main`:

- `intent declare --help` now shows the `<path-glob>` positional in its usage line (custom `fs.Usage`).
- `intent revoke --help` no longer treats `--help` as a positional id; returns the usage string and exits 0.
- The three gotcha bullets above are now in `docs/agent/agent-system-prompt-20260522.md` and `Skills/sharedwatch-client/SKILL.md` startup ritual.

The discussion above covers only what was **deferred** because it warrants a design call rather than a one-line patch.
