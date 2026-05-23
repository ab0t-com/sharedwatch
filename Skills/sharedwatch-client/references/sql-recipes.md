# sharedwatch — SQL recipe book

Copy-paste SQL for queries that don't have a first-class CLI surface. All read-only — wrap in `sharedwatch sql "..."`.

Run `sharedwatch schema --format json` first if you need to confirm column names.

## Contents
1. Activity overview
2. Per-actor queries
3. Per-path queries
4. Time-bucketed activity
5. Coalesce / dedup diagnostics
6. Cursor diagnostics
7. Health and operational

## 1. Activity overview

```sql
-- How many events of each type in the last 24h?
SELECT type, COUNT(*) AS n
FROM events
WHERE created_at > datetime('now', '-1 day')
GROUP BY type
ORDER BY n DESC;

-- Total events vs pending vs failed
SELECT
  COUNT(*)                                                  AS total,
  SUM(CASE WHEN status = 'pending'    THEN 1 ELSE 0 END)    AS pending,
  SUM(CASE WHEN status = 'processing' THEN 1 ELSE 0 END)    AS processing,
  SUM(CASE WHEN status = 'failed'     THEN 1 ELSE 0 END)    AS failed,
  SUM(CASE WHEN status = 'processed'  THEN 1 ELSE 0 END)    AS processed
FROM events;

-- Reconcile vs watcher attribution mix
SELECT source, COUNT(*) FROM events GROUP BY source;
```

## 2. Per-actor queries

```sql
-- Top actors by event count, last 24h
SELECT
  json_extract(payload_json, '$.actor') AS actor,
  COUNT(*) AS events,
  MAX(created_at) AS last_seen
FROM events
WHERE created_at > datetime('now', '-1 day')
  AND json_extract(payload_json, '$.actor') IS NOT NULL
GROUP BY actor
ORDER BY events DESC
LIMIT 10;

-- Activity by one specific actor
SELECT type, rel_path, observed_at,
       json_extract(payload_json, '$.task') AS task,
       json_extract(payload_json, '$.intent') AS intent
FROM events
WHERE json_extract(payload_json, '$.actor') = 'claude-coordinator-1'
  AND created_at > datetime('now', '-1 hour')
ORDER BY created_at DESC;

-- Unattributed events (quality metric for cooperative attribution)
SELECT COUNT(*) AS unattributed_count
FROM events
WHERE created_at > datetime('now', '-1 day')
  AND (payload_json = '{}' OR json_extract(payload_json, '$.actor') IS NULL);

-- Events addressed to a specific actor (handoffs to consume)
SELECT id, rel_path, observed_at,
       json_extract(payload_json, '$.actor') AS sender,
       json_extract(payload_json, '$.task') AS task
FROM events
WHERE json_extract(payload_json, '$.addressee') = 'claude-me-1'
ORDER BY created_at DESC
LIMIT 20;
```

## 3. Per-path queries

```sql
-- Top paths by churn, last 24h
SELECT rel_path, COUNT(*) AS events, MAX(created_at) AS last_at
FROM events
WHERE created_at > datetime('now', '-1 day')
GROUP BY rel_path
ORDER BY events DESC
LIMIT 20;

-- Full history for one path
SELECT id, type, source,
       json_extract(payload_json, '$.actor') AS actor,
       observed_at
FROM events
WHERE rel_path = 'auth/login.go'
ORDER BY created_at ASC;

-- Files touched but not since (stale work?)
SELECT rel_path, MAX(created_at) AS last_at
FROM events
GROUP BY rel_path
HAVING last_at < datetime('now', '-7 days')
ORDER BY last_at ASC
LIMIT 20;
```

## 4. Time-bucketed activity

```sql
-- Events per hour, last 24h
SELECT strftime('%Y-%m-%d %H:00', created_at) AS hour, COUNT(*) AS n
FROM events
WHERE created_at > datetime('now', '-1 day')
GROUP BY hour
ORDER BY hour;

-- Events per day, last 30 days
SELECT date(created_at) AS day, COUNT(*) AS n
FROM events
WHERE created_at > datetime('now', '-30 days')
GROUP BY day
ORDER BY day;

-- Quietest hours (good for batch operations)
SELECT strftime('%H', created_at) AS hour, COUNT(*) AS n
FROM events
WHERE created_at > datetime('now', '-7 days')
GROUP BY hour
ORDER BY n ASC;
```

## 5. Coalesce / dedup diagnostics

```sql
-- How often does coalesce merge events?
SELECT COUNT(*) AS coalesced_count
FROM events
WHERE coalesced_into IS NOT NULL;

-- Largest coalesce chains (find runaway editors)
SELECT coalesced_into AS survivor, COUNT(*) AS merged_in
FROM events
WHERE coalesced_into IS NOT NULL
GROUP BY survivor
ORDER BY merged_in DESC
LIMIT 10;

-- Dedup by (rel_path, content_hash) to find watcher/reconciler duplicates
SELECT rel_path, content_hash, COUNT(*) AS dupes
FROM events
WHERE content_hash IS NOT NULL
GROUP BY rel_path, content_hash
HAVING dupes > 1
ORDER BY dupes DESC
LIMIT 20;
```

## 6. Cursor diagnostics

```sql
-- All named cursors and their positions
SELECT name, last_id, updated_at
FROM cursors
ORDER BY updated_at DESC;

-- Cursors that are way behind (haven't advanced in days)
SELECT name, updated_at,
       julianday('now') - julianday(updated_at) AS days_stale
FROM cursors
WHERE days_stale > 1
ORDER BY days_stale DESC;
```

## 7. Health and operational

```sql
-- Failed events (potential consumer issues)
SELECT id, type, rel_path, retry_count, observed_at
FROM events
WHERE status = 'failed'
ORDER BY observed_at DESC
LIMIT 20;

-- Snapshots retained per source (should be ≤ 5)
SELECT source, COUNT(*) FROM snapshots GROUP BY source;

-- Digest production rate, last 24h
SELECT mode, COUNT(*), AVG(event_count) AS avg_events_per_digest
FROM digests
WHERE created_at > datetime('now', '-1 day')
GROUP BY mode;

-- Runtime state — what does sharedwatch *think* is happening?
SELECT key, value_json, updated_at FROM runtime_state;
```

## Pattern: combine with `--format jsonl` post-processing

For richer post-processing, prefer to pull JSONL through `jq` rather than building large SQL:

```bash
sharedwatch events list --since 24h --format jsonl \
  | jq -r '
      select(.payload_json) |
      .payload_json |= fromjson |
      [.observed_at, .type, .rel_path, .payload_json.actor // "-", .payload_json.task // "-"] | @tsv
    '
```

For one-shot aggregations, SQL is cheaper. For deep introspection of individual events, JSONL + `jq` is friendlier.
