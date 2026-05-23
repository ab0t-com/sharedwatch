# sharedwatch — agentic patterns (current state)

Extended descriptions of the five canonical patterns named in SKILL.md, plus the conventional `payload_json` v1 schema.

## Contents
1. The `payload_json` v1 schema
2. PATTERN: initial-orientation
3. PATTERN: peer-handoff-check
4. PATTERN: continuous-monitoring
5. PATTERN: publish-and-attribute
6. PATTERN: did-peer-respond
7. Pattern composition examples

## 1. The `payload_json` v1 schema

When you emit an event (via `test emit --payload`) or want peers to find your work, populate `payload_json` with this shape:

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

| Key | Required? | Convention |
|---|---|---|
| `schema_version` | required (always `1` today) | Renderers warn on mismatch |
| `actor` | required | Stable across the agent's session. Examples: `claude-coordinator-1`, `claude-spec`, `human-mike` |
| `actor_kind` | optional | `human` / `ai_agent` / `automation` |
| `session` | optional | One logical run. Convention: `sess-YYYY-MM-DD-<short-id>` |
| `task` | optional | Short human-meaningful work label |
| `intent` | optional | One-sentence free-text reason |
| `addressee` | optional | Who this change is FOR (peer agent or human) |
| `ref_event_id` | optional | Causal predecessor — the event you are reacting to |
| `tags` | optional | Free-form string list; conventional values stabilize over time |

Unknown keys are allowed but ignored. Validation is soft (warn, never reject).

Build the payload either with the dedicated CLI flags (preferred, since SW-AGENT-7 shipped) or by hand-rolling JSON via `--payload`:

```bash
# Preferred — first-class flags on root or `test emit`. Mutually exclusive
# with `--payload` on the same command.
sharedwatch test emit foo.md \
  --actor claude-coordinator-1 \
  --actor-kind ai_agent \
  --session sess-2026-05-23-abc \
  --task refactor-auth \
  --intent "split JWT validation" \
  --addressee human-mike \
  --ref evt_a1b2 \
  --tag refactor --tag auth

# Legacy — hand-built JSON. Still supported; useful when you need a payload
# key that isn't covered by flags.
sharedwatch test emit foo.md --payload '{
  "schema_version": 1,
  "actor": "claude-coordinator-1",
  "session": "sess-2026-05-23-abc",
  "tags": ["refactor", "auth"]
}'
```

Query attribution with `--payload-key`/`--payload-value`:

```bash
sharedwatch events list \
  --payload-key actor --payload-value claude-coordinator-1 \
  --since 1h --format jsonl
```

Or in SQL:

```bash
sharedwatch sql "SELECT json_extract(payload_json,'\$.actor') AS actor, COUNT(*)
                 FROM events
                 WHERE created_at > datetime('now','-1 day')
                 GROUP BY actor"
```

## 2. PATTERN: initial-orientation

**When:** session start. Goal: build situation awareness in under 500 tokens.

**Steps:**
1. `sharedwatch status --json` — current mode, pending, last_run.
2. `sharedwatch sql "SELECT type, COUNT(*) FROM events WHERE created_at > datetime('now','-1 day') GROUP BY type"` — what kind of activity?
3. `sharedwatch sql "SELECT json_extract(payload_json,'\$.actor') AS actor, COUNT(*) FROM events WHERE created_at > datetime('now','-1 hour') GROUP BY actor ORDER BY 2 DESC LIMIT 5"` — who is active?
4. Decide: do I need to drill in? If yes → events list with filters; if no → stop.

**Worked example:**
```bash
sharedwatch status --json | jq '{mode, pending, processed, last_run: .last_consume_at}'
# → {"mode":"passive","pending":3,"processed":412,"last_run":"2026-05-22T14:30:11Z"}

# If pending > 0 or recent activity is interesting, drill:
sharedwatch events list --since 1h \
  --fields type,rel_path,producer_id,created_at --format jsonl
```

**Stop condition:** you have a one-paragraph mental model of the workspace. Do not pull L4/L5 unless you have a reason.

## 3. PATTERN: peer-handoff-check

**When:** before starting a non-trivial edit on a path that a peer might be working on.

**Steps:**
1. Query recent activity on the target:
   ```bash
   sharedwatch events list --path-glob '<target-glob>' --since 1h --format jsonl
   ```
2. If a recent event from a non-self `actor` exists, decide:
   - Is the actor still active? Heuristic: are there events from them in the last 5 minutes?
   - If active → either skip and pick another task, OR coordinate (write a peer message file with `addressee=<them>`).
   - If quiet → proceed; set your own attribution loudly.
3. After your edit, run PATTERN: publish-and-attribute so the next peer doing the same check sees you.

**Worked example:**
```bash
# Before editing auth/login.go
sharedwatch events list --path-glob 'auth/login.go' --since 1h --format jsonl \
  | jq -s 'map(select(.payload_json | fromjson? | .actor != "claude-me-1"))'
# If non-empty → a peer touched it recently. Decide.
```

## 4. PATTERN: continuous-monitoring

**When:** rare — you are an agent that must *react* to incoming events.

**Steps:**
1. Pick a stable cursor name: `<your-actor>-<task>`.
2. Loop with a delay ≥ 30 s (passive) or ≥ 5 s (active):
   ```bash
   while true; do
     sharedwatch events list \
       --cursor-name "claude-me-1-monitor" \
       --limit 50 --format jsonl \
       | while read -r ev; do
           # process event
           echo "$ev" | jq -r .rel_path
         done
     sleep 30
   done
   ```
3. To peek without advancing the cursor, add `--no-advance`.

**Hard ceiling:** never poll faster than 1 Hz. If active-mode 5 s isn't enough, you don't want sharedwatch — you want push.

## 5. PATTERN: publish-and-attribute

**When:** you wrote a file and want peer agents to find it with full attribution.

**Steps (since SW-AGENT-7):**
1. If you control the `run` invocation, launch it with attribution on the root flagset — the watcher will stamp your attribution onto every auto-detected event during this lifetime:
   ```bash
   sharedwatch --actor claude-me-1 --session sess-abc --task refactor-auth run &
   ```
2. Write the file (any tool). The next watcher tick (~2 s) picks it up with your attribution attached.
3. If you do NOT control `run`, emit a paired synthetic event using flags:
   ```bash
   sharedwatch test emit auth/login.go \
     --actor claude-me-1 --session sess-abc --task refactor-auth \
     --intent "split JWT validation"
   ```
4. Verify the event is in the journal:
   ```bash
   sharedwatch events list --path-glob 'auth/login.go' --since 1m --limit 1 --format jsonl
   ```

**Why the paired synthetic event when you do NOT control `run`?** The watcher's auto-detected event will carry whatever attribution the `run` was launched with (none, if defaulted). The synthetic event carries yours. Filter by `--payload-key actor` to find your event. As of v0.8.0 (SW-AGENT-11), coalesce is actor-aware: same-path events from different actors stay distinct rather than merging. Same-path same-actor events still coalesce within the 5 s window.

## 6. PATTERN: did-peer-respond

**When:** you published a handoff event and want to know if a peer consumed it.

**Steps:**
1. Capture your handoff event's id:
   ```bash
   MY_EVT=$(sharedwatch events list --payload-key actor --payload-value claude-me-1 \
            --limit 1 --format json | jq -r '.rows[0].id')
   ```
2. Poll for events that reference yours:
   ```bash
   sharedwatch events list \
     --payload-key ref_event_id --payload-value "$MY_EVT" \
     --format jsonl
   ```
3. If non-empty → peer responded. Otherwise wait and retry, with backoff.

**Reasonable timeout:** 5 min for an active peer; longer for a queued/scheduled one. If no response after the timeout, fall back to a direct message file with `addressee=<peer>` and try again.

## 7. Pattern composition — a worked end-to-end example

Scenario: claude-spec writes a spec for a widget; claude-code is meant to implement it.

```bash
# claude-spec's session
SESSION="sess-widget-2026-05-22"
ACTOR="claude-spec"

# 1. PATTERN: initial-orientation (5 sec)
sharedwatch status --json

# 2. Write the spec file
echo "Widget: red/blue modes" > "$WATCH/specs/widget.md"

# 3. PATTERN: publish-and-attribute (uses SW-AGENT-7 flags)
sharedwatch test emit specs/widget.md \
  --actor "$ACTOR" \
  --session "$SESSION" \
  --task new-widget-spec \
  --addressee claude-code \
  --tag spec --tag widget

# 4. capture event id
SPEC_EVT=$(sharedwatch events list --path-glob 'specs/widget.md' --limit 1 --format json | jq -r '.rows[0].id')

# Now claude-code wakes up
ACTOR="claude-code"
SESSION="sess-widget-impl"

# 5. PATTERN: peer-handoff-check on specs/
sharedwatch events list --path-glob 'specs/**' --since 1h \
  --payload-key addressee --payload-value claude-code --format jsonl

# 6. Write implementation
echo "// implements per $SPEC_EVT" > "$WATCH/code/widget.go"

# 7. PATTERN: publish-and-attribute with ref_event_id
sharedwatch test emit code/widget.go \
  --actor "$ACTOR" \
  --session "$SESSION" \
  --task implement-widget \
  --ref "$SPEC_EVT"

# Back at claude-spec:
# 8. PATTERN: did-peer-respond
sharedwatch events list --payload-key ref_event_id --payload-value "$SPEC_EVT" --format jsonl
# → claude-code's implementation event
```

This is the intended flow. If you cannot make it work, the gap is a real product bug — file it as a ticket against `tickets/`.
