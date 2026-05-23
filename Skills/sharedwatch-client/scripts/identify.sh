#!/usr/bin/env bash
# identify.sh — emit sharedwatch events with structured attribution.
#
# Sets the conventional payload_json v1 keys (actor, session, task,
# intent, addressee, ref_event_id, tags) for a paired synthetic event,
# so peers can find your work.
#
# Usage:
#   ./identify.sh <relpath> [type] \
#                 --actor <id> [--session <id>] [--task <label>] \
#                 [--intent <free-text>] [--addressee <id>] \
#                 [--ref <event-id>] [--tag <t>]...
#
# Examples:
#   ./identify.sh auth/login.go file.modified --actor claude-me-1 \
#     --session sess-2026-05-22-abc --task refactor-auth \
#     --intent 'split JWT validation'
#
#   ./identify.sh code/widget.go file.created --actor claude-code \
#     --ref evt_a1b2c3 --addressee claude-spec

set -euo pipefail

SW="${SW:-sharedwatch}"

usage() {
  sed -n '4,21p' "$0"
  exit "$1"
}

relpath="${1:-}"
[ -z "$relpath" ] && usage 2

type="${2:-file.modified}"
case "$type" in
  file.created|file.modified|file.deleted|file.renamed) shift 2 ;;
  *) shift 1 ;;
esac

actor=""; session=""; task=""; intent=""; addressee=""; ref=""
tags_json="[]"

while [ $# -gt 0 ]; do
  case "$1" in
    --actor)     actor="$2"; shift 2 ;;
    --session)   session="$2"; shift 2 ;;
    --task)      task="$2"; shift 2 ;;
    --intent)    intent="$2"; shift 2 ;;
    --addressee) addressee="$2"; shift 2 ;;
    --ref)       ref="$2"; shift 2 ;;
    --tag)
      tags_json=$(jq --arg t "$2" '. + [$t]' <<< "$tags_json")
      shift 2 ;;
    -h|--help)   usage 0 ;;
    *) echo "unknown flag: $1" >&2; usage 2 ;;
  esac
done

[ -z "$actor" ] && { echo "--actor is required" >&2; exit 2; }

payload=$(jq -n \
  --arg actor "$actor" \
  --arg session "$session" \
  --arg task "$task" \
  --arg intent "$intent" \
  --arg addressee "$addressee" \
  --arg ref "$ref" \
  --argjson tags "$tags_json" \
  '{
    schema_version: 1,
    actor: $actor
  }
  + (if $session   != "" then {session:   $session}   else {} end)
  + (if $task      != "" then {task:      $task}      else {} end)
  + (if $intent    != "" then {intent:    $intent}    else {} end)
  + (if $addressee != "" then {addressee: $addressee} else {} end)
  + (if $ref       != "" then {ref_event_id: $ref}    else {} end)
  + (if ($tags|length) > 0 then {tags: $tags}         else {} end)
  ')

"$SW" test emit "$relpath" --type "$type" --payload "$payload"
