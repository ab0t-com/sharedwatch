# sharedwatch — agent-fit analysis

Date: 2026-05-20 03:42 UTC
Question from Mike: forget the dressing — would an agent (running many concurrent threads of work, ideas, changes) actually find this useful as it stands? What's the agent really getting out of it?

This is the answer.

---

## 1. What sharedwatch actually is, stripped to the core

Three real things, one piece of dressing on top:

**Core 1: Durable file-event journal.**
A SQLite table (`events`) recording every detected change to files in a folder: created / modified / deleted / renamed, with timestamp, size, mtime, source, and a 14-char ID. Indexed for time-ordered queries. Survives restart.

**Core 2: Noise reducer.**
Coalescing (same file, modify-burst → 1 row) + ignore patterns + heuristic rename pairing. Without this, the journal is full of garbage and unusable. This is the part you cannot easily replicate with `inotify | tee`.

**Core 3: Drift safety net.**
A reconcile pass that snapshots the folder, diffs against the last snapshot, and synthesizes "recovery" events for anything the polling missed. The reason you can trust the journal as an authoritative history.

**Dressing: digest abstraction.**
The "consumer pulls a batch of pending events, renders a prose summary, writes a digest row, marks events processed" pipeline. This is the layer that matters for *humans*, because a human reading 50 file-events is fatiguing and a human reading "12 modified across 3 files" is not.

For an agent, the dressing is mostly waste. An agent doesn't get fatigued.

## 2. What an agent actually wants from a tool like this

Frame it from the agent's perspective. The agent is doing N concurrent things — coding tasks, reading specs, watching what teammates upload, tracking decision changes. The agent's reasons to want a file-event journal:

**A. Resume context after a suspend.**
"I was working on the auth refactor 40 minutes ago. While I was elsewhere, did anything in `repo/auth/` change?" The agent needs to answer this without re-reading every file or trusting its own memory.

**B. Detect that a teammate (human or other agent) has acted.**
"Did the human drop a new spec in `/inbox/` for me? Did the security-review agent leave a finding in `/reviews/`?" The agent needs to know without polling N folders in a tight loop.

**C. Audit its own actions.**
"What files did I touch in the last hour while working the migration thread?" Useful for self-correction, postmortems, undo.

**D. Coordinate among many agents.**
"Agent X is editing the schema; if I edit the schema too, I'll conflict. Show me what X has been writing." Useful for soft coordination without a real lock.

**E. Generate its own summaries on demand.**
The agent has an LLM. It doesn't need a hand-rolled `RenderHumanSummary` — it'll ask its own model to summarize the raw events when it needs prose. What it needs is **the structured events**, not pre-baked prose.

In every case, the agent wants **the event journal**, not the digest.

## 3. Is sharedwatch a good event-stream watcher? Yes. Does it have more value? Yes — but the more is mostly latent.

**Yes, it's a good event-stream watcher**, considered narrowly:
- Captures the right events
- Coalesces the noise
- Recovers drift
- Persists reliably
- Concurrent-CLI-safe (post-WAL)

**The extra value beyond "event stream":**
- Durability across crash/restart — a streaming watcher loses state; this doesn't.
- Structured, queryable storage — you can ask SQL questions, not just `tail -f` a log.
- Reconcile heartbeat — drift is bounded, not ignored.
- Pull-not-push — agent decides when to read; doesn't get interrupted by the watcher.
- Multi-consumer-safe — N readers can hit the same DB without races.

That's a real product. It's the difference between `tail -f log` (ephemeral, lossy, push) and a journal (durable, queryable, pull).

**But the latent value isn't exposed to agents yet.** The CLI optimizes for human eyes (`digest list`, `digest show`); the events table is buried behind `consume → digest`. An agent that wants the journal has to either:
- Query SQLite directly (knowing the schema, joining tables, escaping SQL),
- Or run `consume` and parse the prose summary (lossy, fragile),
- Or `test emit` for ad-hoc events but with no way to add metadata.

So the answer to "is it good for an agent as it stands" is: **good enough to be useful, not good enough to be obvious or pleasant.** The agent has to know the SQLite schema and write its own queries. That works, but it's not what a tool meant for agents looks like.

## 4. The honest gap list, ranked by how often an agent would hit it

These are the missing primitives, in priority order. None are "more dressing" — they are missing core utility for the agent use case.

### Gap 1 — `events` query CLI (highest impact)
There is no first-class way to ask "give me the raw events". The closest thing is `digest list` + `digest show`, which goes through human-readable prose.

What's needed:
```
sharedwatch events list \
  [--since 2026-05-20T03:00:00Z] \
  [--type file.modified] \
  [--path-glob 'auth/**'] \
  [--source watcher] \
  [--status pending|processing|processed|failed] \
  [--limit N] \
  [--json]
```

The schema already supports every one of these filters. Indexes are already there for `(status, created_at)` and `(rel_path, status, created_at)`. This is a CLI gap, not a data-model gap. Probably 60 lines of code.

**Why it matters:** without this, every agent integration starts with "OK, let me look at the SQL schema..." That's the wrong shape for a tool that bills itself as an integration point.

### Gap 2 — Cursor / "what's new since" semantics
Currently the only way an agent can ask "what's new since I last looked" is to track a timestamp itself and filter `--since`. That works but:
- requires the agent to persist a cursor,
- has clock-skew gotchas if the agent's clock differs from the DB's,
- has tie-breaking gotchas when events arrive in the same nanosecond,
- doesn't survive event re-ordering (e.g. reconcile inserts old events later).

What's needed: a server-side cursor primitive.
```
sharedwatch events list --since-cursor <opaque-token>
  → returns events + a fresh cursor
sharedwatch events cursor --name my-agent → opaque token
```

Implementation: events already have monotonic `created_at` + unique `id`. Cursor = `(created_at, id)` tuple, base64'd. Optional persistence: `cursors` table keyed by name so the agent can do `--cursor-name my-agent` and the server does the bookkeeping.

**Why it matters:** this is THE primitive for the resume-context use case (A above). Without it, every agent re-implements the same bookkeeping.

### Gap 3 — Producer-supplied event metadata (`payload_json`)
The `events.payload_json` column exists in the schema. It is written but never read. There is no way for a producer to attach metadata to an event ("this change belongs to thread X", "this was made by agent Y", "this is part of the auth refactor").

What's needed:
- `sharedwatch test emit <relpath> --payload '{"thread":"auth","by":"agent-x"}'`
- A filter `events list --payload-jq '.thread == "auth"'` (or simpler: `--payload-key thread --payload-value auth`)
- Pass-through in the schema (already there)

For watcher-detected events (filesystem-observed), there's no way the watcher can tag with semantic metadata — it doesn't know which thread a file belongs to. But:
- a config-driven path-prefix → tags mapping would solve 80% of it,
- and synthetic-emit + agent-emit can fill in the rest.

**Why it matters:** for the many-threads case (D above), some way to associate events with the thread they "belong to" is the difference between "one folder per thread" (operationally painful) and "one DB, one shared root, filter by tag" (clean).

### Gap 4 — Read-without-consume
`consume` is destructive: it claims events (`pending → processing`), bundles them into a digest, marks them `processed`. After that, the events are still in the DB but they're no longer the agent's queue — they're history.

An agent reading the journal for "what happened recently" should not need to consume. Currently the agent's options are:
- Consume → events vanish from the "pending" view, can no longer be re-consumed by another agent.
- Query SQL directly → bypasses every helper.
- Read digests → prose, lossy.

What's needed: `events list` (Gap 1) reads without claiming. That's already what `digest list` does for digests. Need the same for events.

### Gap 5 — Multi-folder watching
One `WatchPath` per instance. To watch 5 folders you run 5 sharedwatch processes against 5 DBs. For a multi-thread agent, this is awkward.

Two reasonable fixes:
- **Easy:** document that "one folder, one instance" is the model, and add a `--root` config that supports a glob like `repo/*/specs/` so multi-folder use cases that fit a glob just work.
- **Harder:** make `WatchPath` a `[]string`, key snapshots by `(source, path)`, scan each.

Either lifts the awkwardness. The harder fix is more honest to what agents will do.

### Gap 6 — Content-diff / hash field
The schema has `content_hash`. It is never computed. For an agent answering "what did the human actually change in the spec", knowing only "file.modified" is not enough.

Two levels:
- **Cheap:** compute SHA-256 in `BuildSnapshot`, populate `Hash`. Then "modified" rows carry old+new hash so the agent can detect "metadata-only" mtime touches from "real" content changes.
- **More:** store a small `before`/`after` excerpt or a unified-diff text in `payload_json` for files under a size threshold.

Cheap level is a 10-line change with real value. Expensive level is a feature.

### Gap 7 — Producer attribution
All events have `source ∈ {watcher, reconciler, test}`. There is no per-producer identity. Multi-agent coordination (case D) wants: "show me events I made vs events anyone else made."

What's needed: an optional `producer_id` field on `Event`, defaulting to a hostname/pid or a configured agent name. Synthetic-emit takes a `--by <name>` flag.

This is a small schema migration. Worth doing once, before there's external usage to keep stable.

### Gap 8 — Path-glob filter on the watcher side
Right now if an agent wants to watch only `*.md` files in a folder full of binaries, it has to over-ignore. Inverse-glob (include-only patterns) would let the agent narrow the watcher's attention.

```
sharedwatch run --include '*.md' --include 'specs/**'
```

Lower priority than the others, but cheap.

## 5. What I would NOT add

Things that sound like missing features but aren't valuable for the agent case:

- **A push/notification mechanism** (webhook, IPC). Violates the queue-first principle and the agent doesn't need it — agents can poll cheaply. The whole point of the journal is that the consumer reads on its own schedule.
- **Multi-host / distributed storage.** Out of scope. If an agent needs cross-host, the right answer is to ship event rows through a real message bus or via filesystem replication; not to make sharedwatch distributed.
- **A "summarize this for me" LLM call.** The agent has its own model. The job of sharedwatch is to give the agent clean, structured data; not to do the agent's thinking for it.
- **A web UI.** Agents don't read UIs.
- **Auth / permissions.** Single-host SQLite; OS-level permissions on the data dir are sufficient.

## 6. So, would an agent use this as it stands?

**Honest answer: yes, but reluctantly.** A motivated agent can write `sqlite3 queue.db "SELECT * FROM events WHERE ..."` and get what it needs. The data is all there, the schema is clean, the durability and coalescing are real assets.

But the agent will not enjoy it, and it will feel like it's bypassing the tool's intended surface rather than using it.

If we close Gaps 1, 2, and 3 (events query CLI, cursor semantics, producer-supplied payload), the agent goes from "tolerating it" to "actually wanting it". Those three gaps are small mechanical work — none touch the core data model or design principles.

Concretely:
- **Gap 1** ≈ half a day of code.
- **Gap 2** ≈ half a day.
- **Gap 3** ≈ a couple of hours.
- All three together: one focused day.

After that pass, sharedwatch becomes a real **"agent-facing event journal for one folder"** — and the calmness-for-humans story (digests, modes, TTL) is one layer on top that agents can ignore.

## 7. The one-line positioning shift

Today's pitch (from the README I just wrote): *"calm, durable, pull-based activity feed for a local shared folder."* That's the human story.

The agent story underneath is: *"a durable, queryable journal of everything that happens to files in a folder, with noise reduction and drift recovery built in."*

Both are true. Both can ship. But if the goal is "agents use this to manage many threads of work", the journal framing is the one that earns the agent's adoption — and the missing primitives in §4 are what bridge the gap between "the data is there if you SQL" and "the tool feels built for me".

## 8. Recommendation

Decide which audience leads:

- **Lead with humans → keep current shape, mark agent integration as a v2 area.** Ship as-is.
- **Lead with agents → close Gaps 1–3 before opening up.** One day's work. Repositions the README around `events` as the primary surface, with `digest` as the human-friendly layer on top.
- **Both, honestly → ship Gaps 1–3 alongside the existing surface.** The cost is small enough that I'd default here. The README stays welcoming to humans, but the `events` subcommand and cursor semantics are first-class so an agent walking in can integrate without reverse-engineering the schema.

My recommendation is the third. The work is small relative to the credibility it buys.
