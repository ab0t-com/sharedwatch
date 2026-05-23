# Recovery, failure modes, and operational rescue

How to get an unhealthy sharedwatch back to healthy. Drives the `events retry` / `events recover-stuck` / `reconcile now` triad, plus the diagnostic queries that tell you which one to reach for.

## Contents
1. The four-question health check
2. Symptom → action matrix
3. `events retry` — requeuing failed events
4. `events recover-stuck` — releasing stranded events
5. `reconcile now` — catching drift
6. SQL diagnostics
7. When to escalate (not a sharedwatch problem)

## 1. The four-question health check

Run these in order. Stop at the first abnormal answer.

```bash
# Q1: Is the daemon even running?
sharedwatch status --json | jq -e '.last_run' >/dev/null \
  || echo "no last_run timestamp — daemon may not have run yet"

# Q2: Are events being consumed?
sharedwatch status --json | jq '{pending, failed, processed, last_consume_at}'
# Healthy: pending bounded, last_consume_at recent.

# Q3: Are there stuck events?
sharedwatch sql "SELECT COUNT(*) FROM events WHERE status='processing'"
# Healthy: 0, or non-zero but decreasing.

# Q4: Are there failed events?
sharedwatch sql "SELECT COUNT(*) FROM events WHERE status='failed'"
# Healthy: 0.
```

## 2. Symptom → action matrix

| Symptom | Likely cause | First action |
|---|---|---|
| `pending` keeps growing | Consumer not running, or batch too small for incoming rate | `sharedwatch run &` if absent; check `mode` (active=5s, passive=10m) |
| `failed > 0` | Consumer hit an error (often transient: DB locked, disk full, malformed payload) | `sharedwatch events retry --max-retries 3` |
| `processing > 0` and not decreasing | Consumer crashed mid-batch | `sharedwatch events recover-stuck --older-than 5m` |
| `last_consume_at` is hours old | Daemon dead, file lock held, or DB locked | check `ps`, check `<data_dir>/sharedwatch.lock`, then `sharedwatch run &` |
| Events you expect are missing | Watcher missed them between snapshots | `sharedwatch reconcile now` |
| Events you don't expect appear (duplicates) | Reconciler re-emitted what the watcher captured | Expected — dedup by `(rel_path, content_hash)` in the consumer agent |
| `digest list` returns nothing in a busy folder | Consumer running but events not transitioning | `events recover-stuck` then `consume` manually |
| `sharedwatch run` exits immediately | File lock held by another instance | `lsof <data_dir>/sharedwatch.lock` — kill the holder, then retry |
| `sql --write` rejected | You forgot `--write` | Add `--write`, or reconsider whether you really want to mutate the journal |

## 3. `events retry` — requeuing failed events

```bash
sharedwatch events retry                  # requeue ALL failed events
sharedwatch events retry --max-retries 3  # skip events that have already failed N+ times
```

Behavior: flips `status='failed'` → `status='pending'` for matching rows. The consumer will pick them up on the next cycle.

**Before retrying, find out why they failed.** Common causes:
- Disk full (consumer wrote `digests` row, couldn't follow up)
- Payload JSON malformed (rare; the watcher validates on write)
- DB lock contention (timed out; usually transient)
- Bug introduced in a recent consumer change

```bash
# What kinds of failures are we seeing?
sharedwatch sql "SELECT type, COUNT(*) FROM events WHERE status='failed' GROUP BY type"
sharedwatch sql "SELECT id, rel_path, retry_count FROM events WHERE status='failed' ORDER BY retry_count DESC LIMIT 10"
```

Use `--max-retries N` to avoid infinite retry loops on a poison message. `retry_count` is bumped each time the consumer fails the event; `--max-retries 3` skips anything with `retry_count >= 3`.

## 4. `events recover-stuck` — releasing stranded events

```bash
sharedwatch events recover-stuck                # default --older-than 5m
sharedwatch events recover-stuck --older-than 10m
```

Behavior: any event in `status='processing'` whose `observed_at` is older than the threshold gets flipped back to `pending`. Used when a consumer crashed between `ClaimPendingEvents` and `MarkEventsProcessed`.

When to bump the threshold:
- Large batch sizes on slow disks → 10–15 min.
- Consumer doing heavy per-event work (custom downstream) → match its p95 latency × 2.

Idempotent and safe: running it twice with the same threshold returns 0 the second time.

## 5. `reconcile now` — catching drift

```bash
sharedwatch reconcile now
# → reconcile checkpoint recorded recovery_events=<N>
```

Behavior: rebuilds a fresh snapshot of the watched folder, diffs against the last reconcile snapshot, emits recovery events for anything missing. Catches:
- Events the live watcher missed during a sleep/wake cycle
- Files created during a restart window
- Mtime-only changes that were too subtle for the burst-coalesce window

Recovery events have `source='reconciler'`. If your dedup logic groups by `(rel_path, content_hash)`, the duplicate from a watcher+reconciler double-detection collapses naturally.

Running it on a healthy system should return `recovery_events=0` most of the time. Persistent non-zero counts indicate the watcher cadence is too slow for the workload.

## 6. SQL diagnostics

Reusable one-liners. All read-only.

```sql
-- Lifecycle distribution
SELECT status, COUNT(*) FROM events GROUP BY status;

-- Oldest pending event (queue head)
SELECT MIN(created_at) AS oldest_pending FROM events WHERE status='pending';

-- Failed events grouped by error pattern (retry_count is the signal)
SELECT retry_count, COUNT(*) FROM events WHERE status='failed' GROUP BY retry_count;

-- Stuck processing events (age)
SELECT id, rel_path, observed_at,
       ROUND((julianday('now') - julianday(observed_at)) * 24 * 60, 1) AS minutes_stuck
FROM events WHERE status='processing' ORDER BY observed_at ASC LIMIT 20;

-- Watcher vs reconciler ratio (drift indicator)
SELECT source, COUNT(*) FROM events
WHERE created_at > datetime('now','-1 day') GROUP BY source;
-- High reconciler ratio means the watcher is missing too much.

-- Coalesce ratio (burst intensity)
SELECT
  SUM(CASE WHEN coalesced_into IS NOT NULL THEN 1 ELSE 0 END) AS coalesced,
  SUM(CASE WHEN coalesced_into IS NULL     THEN 1 ELSE 0 END) AS standalone
FROM events WHERE created_at > datetime('now','-1 day');

-- Digest production rate
SELECT date(created_at) AS day, COUNT(*) FROM digests
WHERE created_at > datetime('now','-7 days') GROUP BY day;
```

## 7. When to escalate (not a sharedwatch problem)

Some failures look like sharedwatch problems but aren't. Recognize them and route correctly:

- **Disk full.** `df -h` on the data dir. sharedwatch doesn't manage disk; clear space (do NOT `rm` from the watched folder, that creates events).
- **SQLite WAL grown huge.** `ls -la <data_dir>/queue.db-wal`. If > 100 MB, force a checkpoint: `sharedwatch sql --write "PRAGMA wal_checkpoint(FULL)"`.
- **File lock orphaned.** Another `sharedwatch run` died without releasing `<data_dir>/sharedwatch.lock`. Verify no PID holds it (`lsof`), then remove the lock file by hand.
- **Filesystem mounted with `noatime` causing mtime issues.** Not a sharedwatch bug. The watcher uses mtime to detect changes; weird mount options can confuse it.
- **Network filesystem (NFS/SMB) with stale-handle errors.** sharedwatch is designed for local disks. NFS may work; SMB may not. Document the deployment and move on.
- **DB schema differs from what the code expects.** You're running an older binary against a newer DB or vice versa. Check `sharedwatch version` against the schema migration in source.

For genuine bugs (sharedwatch behavior contradicts the docs / tests), open a ticket in `tickets/` per the conventions in `sharedwatch-contributor`.
