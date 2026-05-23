# SCHEMA CONTRACTS

## Event
Fields:
- `id`
- `type`
- `path`
- `rel_path`
- `old_path`
- `source`
- `status`
- `retry_count`
- `created_at`
- `observed_at`
- `processed_at`
- `file_size`
- `mtime`
- `content_hash`
- `coalesced_into`
- `payload_json`

## Digest
Fields:
- `id`
- `created_at`
- `window_start`
- `window_end`
- `mode`
- `event_count`
- `summary_text`
- `status`

## Runtime state
Keys:
- `mode`
- `active_until`
- `last_consumer_run`
- `last_reconcile_run`
- `last_event_at`
- `last_snapshot_hash`
