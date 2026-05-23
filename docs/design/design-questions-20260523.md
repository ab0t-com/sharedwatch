# sharedwatch — design questions (Q&A log)

**Date opened:** 2026-05-23
**Status:** living document — append new questions as they surface
**Purpose:** durable record of the "what is sharedwatch *for*" questions and the answers we've worked out, so the next contributor (human or agent) doesn't have to re-derive them.

The questions in this doc are not feature requests. They are framing questions — the kind that, if answered wrong, lead to building the wrong product. Each is preserved with the user's verbatim wording (the question that triggered the discussion) and a worked-out answer.

---

## Q1 — Could sharedwatch be a git replacement?

**Question (verbatim):** *"general question, would this be a git replacement? liek could we walk file state with something like this? or no"*

### Short answer

**No, not a git replacement** — but the question is interesting and the gap is illustrative of what sharedwatch *is* vs what it's not.

### Where they overlap

sharedwatch and git both record "who changed what when":

| | git | sharedwatch |
|---|---|---|
| Records what changed | yes (path) | yes (path) |
| Records who | author + committer | producer_id + payload.actor |
| Records when | commit timestamp | created_at, observed_at, mtime |
| Records why | commit message | payload.intent / payload.task |
| Per-file fingerprint | SHA-1 blob hash | optional SHA-256 content_hash |
| Causality | parent commit refs | payload.ref_event_id |

So they're cousins. Both are append-only event logs of filesystem activity with attribution.

### Where the gap is total — and why it's a deal-breaker

The critical missing piece is **content**. sharedwatch records *that* a file changed and (optionally) its hash. It does NOT store the bytes. You can't:

- Reconstruct the file as it existed at any past point
- Diff two arbitrary moments
- Revert to a prior version
- Recover a deleted file
- Cherry-pick

git's whole superpower is the content-addressed blob store. Every byte you've ever committed is in `.git/objects/`. Pull any tree from any commit and it materializes the exact state. sharedwatch has a snapshots table but it stores file *metadata* (path/size/mtime/hash), not contents — and only keeps the latest 5 per source.

The other big gaps:

- **No branching / DAG.** sharedwatch is linear time. Git is a directed graph of commits.
- **No merge model.** Coalesce drops events; it doesn't reconcile divergent histories.
- **Pull-side ambient observer, not actor-driven commits.** sharedwatch sees what happened; git records what you decided.
- **Single host.** Git is distributed by design.

### What you CAN walk today

- The **event timeline** — chronologically, by actor, by path, by session. Rich attribution.
- **The current snapshot** — file list with sizes/mtimes/hashes (if hashing on).
- **Causal chains** — follow `ref_event_id` parents/children.
- **Per-actor activity slices** — "what did claude-X touch this week."

What you can't walk: *file contents at any past point*.

### What would close the gap (partly)

Feature #9 from the predicted-features list (content blob store) would give you "show me what `auth/login.go` looked like at event `evt_abc123`" — a time-travel filesystem journal. Useful for:
- Debugging multi-agent flows ("what did claude-Y see when they made this decision?")
- Audit ("reconstruct the file state on this date")
- Forensics ("when did this regression first appear?")

Even with that, sharedwatch wouldn't be a git replacement — no branches, no merges, no conflict resolution, no distributed model. It'd be a **content-aware activity journal**. Different category.

### The accurate analogy

sharedwatch is closer to:
- `inotify` / `fsnotify` with persistence + attribution
- An audit log with content fingerprints
- A "what happened in this folder" feed designed for AI agents to consume

It's a complement to git, not a substitute. A team using sharedwatch in their git repo would have: git for *intentional commits and history*; sharedwatch for *ambient awareness of what agents are doing in the workspace right now*. The two answer different questions:

- git: "what does the project look like at this commit?"
- sharedwatch: "what's happening in the folder right now, and who's doing it?"

If you wanted real version control with the multi-agent attribution sharedwatch provides, the right move is probably to **wrap git** with payload-tagged commits (e.g., agents commit with structured trailers carrying actor/session/task) rather than rebuild git's content model inside sharedwatch.

**Bottom line:** sharedwatch could grow toward "git-like time travel" via the content blob store, but it's the wrong tool for version control. Wrong center of gravity. The two stay friends, not replacements.

---

## Q2 — What's the point of knowing a file changed without knowing what happened?

**Question (verbatim):** *"Okay but whats the point in knowning a file changed state without knowing what happened? that is pointless ifnormatuion? is it now what is the orginal intent? to know what llms team mates have been doing? and sub to that? how can we sub to that? Do you mind storing that above document with the question I asked, (also we don't want version control but we DO wnat to see what changed not just toched files, toched files are good but empty of information and not actionable for agents rigth?"*

**Clarification from the user before answering:** *"don't assume any agents, they are just the users. but humans can also use it."*

### Reframing first — what sharedwatch is and isn't

sharedwatch is a **filesystem watcher with a durable, queryable event journal**. It does not assume anything about who or what is writing the files. The writers — the entities that actually modify bytes on disk — can be **anything**: a human in vim, a build system, a backup script, an IDE plugin, an AI agent, a network sync daemon, `cp`. The watcher sees what the kernel/FS shows it: a file at path P changed at time T, here's its new size and mtime, here's its new SHA-256 if hashing is on. That's the floor of what's available.

The **users of the journal** — the readers — are also anything: humans browsing `digest show`, scripts piping `events list --format jsonl` into jq, dashboards aggregating activity, AI agents reacting to changes, audit log collectors. Some users coincide with writers (an AI agent that reads the journal AND writes files), but they don't have to.

This separation matters for the question because:
- **The watcher cannot fabricate information the FS doesn't reveal.** If the writer didn't volunteer "what they did," and we don't have content + diff, the watcher only knows that path P changed.
- **Cooperation is optional and partial.** Writers that can annotate (AI agent CLIs, git hook wrappers, IDE plugins, anything invoked through `sharedwatch test emit --intent "..."` or `sharedwatch run --actor X`) get rich events. Writers that can't (vim, cp, a Dropbox sync daemon) produce touch events only. Both kinds coexist in the same journal.

So the honest answer to "is touch info pointless?" depends on what you're trying to do with it.

### Touch events are not pointless — they're a different kind of signal

A pure `file.modified auth/login.go at 14:32` event is a **trigger**, not a **description**. Triggers are valuable even without rich semantic content:

- **Alarm / drift detection** — "this file should not have changed; investigate."
- **Wake-ups** — "the spec doc was created; the reviewer should look."
- **Liveness / activity heartbeat** — "this folder is active; the project isn't dormant."
- **Coordination signal** — "someone wrote where I was about to write; check before proceeding."
- **Audit trail** — "everything that changed over the weekend, by path and timestamp."
- **Trigger downstream work** — `npm run build` on `src/*.js` change; reload preview on `*.md`; re-run tests on `*_test.go`.
- **Activity counters / dashboards** — top paths by churn, hourly busy times, who edits when.

None of these require knowing *what* changed. They only require knowing *that* it changed and *when* and (sometimes) *who* (via process attribution or hash continuity).

This is what `inotify` / `fswatch` / `watchdog` give you, and an entire class of useful tools (auto-reloaders, build systems, sync clients) is built on it. sharedwatch's superpower over those tools is **persistence + attribution + multi-reader cursors** — but the underlying signal is still "the FS revealed this."

### Where touch events DO fall short

If your use case is **"I want to understand what someone (or something) did to this file"**, touch events are thin. The watcher cannot tell you:

- *Why* the file changed (intent — only the writer knows)
- *What* logically changed inside the file (semantic operation — refactor? formatting? bugfix?)
- *Which bytes* changed (requires content + diff)
- *Whether* the change is significant (vs. whitespace-only, mtime-only, hash-stable)

The fourth one we *can* partially close even without writer cooperation: content_hash continuity lets you distinguish "real" content change from mtime-only touches. The first three require either writer cooperation, content storage, or external enrichment.

### What we have today vs what's missing

| Signal | Source | v0.8.0 status |
|---|---|---|
| Which file changed (path) | watcher | ✅ event row |
| When (observed_at, mtime) | watcher | ✅ |
| New size | watcher | ✅ |
| Content hash (real change vs touch) | watcher (opt-in) | ✅ `--hash on` |
| Process identity | OS (not currently captured) | ❌ — `producer_id` is the daemon's, not the writer's |
| Why (writer's intent) | writer cooperation | ⚠ voluntary via `payload.intent`; nothing forces non-cooperative writers |
| What semantically changed | writer cooperation OR diff inference | ❌ neither in v0.8.0 |
| Actual content / diff | content store + diff | ❌ no content stored (by design — see Q1) |
| Push subscription | watcher surface | ❌ pull-only (named as feature #2) |

The honest gap: **for non-cooperative writers, we have nothing beyond touch + hash + size delta.** That's a real boundary, not a bug.

### Three layers that could close the gap (each with different cost + scope)

**Layer 1 — Writer cooperation (cheap, voluntary, already partly there).**

Writers that can annotate already do, via `payload_json`. We could enrich the convention with structured keys — `operation`, `summary`, `lines_added`, `lines_removed`, `files_affected` — and ship CLI helpers / library bindings for the common write patterns:

- A `sharedwatch wrap <command>` wrapper that infers operation type from the command (`sed -i` → modify, `mv` → rename, `git commit` → commit, with optional `--summary "..."`).
- A git post-commit hook that emits an enriched event when a commit lands in a watched repo.
- An IDE plugin (vscode, JetBrains) that emits on save with the editor's diff summary.

This unlocks rich events for cooperative writers (any writer running through one of these wrappers) without changing the watcher's contract. Non-cooperative writers still produce touch events; that's accepted.

**Layer 2 — Watcher-side inference (medium cost, partial, no cooperation required).**

When the watcher sees a `file.modified` event, it could:
- Compute the byte diff vs. the prior snapshot's hash (requires content store).
- Run a file-type-aware summarizer for known formats (markdown line diff, go function add/remove, json key add/remove). Small heuristics, large value.
- Detect "pure" changes: mtime-only (skip), whitespace-only (note), formatter run (e.g., `gofmt`), generated-file regenerate (e.g., `_test.go` autogenerated).
- Classify operation from the diff shape: pure-addition, pure-deletion, balanced refactor.

This works regardless of writer cooperation. Cost: needs content store (Layer 3) for diffs; pure heuristics work on hashes + sizes alone but less informatively.

**Layer 3 — Content storage + diff (high cost, complete, by-design absence today).**

Store the bytes (or a compressed/dedup'd version) alongside each event. Subscribers can call `content show <event>` or `content diff <eventA> <eventB>` to see the literal change. Storage cost is real; mitigations: opt-in flag, per-root opt-in, size cap, retention by age, content-addressable dedup so identical bytes only store once.

This is what Q1 (git replacement) sketched. Even with it, sharedwatch isn't git — no branches, no merges. But it does become a **time-travel filesystem journal** that can answer "what did the file look like at this past moment."

### Combining the layers — and what the "subscribe to teammates' activity" feature looks like

To answer the user's specific subscribe question — "how can we sub to what teammates are doing?":

**1. A push surface** (feature #2 — `events watch`): long-poll/SSE stream of new events matching a filter. Replaces the polling tax for any consumer.

**2. Filter primitives that work on whatever fidelity is available:**
```bash
# Touch-level subscription — works for any writer (no cooperation needed)
sharedwatch events watch --root <X> --since-cursor my-cursor

# Exclude your own writes (useful when consumer and writer are the same entity)
sharedwatch events watch --not-producer self-pid --since-cursor my-cursor

# Filter to events that carry semantic content (only catches cooperative writers)
sharedwatch events watch --payload-key operation --since-cursor my-cursor

# Combine: live stream of cooperative-writer activity addressed to me
sharedwatch events watch \
  --payload-key addressee --payload-value me \
  --not-producer self-pid \
  --format jsonl
```

The subscriber chooses the fidelity bar. A dashboard for "show me activity" subscribes to all events. A reviewer agent for "show me deliberate work others did" filters to `--payload-key operation`. A build system subscribes to `--type file.modified --path-glob 'src/**/*.go'` and doesn't care about who or why.

### How this reshapes thinking about the roadmap

The original predicted-features list ranked content storage (#9) as "medium need, low fit (storage)." With this clearer framing:

- **Layer 1 (richer cooperative payload + wrappers)** is cheap, additive, valuable for the agent-and-tool subset of writers. Should be v0.9.
- **Layer 2 (watcher-side inference)** depends on Layer 3 (content) for diff-based inference, but format-aware heuristics on hashes alone (mtime-only, hash-stable elision) are easy and already partially implemented. Should be opportunistically added.
- **Layer 3 (content storage)** is the heavyweight option that unlocks the full "what changed in the bytes" capability — regardless of writer cooperation. Higher cost; should be opt-in per root. Probably v0.9.x or v1.0.
- **Push surface (#2)** becomes essential the moment any consumer needs near-real-time enrichment. Should be v0.9.

### Bottom line — what to tell users

When someone asks "is touch info pointless?", the honest answer is:

- **It's not pointless** — it's the right primitive for triggers, alarms, coordination signals, dashboards, build-system wake-ups, audit trails.
- **It IS empty of intent** — for understanding *why* or *what semantically* changed, you need either (a) the writer to have annotated, or (b) the watcher to have stored content + done a diff.
- **The watcher faithfully captures what the FS reveals.** Anything richer is opt-in — by the writer (annotated emit), by the operator (turn on `--hash` and `--capture-content`), or by an external enrichment layer (correlate with editor history, git logs, etc.).
- **The product question is: what enrichment mechanisms does sharedwatch ship?** The answer should be a spectrum (wrappers + content store + format-aware inference) rather than a single answer.

---

## Q3 — Could we run `git add . && git commit` on every change to capture content history?

**Question (verbatim):** *"Okay what if we used the shaared watch to run a git add . && git commit on very change? is that viable or no?"*

**Follow-up constraints from the user:**
- *"if we did use git it shouldn't be the normal one it would be hidden out of the folder thing so it doens't interfare wtih any other git"*
- *"have a strong understand of what llms want to see, almost like a time series of diff changes"*

### Short answer

**Auto-commit on every change is not viable.** But the underlying insight — that git's content-addressed blob storage is exactly the right shape for the content layer sharedwatch needs — is sound. The clean version of the idea drops commits entirely and uses only the blob-storage layer, in a hidden sharedwatch-owned directory that never touches the user's actual git repos.

The full evaluation is in [`sharedwatch-content-storage-evaluation-20260523.md`](sharedwatch-content-storage-evaluation-20260523.md). This Q&A summarizes the conclusions; the evaluation doc carries the worked reasoning.

### Why auto-commit-on-change is not viable

Six concrete reasons:

1. **Commit storm pollutes history.** Every saved file becomes a commit; a coding session = thousands. `git log --oneline` becomes a wall of "auto-commit at T" entries instead of meaningful change boundaries; `git bisect`/`git blame`/PR review lose their value.
2. **Mid-edit race conditions.** Saves often happen in stages (write `.swp` → atomic rename). Auto-commits capture half-written states.
3. **Wrong semantic level.** Commits are *deliberate*; sharedwatch events are *ambient*. Conflating the two loses the "this was a thought-through change" property of git.
4. **Forever-stores transient content.** Every accidentally-pasted secret, debug log, or `.env` edit lives in `.git/objects/` permanently. Real privacy hazard.
5. **Conflicts with intentional git use.** Most watched folders are already real git repos. Auto-commits interleave with deliberate ones; `git push` becomes a privacy minefield.
6. **Concurrent writer races.** Two agents writing simultaneously → two `git commit` calls → git's index locking serializes but second can fail mid-way. The lease/intent advisory layer doesn't help — git is unaware of it.

### The clean version of the idea

Drop commits, keep the blob storage:

```bash
# For each file.modified event, just store the bytes (no commit, no index, no working tree):
sha=$(sharedwatch-blob-store write <bytes>)         # zlib-compressed, content-addressed
# Store sha in event_content.content_hash; the bytes are reachable later via:
sharedwatch content show <event-id>                  # fetches blob, decompresses, returns
```

This is what EdenFS, Buck/Bazel's remote cache, and other tools do — reuse git's blob *format idea* without adopting git's commit/history layer.

### The hidden + isolated constraint (locked)

Per the user's stipulation, any blob storage MUST:

- Live in `<data_dir>/blobs/` — **never** inside the watched folder.
- Be sharedwatch-owned — **never** a git repo (no refs, no index, no working tree).
- Be invisible to user-facing git tooling. `git status` in the watched folder shows nothing about it; `git push` doesn't push it.
- NOT require the user's watched folder to be a git repo at all (sharedwatch's blob store is independent of any working tree).

Three implementation paths considered:

- **α** — sharedwatch-owned, git-format-compatible (loose objects, `blob <size>\0` header, SHA-1, zlib). Bonus: `git cat-file -p <hash>` can read the blobs.
- **β** — vendor `go-git/v5` (~30k LOC). Battle-tested; large dep.
- **γ** — sharedwatch-owned, NOT git-compatible (SHA-256, zlib, ~150 LOC, no new dep). **Recommended.**

Path γ satisfies the constraint without using git at all — only the *idea* of content addressing.

### What LLM consumers want — the framing that decides the answer

The user's framing: *"almost like a time series of diff changes."*

That makes the answer clear. LLM queries that matter are time-series queries over file content:

- "How has `auth/login.go` evolved over the last hour?"
- "Walk me through each modification claude-X made to this file."
- "What did the file look like at event N?"
- "Diff the file's state between any two events."
- "Reconstruct historical state to ground a follow-up question."

Storing only the diff (the original SW-AGENT-16 design) answers the first two well but fails on points 3, 4, 5. Storing content blobs answers all five.

| LLM query | diff_gz only | content blob store |
|---|---|---|
| "What changed at event N?" | ✅ direct | computed on-demand from blob_N vs blob_N-1 |
| "What did the file look like at event N?" | ❌ no content | ✅ retrieve blob |
| "Diff between events A and E?" | ⚠ chain forward | ✅ direct |
| "Reconstruct file state 3 days ago" | ❌ | ✅ retrieve blob from that day |
| "Walk the time-series with diffs" | ✅ | ✅ |

### Storage cost (honest numbers)

For a busy workspace (10 active writers, 1000 modifications/day, avg 20 KB files, 2 KB diff):

- diff_gz only: ~180 MB/year
- blob store (with dedup): ~870 MB/year
- Hybrid: ~1 GB/year

~5× more for the blob store but comfortably bounded on any modern disk.

### Decision implications

If the team accepts the evaluation's recommendation:

1. **SW-AGENT-16's `event_diffs` table becomes `event_content`** — single `content_hash` column referencing the blob store, no diff text in the DB.
2. **`sharedwatch diff <a> <b>` computes on-demand** from the two blobs using the same `pmezard/go-difflib` lib SW-AGENT-16 already plans to vendor.
3. **`sharedwatch content show <event-id>` becomes possible** — currently impossible with diff-only storage.
4. **`sharedwatch path history` and `path replay` become first-class** — the time-series consumption that motivated the v0.9.0 work.
5. **Blob storage lives at `<data_dir>/blobs/<sha256[:2]>/<sha256>`** — hidden, sharedwatch-owned, never inside the watched folder, never a git repo.
6. **Default off** — opt-in via `--capture-content on` or `capture_content: true` in config.

Risks (all addressable; named in the evaluation doc): blob store + SQLite not transactional together; blob directory needs GC; backup must include both `queue.db` AND `blobs/`; high-churn workspaces could grow many inodes.

The user's "git on every change?" question landed in exactly the right place — not the commits (rejected), but the content-addressed blob storage layer git pioneered. Sharedwatch can have that capability cleanly, hidden, isolated.

### Status

**Under review.** SW-AGENT-16 has a stop-the-line warning at the top pointing at the evaluation doc; implementation should not start until the team picks Option A / B / C. Recommendation: Option B + Path γ + opt-in default off.

---

## Q4+ — Reserved for future questions

Append new entries below as they surface. Each should include the verbatim question, a worked-out answer, and any design implications.

<!-- end of file -->
