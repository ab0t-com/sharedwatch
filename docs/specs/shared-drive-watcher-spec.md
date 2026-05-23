# Shared Drive Watcher / Queue System Spec

## Owner
John

## Intended implementer
Sarah

## Purpose
Build a background system that watches the shared drive, converts file changes into queued events, and lets John consume those events on his own schedule without interrupting the main conversational process.

Primary shared folder:
- `/home/node/.openclaw/shared`

## Product goal
We want the shared folder to behave like a low-friction collaboration bus between team members.

The system should:
- detect file changes in the shared folder
- place those changes into a queue
- allow John to read/process queued updates every 5-10 minutes by default
- support a temporary real-time mode during active collaboration
- keep a heartbeat-style long-term safety check running in case the real-time watcher misses something
- avoid interrupting John’s main process directly

In short:
**capture instantly when useful, consume calmly by default.**

---

## Core principles

### 1. Queue-first, not interrupt-first
File activity should never directly interrupt the assistant runtime.

Instead:
- watcher detects change
- watcher writes normalized event into queue/storage
- consumer reads from queue on a schedule or when explicitly enabled for active work

This decoupling is the core design requirement.

### 2. Two-speed operation
The system has two operating modes:

#### Normal mode
- polling/consumption cadence: every 5-10 minutes
- optimized for low interruption
- best for passive awareness

#### Active collaboration mode
- near real-time consumption
- only enabled while John is actively collaborating on a shared-drive task
- should automatically time out back to normal mode after inactivity

### 3. Heartbeat as long-term safety net
Even if the watcher is running, a slower reconciliation pass should happen periodically.

Purpose:
- catch missed filesystem events
- rebuild consistency after restarts
- verify queue state against actual folder contents

### 4. Durable state
The system should survive restarts without losing track of:
- what files were already seen
- what events are pending
- what events were processed
- whether active collaboration mode is enabled

---

## High-level architecture

There are four logical components:

1. **Watcher**
   - listens for filesystem changes on `/home/node/.openclaw/shared`
   - turns raw filesystem signals into normalized events

2. **Queue**
   - durable storage for pending events
   - events wait here until consumed

3. **Consumer**
   - reads queued events on schedule
   - batches them into a digest John can review
   - marks events as processed

4. **Reconciler / heartbeat checker**
   - periodically scans the folder state directly
   - detects missed events or drift
   - enqueues synthetic recovery events if needed

---

## Functional requirements

## A. Watch scope
The system must monitor:
- file create
- file modify
- file rename/move within watched scope
- file delete

Optional later:
- subdirectories beneath `/home/node/.openclaw/shared`
- file content diff previews
- ignore patterns

For v1, assume recursive watch if practical. If recursive adds too much complexity, start with top-level only but design for expansion.

## B. Event model
Each detected change should become a normalized queue event.

Suggested event schema:

```json
{
  "id": "evt_...",
  "type": "file.created | file.modified | file.deleted | file.renamed",
  "path": "/home/node/.openclaw/shared/example.md",
  "relPath": "example.md",
  "oldPath": null,
  "timestamp": "2026-03-18T07:16:00Z",
  "source": "watcher | reconciler",
  "size": 1234,
  "mtime": "2026-03-18T07:15:58Z",
  "hash": "optional-content-hash",
  "status": "pending | processing | processed | failed | suppressed",
  "batchKey": "optional",
  "meta": {
    "coalesced": false
  }
}
```

Requirements:
- each event has stable unique id
- event timestamps stored in UTC
- status transitions are explicit
- enough metadata to let the consumer decide whether to read the file

## C. Queue behavior
The queue must be durable and disk-backed.

Recommended options:
- **SQLite** preferred for v1
- append-only JSONL acceptable only if Sarah strongly prefers simplicity and can guarantee safe writes/locking

Recommendation: **SQLite**.

Why:
- easy durability
- simple status transitions
- batching/querying is cleaner
- avoids JSONL corruption headaches

Queue requirements:
- insert event
- claim next pending events
- mark processed
- mark failed with retry count
- suppress duplicates/coalesce noisy modify bursts
- inspect backlog length

## D. Coalescing / noise control
Filesystem changes can be noisy. The system should reduce spam.

Rules for v1:
- multiple modify events for the same file within a short window should coalesce into one pending event
- create + modify in quick succession can collapse into one `file.created` event with latest metadata
- rename should preserve old and new path when detectable

Suggested coalescing window:
- 2-10 seconds configurable

## E. Consumption model
John should not be interrupted directly by new filesystem events.

Instead, the consumer should produce a digest/checkpoint that can be read during scheduled review.

Default consumption cadence:
- every 5 minutes during active collaboration periods
- every 10 minutes during passive periods

This can be dynamic, based on recent activity.

Suggested behavior:
- if queue received events in the last 30 minutes, use 5-minute consumption
- otherwise use 10-minute consumption

Consumer outputs should be batched summaries like:
- 1 new file from Sarah
- 3 modifications across 2 files
- deleted file notice

Each digest should reference exact files and timestamps.

## F. Active collaboration mode
There should be a controllable mode where shared-drive updates are consumed near real-time.

Intent:
- use during a live back-and-forth with Sarah
- revert automatically when the burst of collaboration ends

Requirements:
- can be enabled manually by John/system
- can have TTL, e.g. 15-30 minutes
- auto-extends if new activity continues
- falls back to scheduled mode after inactivity

Recommended real-time behavior:
- watcher still writes only to queue
- consumer polls queue every few seconds while active mode is on
- delivery still happens through a safe inbox/digest channel, not direct interruption of core runtime

This preserves the queue-first rule.

## G. Heartbeat / reconciliation
Need a slower, independent process that checks actual folder state.

Recommended cadence:
- every 30-60 minutes normally
- optionally once on startup

Responsibilities:
- compare current folder snapshot to last known snapshot
- detect missed creates/modifies/deletes
- enqueue recovery events with `source = reconciler`
- verify queue/database health
- optionally prune old processed events

## H. Non-interrupting delivery
This is critical.

The watcher/consumer system must **not** directly inject messages into John’s active conversational loop.

Instead, it should deliver updates into a side inbox John can check when appropriate.

Recommended delivery patterns:

### Preferred v1
Maintain a durable inbox file and/or inbox table such as:
- `workspace/.openclaw/shared-inbox.json`
- or SQLite table `digests`

John then reads from that inbox during:
- heartbeat checks
- scheduled collaboration checks
- explicit “check shared drive” requests
- active-mode short-interval review

### Optional v2
If OpenClaw later supports internal low-priority notifications, these could point John to the inbox without injecting full content.

But v1 should assume **pull, not push** for assistant consumption.

---

## State model

Suggested persisted state:

```json
{
  "mode": "passive | active",
  "activeUntil": "2026-03-18T07:45:00Z",
  "lastConsumerRun": "...",
  "lastReconcileRun": "...",
  "lastSnapshotHash": "...",
  "lastSeenFiles": {
    "hello-john-from-sarah.md": {
      "mtime": "...",
      "size": 608,
      "hash": "optional"
    }
  }
}
```

Even if SQLite is used, this mental model should hold.

---

## Suggested storage layout

Sarah can choose exact names, but something like this is clean:

- `/home/node/.openclaw/shared-system/`
  - `queue.db` — SQLite database
  - `state.json` — lightweight runtime state if needed
  - `logs/`
  - `config.yaml`

Or, if we want everything under workspace instead:
- `/home/node/.openclaw/workspace/.shared-watch/`
  - `queue.db`
  - `state.json`
  - `config.yaml`
  - `logs/`

My preference:
- operational files outside the human-facing shared folder
- not mixed into `/shared`

---

## Recommended database tables

### events
Stores normalized file events.

Fields:
- id
- type
- path
- rel_path
- old_path
- source
- status
- retry_count
- created_at
- observed_at
- processed_at
- file_size
- mtime
- content_hash
- coalesced_into
- payload_json

### digests
Stores consumer-generated batched summaries.

Fields:
- id
- created_at
- window_start
- window_end
- mode
- event_count
- summary_text
- status (`pending`, `read`, `archived`)

### snapshots
Stores reconciliation snapshots or summary checkpoints.

Fields:
- id
- created_at
- snapshot_json
- source

### runtime_state
Simple key/value or singleton table.

Fields:
- key
- value_json
- updated_at

---

## Processing flow

## Normal flow
1. file changes in shared folder
2. watcher observes event
3. watcher normalizes + coalesces
4. event inserted into queue with `pending`
5. consumer wakes on schedule
6. consumer claims batch of pending events
7. consumer generates digest
8. digest stored in inbox/digests table
9. events marked `processed`
10. John reads digest later during normal check loop

## Realtime collaboration flow
1. active mode enabled with TTL
2. watcher continues queueing events normally
3. consumer polls queue every few seconds
4. small digests generated quickly
5. John checks/consumes inbox in near real-time
6. mode expires back to passive after inactivity/TTL

## Recovery flow
1. reconciler scans actual folder state
2. finds drift or missed changes
3. emits recovery events into queue
4. consumer processes as normal

---

## Operational requirements

### Reliability
- service should restart cleanly
- no event loss on ordinary restarts
- duplicate events acceptable occasionally, but silent misses are worse
- reconciliation should minimize missed changes over time

### Observability
Need enough visibility to answer:
- is watcher running?
- how many pending events are queued?
- when was last consumer run?
- when was last reconcile run?
- are there failed events?
- is active mode on?

Minimum v1:
- structured logs
- simple CLI/status output
- backlog count

### Performance
Expected volume is low-to-moderate human collaboration, not high-throughput infrastructure logging.

Optimize for:
- simplicity
- correctness
- maintainability

Not for:
- extreme throughput
- distributed scaling

---

## Configurable settings

Need config for at least:
- watched path
- recursive true/false
- coalescing window
- passive consumer interval (default 10m)
- active consumer interval (default 5s to 30s range; Sarah can choose)
- activity threshold for switching 10m → 5m passive review
- active mode TTL
- reconcile interval (default 30m or 60m)
- max batch size
- retention/pruning policy
- ignore file patterns

---

## CLI / service expectations
Sarah is writing this in Go, so give it a simple operational surface.

Suggested subcommands:

- `sharedwatch run`
  - runs watcher + consumer + reconciler service

- `sharedwatch status`
  - shows mode, queue depth, last event, last consumer run, last reconcile run

- `sharedwatch mode active --ttl 30m`
  - enables active collaboration mode

- `sharedwatch mode passive`
  - forces passive mode

- `sharedwatch digest list`
  - lists unread digests

- `sharedwatch digest show <id>`
  - shows one digest

- `sharedwatch reconcile now`
  - manual reconciliation

- `sharedwatch test emit`
  - dev/testing only

If Sarah prefers a single daemon plus helper commands, that’s fine.

---

## v1 success criteria
The first release is successful if:
- a new or changed file in `/home/node/.openclaw/shared` gets recorded reliably
- events land in a durable queue
- John can review a digest without being interrupted mid-task
- active collaboration mode allows much faster review when desired
- a heartbeat/reconciliation pass catches missed changes eventually
- the system is simple enough for Sarah to maintain comfortably in Go

---

## Non-goals for v1
Do **not** overbuild this initially.

Not required in v1:
- full file content diff engine
- multi-user auth model
- network-distributed queue
- browser UI
- semantic clustering of changes
- direct live chat injection into John’s message stream

Those can come later if actually needed.

---

## My product judgment
If I were PMing this, I’d push Sarah toward this implementation order:

### Phase 1
- SQLite-backed queue
- filesystem watcher
- basic event normalization
- digest generation
- manual status command

### Phase 2
- reconciliation pass
- coalescing improvements
- active mode with TTL
- unread digest inbox

### Phase 3
- smarter batching
- better ignore rules
- optional content hashing/diff hints
- metrics/health polish

That gets us real usefulness fast without turning this into a platform project.

---

## Short brief for Sarah
Build a Go service that watches `/home/node/.openclaw/shared`, writes file events into a durable queue, and lets John consume batched updates on a schedule. Default review cadence is 5-10 minutes. During active collaboration, the system can temporarily switch to near real-time review. A separate heartbeat/reconciliation process must verify folder state over time and recover missed events. The queue must decouple detection from consumption so John is informed without being interrupted.
