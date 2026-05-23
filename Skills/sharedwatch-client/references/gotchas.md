# sharedwatch — gotchas, edge cases, and recovery

Things that are easy to misread or to get wrong. Skim once at agent boot; revisit when something surprises you.

## Contents
1. Coalesce window subtleties
2. Reconcile duplicates
3. Cursor pitfalls
4. Status lifecycle confusion
5. Content is NOT captured
6. Time resolution and ordering
7. Rename heuristic is heuristic
8. Polling cadence reality
9. SQL escape hatch — read-only enforcement
10. Recovery procedures

## 1. Coalesce window subtleties

- The default coalesce window is **5 seconds**, keyed on `rel_path` only.
- Two modifies to the same file within 5 s collapse into **one** visible event. The merged event's id is preserved; the swallowed event's id appears in `coalesced_into`.
- The current implementation does **not** consider `actor` when coalescing. **If two different agents modify the same file inside the window, their events merge and one actor's attribution wins.** This is a known limitation — see `tickets/` and `sharedwatch-disclosure-attribution-discussion-20260522.md`.
- Coalesce does NOT cross paths. Two different files inside the window stay distinct.
- Coalesce does NOT cross types. A `file.modified` won't fold into a `file.created` even if they're on the same path and inside the window.

**Diagnostic:** `SELECT id, coalesced_into FROM events WHERE rel_path='<x>' ORDER BY created_at`.

## 2. Reconcile duplicates

- The 30-min reconcile loop re-snapshots the folder and enqueues any drift it finds. This is the safety net behind "duplicates beat misses."
- You can see the **same logical change** twice: once with `source=watcher`, once with `source=reconciler`. They have different ids and different observed_at, but the same `rel_path` and (usually) the same `content_hash`.
- Idempotency is your responsibility as a consumer. **Dedup by `(rel_path, content_hash)`** when content_hash is populated; by `(rel_path, observed_at-bucket)` otherwise.
- The reconciler also acts on cold start: if you delete `queue.db` and restart, every existing file in the watched folder appears as a `file.created` with `source=reconciler`.

## 3. Cursor pitfalls

- **Cursor races.** Two agents (or two threads of one agent) sharing the same `--cursor-name` advance it past each other's reads. Each call gets a partial slice; together they see everything, but neither agent sees a complete stream alone. **Always namespace your cursor: `<actor>-<task>`** or include a UUID.
- **Filter-change silent skip.** A cursor reused with a different filter set silently skips events. Example:
  ```bash
  sharedwatch events list --cursor-name X --type file.modified --limit 10  # cursor advances past 10 modifies
  sharedwatch events list --cursor-name X --type file.created              # cursor is now past the modify position, so created events older than that are skipped
  ```
  **Rule:** new filter scope → new cursor name. Or use `--no-advance` to peek without committing.
- **Cursor advances on the last RETURNED row, not the last MATCHING row.** If `--limit` truncates, the cursor lands at the last returned event; the next call resumes from there.
- **Cursor iteration is always ASC** when a cursor is active, regardless of `--order`. The flag only affects display order of the page.

## 4. Status lifecycle confusion

Events transition: `pending → processing → processed | failed | suppressed`.

- **`processing` is the consumer's flag, not yours.** Do not filter for it in read queries unless you are debugging the consumer.
- **`pending` count** in `status --json` includes events that haven't been consumed yet. This is the right metric for "is there work in the queue?"
- **`failed`** is for events the consumer tried and gave up on. They can be retried with `sql --write "UPDATE events SET status='pending' WHERE status='failed'"` — but check first whether the underlying cause is fixed.
- **`suppressed`** is unused today; reserved for future filters.

Digests have their own lifecycle: `pending → read → archived`. `digest show <id>` marks read.

## 5. Content is NOT captured

- sharedwatch records *that* a file changed, its size, its mtime, and optionally a SHA-256 content_hash (only if `HashEnabled` AND `size <= HashMaxSize`, default 1 MB).
- It does **not** capture file contents. To read what's in a file, open the file directly.
- Hash-stable elision: if `content_hash` matches between two consecutive events on the same path, the content didn't actually change (someone touched the mtime without editing). Useful for filtering out spurious modifies.
- For files above `HashMaxSize`, `content_hash` is NULL — can't dedup or verify, just size/mtime.

## 6. Time resolution and ordering

- `created_at` and `observed_at` are RFC3339Nano (nanosecond precision). For events emitted inside the same reconcile pass, they may share the *same* timestamp.
- Filesystem `mtime` is filesystem-dependent: ext4 supports nanosecond, but some FSes (FAT, some network mounts) round to 1 s or 2 s. Don't rely on submillisecond ordering when reasoning about mtime.
- The event `id` (`evt_<hex>`) is the tie-breaker for stable ordering — cursors order by `(created_at, id)`.

## 7. Rename heuristic is heuristic

- Renames are detected by pairing a delete + create with the **same size and mtime**. If either differs (e.g., the FS bumps mtime on rename, or the file is rewritten as part of the move), you get two events: a `file.deleted` and a `file.created`.
- Renames do not pair across the coalesce window if their observed_at is too far apart. The heuristic is local.
- A move INTO the watched folder from outside is always a `file.created` (sharedwatch never saw the source).
- A move OUT of the watched folder is always a `file.deleted`.
- Bulk directory moves work (each file gets its own rename event) **if** the FS preserves mtime on rename — usually true on Linux, sometimes not on network FSes.

## 8. Polling cadence reality

- Passive watcher tick: 10 min default. Active: 5 s.
- "Active mode" is set with `sharedwatch mode active --ttl 30m`. It auto-decays to passive after the TTL.
- Reconcile cadence: 30 min, independent of mode.
- **The lowest-latency possible event observation is ~5 s** (active mode, immediately before a tick fires) **+ coalesce window** (~5 s) = ~10 s worst-case from filesystem write to event-in-queue.
- If you need sub-second reaction, sharedwatch is the wrong tool. Use `inotifywait` or write a custom Go program against `fsnotify`.

## 9. SQL escape hatch — read-only enforcement

- `sharedwatch sql "<sql>"` refuses anything that isn't `SELECT` / `WITH` / `EXPLAIN` / `PRAGMA table_info` unless `--write` is passed.
- The check is a leading-keyword scan after stripping comments/whitespace. It's a tripwire, not a security boundary.
- Multi-statement input (`;`) is rejected without `--write`.
- The DB file is on disk with normal POSIX perms. Anyone with shell access can `sqlite3 queue.db`.

## 10. Recovery procedures

For the full triage flow (symptom→action matrix, `events retry`, `events recover-stuck`, when-to-escalate), see `references/recovery.md`. Quick excerpts below.

### "I think I lost events"
Check the reconciler:
```bash
sharedwatch reconcile now
sharedwatch events list --source reconciler --since 1h
```
If reconcile produces events, the watcher missed them (often after a sleep/wake cycle or a crash).

### "Consumer is stuck"
Look for events in `processing`:
```bash
sharedwatch sql "SELECT COUNT(*) FROM events WHERE status='processing'"
```
If non-zero and the count isn't decreasing, the consumer crashed mid-batch. The `RecoverStuckProcessing` watchdog should flip them back to `pending` after the threshold — verify by waiting 5 min and re-checking.

### "Queue is growing unboundedly"
Check digest production:
```bash
sharedwatch sql "SELECT COUNT(*) FROM digests WHERE created_at > datetime('now','-1 hour')"
```
If zero in a folder with activity, the consumer isn't running. `sharedwatch status --json` to confirm. Re-`run` if needed (after killing the orphaned process; the file lock will prevent two `run`s).

### "DB is locked"
```bash
sharedwatch sql "PRAGMA busy_timeout=10000; SELECT 1"
```
WAL + `busy_timeout=5000` should make this rare. If persistent, suspect a long-running write transaction — usually a consume cycle. Wait or kill.

### "Schema mismatch / unknown column"
Always re-run:
```bash
sharedwatch schema --format json
```
The live DDL is authoritative. Cached schemas in your prompt go stale; refresh on session boot.
