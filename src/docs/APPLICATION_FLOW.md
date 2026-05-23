# APPLICATION FLOW

## Normal flow
1. Watch shared folder.
2. Normalize raw file events.
3. Insert durable pending events into SQLite.
4. Consumer claims pending events on schedule.
5. Consumer writes digest rows.
6. Events become processed.
7. John reads digests when appropriate.

## Active flow
1. Active mode enabled with TTL.
2. Watcher still queues events only.
3. Consumer polls more frequently.
4. Digests are smaller and faster.
5. Mode falls back to passive after TTL/inactivity.

## Recovery flow
1. Reconciler scans folder state.
2. Drift becomes recovery events.
3. Queue + digest pipeline handles them normally.
