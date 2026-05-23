#!/usr/bin/env bash
# orient.sh — run the initial-orientation pattern in one call.
#
# Output is a single JSON object on stdout, designed to be loaded
# straight into an agent's context. Stays well under 1k tokens for
# a typical workspace.
#
# Usage:
#   ./orient.sh                       # uses `sharedwatch` from PATH
#   SW=./sharedwatch/.bin/sharedwatch ./orient.sh

set -euo pipefail

SW="${SW:-sharedwatch}"

if ! command -v "$SW" >/dev/null 2>&1 && [ ! -x "$SW" ]; then
  echo '{"error":"sharedwatch not found","hint":"set SW=<path> or add to PATH"}' >&2
  exit 1
fi

status_json=$("$SW" status --json 2>/dev/null || echo '{}')

# Aggregations — fall back to empty objects if SQL not available for any reason.
type_counts=$("$SW" sql --format json "
  SELECT type, COUNT(*) AS n
  FROM events
  WHERE created_at > datetime('now', '-1 day')
  GROUP BY type
" 2>/dev/null || echo '{"rows":[]}')

actor_counts=$("$SW" sql --format json "
  SELECT json_extract(payload_json,'\$.actor') AS actor, COUNT(*) AS n
  FROM events
  WHERE created_at > datetime('now', '-1 hour')
    AND json_extract(payload_json,'\$.actor') IS NOT NULL
  GROUP BY actor
  ORDER BY n DESC
  LIMIT 5
" 2>/dev/null || echo '{"rows":[]}')

top_paths=$("$SW" sql --format json "
  SELECT rel_path, COUNT(*) AS n, MAX(observed_at) AS last_at
  FROM events
  WHERE created_at > datetime('now', '-1 hour')
  GROUP BY rel_path
  ORDER BY n DESC
  LIMIT 5
" 2>/dev/null || echo '{"rows":[]}')

# Stitch into one envelope.
jq -n \
  --argjson status "$status_json" \
  --argjson types "$type_counts" \
  --argjson actors "$actor_counts" \
  --argjson paths "$top_paths" \
  '{
    status: $status,
    last_24h_by_type: ($types.rows // []),
    last_1h_top_actors: ($actors.rows // []),
    last_1h_top_paths: ($paths.rows // []),
    note: "PATTERN: initial-orientation snapshot. Drill in with `events list --path-glob` or `--payload-key actor --payload-value <x>`."
  }'
