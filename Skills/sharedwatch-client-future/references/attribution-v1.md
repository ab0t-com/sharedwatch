# Attribution v1 — schema, flags, queries

The `payload_json` v1 schema and the first-class CLI flags that populate it.

## Contents
1. The v1 schema
2. Flag → key mapping
3. Promoted columns (post-migration)
4. Querying attribution
5. Required vs optional, soft validation
6. Naming conventions for actors/sessions/tasks
7. Failure modes & defenses

## 1. The v1 schema

```json
{
  "schema_version": 1,
  "actor": "claude-coordinator-1",
  "actor_kind": "ai_agent",
  "session": "sess-2026-05-22-abc",
  "task": "refactor-auth",
  "intent": "extracting JWT validation into a separate package",
  "addressee": "human-mike",
  "ref_event_id": "evt_a1b2c3...",
  "tags": ["refactor", "auth"]
}
```

| Key | Required? | Type | Meaning |
|---|---|---|---|
| `schema_version` | required | int | Always `1` today. Renderers warn on mismatch. |
| `actor` | required | string | Stable id of the writer. |
| `actor_kind` | optional | string | `human` / `ai_agent` / `automation` |
| `session` | optional | string | Logical run id. Group of related events. |
| `task` | optional | string | Short human-meaningful work label. |
| `intent` | optional | string | One-sentence reason. |
| `addressee` | optional | string | Who the change is FOR (peer or human). |
| `ref_event_id` | optional | string | Causal predecessor — event you are reacting to. |
| `tags` | optional | string[] | Free-form list; conventions emerge in practice. |

Unknown keys are allowed but ignored by tooling. Validation is soft.

## 2. Flag → key mapping

Every emit-capable command (`run`, `test emit`, future `consume` operations) accepts:

| Flag | Sets payload key | Notes |
|---|---|---|
| `--actor <id>` | `actor` | Required if any other attribution flag is used |
| `--actor-kind <k>` | `actor_kind` | Defaults to `ai_agent` if `--actor` looks like a model name; `human` otherwise; explicit always wins |
| `--session <id>` | `session` | |
| `--task <label>` | `task` | |
| `--intent <text>` | `intent` | Multi-word values must be quoted |
| `--addressee <id>` | `addressee` | |
| `--ref <event-id>` | `ref_event_id` | The event you're reacting to |
| `--tag <t>` | `tags[]` | Repeatable |

Example:
```bash
sharedwatch test emit code/widget.go \
  --actor claude-code \
  --session sess-widget-impl \
  --task implement-widget \
  --intent "implements spec per evt_a1b2" \
  --addressee claude-spec \
  --ref evt_a1b2c3 \
  --tag widget --tag impl
```

You can still pass `--payload '<raw-json>'` for keys not covered by flags — but the v1 schema is exhaustive, so this should be rare.

## 3. Promoted columns (post-migration)

Once the attribution migration lands, two payload keys become first-class columns for query speed:

| Promoted column | Source key | Index |
|---|---|---|
| `events.actor` | `payload_json.actor` | yes |
| `events.session` | `payload_json.session` | yes |

Both are also kept in `payload_json` for migration safety. Queries should use the promoted columns when available:

```sql
-- Fast (uses index)
SELECT * FROM events WHERE actor = 'claude-1' AND created_at > datetime('now','-1 hour');

-- Equivalent but slower (JSON post-filter)
SELECT * FROM events WHERE json_extract(payload_json,'$.actor') = 'claude-1' ...;
```

Check `sharedwatch schema --format json` to confirm whether `actor` is promoted in the deployed version.

## 4. Querying attribution

Common queries (all read-only):

```bash
# Active actors in the last 5 min
sharedwatch events list --since 5m --fields actor --format jsonl \
  | jq -r 'select(.actor != null) | .actor' | sort -u

# What did one actor touch this hour?
sharedwatch events list --since 1h --payload-key actor --payload-value claude-1 \
  --fields rel_path,type,task,created_at --format jsonl

# All events for one session
sharedwatch events list --payload-key session --payload-value sess-abc

# Events addressed to me, not yet seen
sharedwatch events list --cursor-name me \
  --payload-key addressee --payload-value human-mike

# Causal chain — what does this event respond to, and what responded to it?
ME=evt_abc123
sharedwatch events list --payload-key ref_event_id --payload-value "$ME"   # children
echo "$(sharedwatch events list --format json --limit 1 --payload-key ... | jq -r '.rows[0].payload_json.ref_event_id')"  # parent

# Unattributed rate (quality metric for cooperative attribution)
sharedwatch sql "SELECT
                   ROUND(100.0 * SUM(CASE WHEN actor IS NULL THEN 1 ELSE 0 END) / COUNT(*), 1)
                 FROM events
                 WHERE created_at > datetime('now','-1 day')"
```

## 5. Required vs optional, soft validation

- `actor` is the only required key. Events without it are accepted (legacy compatibility) but queries that filter on `actor` skip them.
- Unknown keys are accepted silently.
- `schema_version` mismatch (e.g., `2` in a deployment that only knows `1`) emits a warning at digest time but does not reject the event.

The philosophy: **never reject events.** A reject creates pressure to skip attribution. A warn creates pressure to fix it.

## 6. Naming conventions for actors/sessions/tasks

**`actor`** — stable across the agent's lifetime. Examples:
- `claude-coordinator-1` (numbered instance of a role)
- `claude-spec`, `claude-code`, `claude-review` (specialized roles)
- `human-mike`, `human-sarah` (humans)
- `automation-deploy-bot` (non-Claude automation)

Don't use:
- model names (`claude-3-opus-20240229`) — they change with model updates
- session-scoped names (`session-xyz-actor`) — confusing
- generic names (`agent-1`) — collisions guaranteed

**`session`** — one logical run. Convention: `sess-YYYY-MM-DD-<short-id>`.

**`task`** — short, kebab-case, human-meaningful. Examples: `refactor-auth`, `investigate-bug-42`, `daily-digest-2026-05-22`.

## 7. Failure modes & defenses

| Failure | Defense |
|---|---|
| Agent forgets `--actor` | Soft-warn at digest time (`X events unattributed in window`). |
| Two agents reuse same actor id | `sharedwatch actor heartbeat` rejects with a clear collision error if a different host has held the id recently. |
| Stale actor in registry | TTL on heartbeat; `status --actors` shows `stale=true`. |
| Schema drift in payload | `schema_version` mismatch warns. |
| Agent invents an `addressed_to` key (typo) | Unknown key silently ignored. Renderer doesn't pick it up. Mitigation: examples in this skill use exact key names; teach by template. |
| Agent lies about its actor id | Out of scope. Use OS-level audit (`fanotify`) if you need authoritative attribution. |

The threat model is **forgotten** attribution, not malicious. Cooperative defaults catch 95%.
