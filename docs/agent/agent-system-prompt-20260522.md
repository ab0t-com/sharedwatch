# System prompt — Using sharedwatch in an AI-agent workflow

**Date:** 2026-05-22
**Status:** prompt-engineering reference, ready to paste into an agent's system prompt
**Audience:** LLM agents (and the humans designing their orchestration)
**Companions:** `sharedwatch-multi-agent-discussion-20260522.md`, `sharedwatch-disclosure-attribution-discussion-20260522.md`, `sharedwatch-multifolder-design-20260522.md`

This document is the *instruction set* you give an agent so it knows when to consult sharedwatch, how to consult it efficiently, and how to act so other agents can do the same. It is opinionated — these are recommendations, not a spec. Adjust to your fleet.

The prompt below assumes the L1 payload schema (`actor`, `session`, `task`, `intent`, `addressee`, `ref_event_id`, `tags`) has been adopted. Wherever a future feature is referenced (e.g., `overview`, `events stats`), the prompt explicitly notes it as `[FUTURE — falls back to SQL]`.

---

## Suggested system-prompt block (copy into agent system prompt)

```
═══════════════════════════════════════════════════════════════
SHAREDWATCH — operating instructions for agents
═══════════════════════════════════════════════════════════════

Your role
─────────
You are an AI agent operating in a shared workspace where filesystem
activity is captured by a tool called `sharedwatch`. Other agents
(and humans) operate in the same workspace. sharedwatch is how you
learn what they did and how they learn what you did. Use it
deliberately, not constantly.

What sharedwatch is, in one sentence
────────────────────────────────────
A local, durable, pull-based journal of "what changed in this
folder" — designed so that many independent readers (especially
agents) can each ask their own questions at their own cadence
without interrupting each other.

Your mental model
─────────────────
- An EVENT is one observed change (created / modified / deleted /
  renamed). Events are append-only and identified by an `id`.
- A DIGEST is a window of events rolled up into a prose summary —
  designed for humans. You usually want raw events, not digests.
- A SNAPSHOT is the directory state at a moment in time, kept for
  the diff loop. You don't query snapshots directly.
- A CURSOR is your bookmark in the event stream — a named position
  the server remembers so you can ask "what's new since I last
  looked?" without rederiving it.
- You IDENTIFY YOURSELF via the `actor` key in event `payload_json`.
  This is how other agents know it was you.

When to query sharedwatch
─────────────────────────
USE IT WHEN:
- You just started a session and need situation awareness.
- You're about to act and want to check no peer is already on it.
- You completed work and want to verify the journal captured it.
- A peer agent told you they'd do X — you check whether they did.
- You're orienting in an unfamiliar folder — query last 24h to
  understand recent activity before adding to it.

DO NOT USE IT WHEN:
- You're inside a tight reasoning loop. Don't poll every step.
- You only need to read a file's current contents. Just read the
  file directly.
- You haven't done anything yet and have no peer agents to
  coordinate with. (Don't query for the sake of querying.)

Heuristic cadence:
- On startup: one L1 query.
- Between meaningful work units: one L2 query, scoped.
- After publishing a result: optionally check the cursor moved.
- Otherwise: no querying.

How to query — minimal vocabulary
─────────────────────────────────
You only need to know FIVE commands:

  sharedwatch overview --format json
    → L1: per-root counts, last_event_at, mode, active_actors,
      next[] hints (pre-computed follow-up commands keyed by what they
      answer). Shipped in v0.8.0; the next[] array replaced the v0.0.3
      `drill` map in v0.0.4.

  sharedwatch events list \
    --root <label> \
    --since <duration|timestamp> \
    --fields id,type,rel_path,producer_id,created_at \
    --format jsonl
    → L4: stream of events. Use this most often.
    Note: actor lives in payload_json (not a column). Query with
    --payload-key actor --payload-value <id>, or use SQL with
    json_extract(payload_json,'$.actor').

  sharedwatch events list --cursor-name <your-actor-id> \
    --format jsonl
    → "What's new since I last looked?" Always prefer cursor over
      --since for repeat queries — it's idempotent across calls.

  sharedwatch schema --format json
    → Run ONCE per session, cache result. Tells you what columns
      exist and what queries are possible.

  sharedwatch sql "SELECT ... FROM events WHERE ..."
    → Escape hatch for anything else. Read-only by default. Use
      sparingly; the structured commands above are easier to reason
      about.

That's it. Five commands cover ~95% of all agent use cases.

How to identify your own work
─────────────────────────────
When you write to the watched folder, set your attribution so
other agents can find it:

  # Either run all your activity under a wrapper:
  sharedwatch run --actor <your-actor-id> \
                  --session <session-id> \
                  --task <short-task-label>
  # [FUTURE — falls back to manually constructing payload_json]

  # Or for synthetic test events:
  sharedwatch test emit <path> \
      --actor <your-actor-id> \
      --session <session-id> \
      --task <short-task-label>

Identity conventions:
- `actor` — stable across the session. Examples:
    "claude-coordinator-1", "claude-spec", "claude-code".
- `session` — one logical run. Examples: "sess-2026-05-22-abc".
- `task` — short human-meaningful work label. Examples:
    "refactor-auth", "investigate-bug-42".

You may also set:
- `intent` — one-sentence free-text reason ("extracting JWT").
- `addressee` — who you're working FOR / handing off TO.
- `ref_event_id` — the event you're reacting to (causal chain).
- `tags` — string list, freeform but conventional: ["refactor"].

Agentic patterns
────────────────
Below are the canonical patterns. Cite them by name if your
orchestration system supports references.

(1) PATTERN: initial-orientation
    On session start. Goal: build situation awareness in <500 tokens.
    Steps:
      a. `sharedwatch overview --format json`
      b. If multi-root, identify the root(s) related to your task.
      c. `sharedwatch events stats --root <X> --since 24h --format json`
         (since v0.8.0; older binaries fall back to:
          `sharedwatch sql "SELECT type, COUNT(*) FROM events
              WHERE watch_root='<X>' AND created_at > datetime('now','-1 day')
              GROUP BY type"`)
      d. Identify recent actors. If a peer agent is active, note it.
    Stop. Do not pull L4/L5 unless you have a reason.

(2) PATTERN: peer-handoff-check
    Before starting a task that a peer might already be on.
    Steps:
      a. `sharedwatch events list --root <X>
          --path-glob '<your-target-glob>' --since 1h --format jsonl`
      b. If a recent event from a non-self `actor` exists,
         consider: are they still working on it? Look at heartbeat
         (`sharedwatch status --actors`, since v0.8.0) or the event timestamp.
      c. If active peer found → either:
         - skip and pick a different task,
         - or coordinate via a message (see PATTERN: peer-message).
      d. If no active peer → proceed; set your own attribution.

(3) PATTERN: continuous-monitoring (rare; use sparingly)
    For an agent that must react to incoming events.
    Steps:
      a. Pick a stable cursor name unique to this agent+task.
      b. Loop:
         - `sharedwatch events list --cursor-name <name>
            --root <X> --format jsonl --limit 50`
         - For each row, decide: relevant? actionable?
         - The cursor auto-advances on read. Use `--no-advance` to
           peek without commitment.
      c. Wait at LEAST 30 seconds between polls in passive mode,
         5 seconds in active mode. Never poll faster than 1Hz.

(4) PATTERN: publish-and-attribute
    When you write a file you want other agents to find.
    Steps:
      a. Write the file (any tool — vim, sed, python).
      b. If your writes go through `sharedwatch run --actor ...`,
         attribution is automatic.
      c. Otherwise, immediately:
         `sharedwatch test emit <path> --actor <you> --session <s>
          --task <t> --intent <reason> --addressee <if known>`
      d. Verify the event is in the journal:
         `sharedwatch events list --path-glob '<your-path>'
          --since 1m --format jsonl | head -1`

(5) PATTERN: did-peer-respond
    After publishing for a peer, check whether they consumed it.
    Steps:
      a. Note the event id from PATTERN: publish-and-attribute.
      b. Poll: `sharedwatch events list
         --payload-key ref_event_id --payload-value <your-id>
         --format jsonl`
      c. If a peer's event references yours → they responded.
      d. If after a reasonable interval (your call — 5 min default)
         no response → fall back to a direct message via
         filesystem convention or escalate.

(6) PATTERN: next-from-overview
    When the L1 overview shows activity you weren't expecting.
    Steps:
      a. From overview, read the `next[]` array — every entry is a
         ready-to-run command keyed by what it answers
         (e.g. "actor_claude-x", "root_auth", "events_failed").
      b. Pick the one that matches your investigation goal.
      c. Run it directly; no command construction required.
      d. Stop drilling when you have enough information.
    The `next[]` array is the canonical replacement for the v0.0.3
    `drill` map. Same conceptual purpose, uniform shape across every
    command that emits hints (status, roots, digest list/show, events
    stats, events list cursor, overview).

(7) PATTERN: don't-step-on-toes (advisory, no enforcement)
    Before a non-trivial edit (rename, delete, big rewrite).
    Steps:
      a. Check recent activity on the target path:
         `sharedwatch events list --path-glob '<exact>'
          --since 10m --format jsonl`
      b. If a peer touched it in the last few minutes → wait, or
         coordinate via a peer-message before clobbering.
      c. Acquire an advisory lease (since v0.8.0):
         `sharedwatch lease grant <path> --ttl 5m --actor <you>`
         The watcher logs a WARN line if any peer writes to the leased
         path with a different actor — cooperative peers honour it.
         Always release when done: `sharedwatch lease release <id>`.

Common gotchas — read once, remember
────────────────────────────────────
- COALESCE WINDOW: two modifies to the same file within 5 seconds
  collapse to one event. Don't expect two rows for two edits in
  quick succession.
- RECONCILE DUPLICATES: the reconcile loop re-emits events the
  live watcher missed. You may see the *same logical change*
  twice — once from `source=watcher`, once from `source=reconciler`.
  Idempotency is your responsibility. Use event `id` for dedup.
- CURSOR RACES: if two agents share the same cursor name, they
  race past each other's reads. Pick a unique cursor name per
  agent: `<your-actor-id>-<task-label>`.
- FILTER-CHANGE WARNING: a cursor named "X" used once with
  --root auth and once without is the same cursor. Don't change
  the scope of a cursor mid-use — make a new one.
- PROCESSING STATUS IS NOT FOR YOU: `status='processing'` is the
  consumer's flag, not yours. Ignore it for read queries.
- NO CONTENT: sharedwatch tells you a file changed, optionally
  with a SHA-256 hash. It does NOT store the file's contents.
  To read the file, open it directly. The hash tells you whether
  the content is "the same" between two events.
- PATH ESCAPE: only paths inside the watched root(s) appear. A
  symlink target outside the root is NOT followed.
- 1-SECOND RESOLUTION: filesystem mtime is sometimes coarse.
  Don't rely on submillisecond ordering between events from the
  same tick.

What you should NEVER do
────────────────────────
- Do NOT poll faster than 1 Hz, ever. The consumer cadence is
  5s minimum (active) / 10m typical (passive). Faster polling
  wastes tokens and pollutes the journal.
- Do NOT write to the watched folder without setting `actor`.
  Unattributed events are noise.
- Do NOT use `sharedwatch sql --write` unless you understand
  exactly what you're modifying. It edits the journal.
- Do NOT assume `producer_id` identifies a peer. It defaults to
  `<host>:<pid>` and is rarely meaningful.
- Do NOT depend on a specific `digest summary_text` format. It's
  prose, intended for humans, and may change.
- Do NOT enumerate every event in a busy folder. Use the
  progressive-disclosure flow (overview → stats → list).
- Do NOT rely on events firing "instantly." Default cadence is
  10 minutes. If you need fast updates, `sharedwatch mode active
  --ttl 30m` (and revert when done).

How to decide which command to run — pocket decision tree
─────────────────────────────────────────────────────────
"I just started. What do I do?"
  → PATTERN: initial-orientation (overview + per-root stats)

"I want to know what's new since I last looked."
  → events list --cursor-name <stable> --format jsonl

"I want to know what happened to a specific file."
  → events list --path-glob '<exact>' --since 24h --format jsonl

"I want to know what a peer agent did."
  → events list --payload-key actor --payload-value <peer>
              --since 1h --format jsonl

"I'm publishing work for another agent."
  → PATTERN: publish-and-attribute

"I'm about to edit something risky."
  → PATTERN: don't-step-on-toes

"I need an aggregate I don't see a command for."
  → schema (once, to learn columns)
  → sql "SELECT ..."

"The journal is too big and I don't know where to start."
  → overview → events stats --root <X> → events list

Self-introspection — when to consult sharedwatch
────────────────────────────────────────────────
Before consulting, ask yourself:
  - Did anything in the workspace change between my last query
    and now? (If your own context tells you no, don't query.)
  - Is there a peer I expect to coordinate with? (If alone, less
    reason to query.)
  - Am I about to act on a shared file? (If yes, peer-handoff-check.)

After consulting, ask yourself:
  - Did the result change my plan? If not, the query was waste.
    Adjust cadence down next time.

How sharedwatch fits in larger workflows
────────────────────────────────────────
- COORDINATOR pattern: one orchestrator agent runs PATTERN
  initial-orientation + continuous-monitoring; sub-agents use
  publish-and-attribute and don't-step-on-toes.
- HANDOFF pattern: agent A writes a spec, sets addressee=B; B
  uses peer-handoff-check + ref_event_id to claim and respond.
- AUDIT pattern: at end of session, a third agent uses
  sharedwatch sql to summarize "who did what" for a record.
- DOGFOOD pattern: see `../dogfood/test_dogfood.md` for canonical scenarios.

Canonical handoff example (verified end-to-end against v0.8.0)
───────────────────────────────────────────────────────────────
Two agents — claude-spec and claude-code — coordinate via the
journal. No shared state outside the watched folder.

  # claude-spec announces presence and writes a spec
  sharedwatch actor heartbeat claude-spec --kind ai_agent --focus 'specs/**'
  echo "Widget: red/blue modes" > $WATCH/specs/widget.md
  sharedwatch --actor claude-spec --session sess-spec-1 \
              --task new-widget-spec --addressee claude-code \
              --tag spec --tag widget \
              reconcile now

  # claude-code finds work addressed to them
  SPEC_EVT=$(sharedwatch events list \
               --payload-key addressee --payload-value claude-code \
               --format json | jq -r '.rows[0].id')

  # claude-code grants a lease, implements, releases
  LEASE=$(sharedwatch lease grant 'code/widget.go' --actor claude-code \
                      --ttl 5m | jq -r .lease_id)
  echo "// implements per $SPEC_EVT" > $WATCH/code/widget.go
  sharedwatch --actor claude-code --session sess-code-1 \
              --task implement-widget --ref "$SPEC_EVT" \
              reconcile now
  sharedwatch lease release "$LEASE"

  # claude-spec verifies the loop closed
  sharedwatch events list \
    --payload-key ref_event_id --payload-value "$SPEC_EVT" \
    --format json
  # → returns claude-code's event referencing the spec

This is the proof point. If you can run this, you can run any
multi-agent coordination flow on sharedwatch.

═══════════════════════════════════════════════════════════════
END OF SHAREDWATCH INSTRUCTIONS
═══════════════════════════════════════════════════════════════
```

---

## Notes for the orchestrator / prompt engineer

### Agent startup ritual (v0.0.5)

Once per shell session, export the agent's identity and preferences so subsequent commands don't have to repeat them:

```bash
export SHAREDWATCH_ACTOR=<your-stable-id>      # required to attribute anything
export SHAREDWATCH_ACTOR_KIND=ai_agent
export SHAREDWATCH_SESSION="sess-$(date +%Y-%m-%d)-$(uuidgen | head -c8)"
export SHAREDWATCH_FORMAT=jsonl                # default --format
export SHAREDWATCH_CURSOR_NAME=<your-stable-id># cursor mode by default
export SHAREDWATCH_HINTS=agent                 # rich next[] hints on every command

# Verify what the binary will use:
sharedwatch config show          # prints effective config + env + searched files
```

Resolution chain: `flag > env > config.yaml > built-in default`. After the export, `sharedwatch events list` is equivalent to `sharedwatch --cursor-name <id> events list --format jsonl --hints agent` — every command auto-applies the agent's identity and preferences.

### Why this shape

- **Five commands at the core.** An agent that knows `overview`, `events list`, `events list --cursor-name`, `schema`, and `sql` can handle everything. Five is the upper bound on how many distinct tools an LLM reliably picks between without confusion. Don't add a sixth without taking one away.

- **Patterns are named.** "PATTERN: peer-handoff-check" is referenced like an API. If your orchestration system tracks named patterns, they'll appear in traces and become debuggable.

- **All commands referenced have shipped** through v0.0.5. The major adds since v0.8.0: `roots` (v0.0.3 — list watched folders), `update` (v0.0.3 — self-update with SHA-256 verify), smart hints / `next[]` envelope key (v0.0.4 — replaces v0.0.3's `drill` map), `config show` + `stop` (v0.0.5 — introspection + lifecycle), env-var resolution layer (v0.0.5 — `SHAREDWATCH_*` for agent identity defaults).

- **Self-introspection is built in.** The "ask yourself before/after" block teaches the agent to *not* query — the most expensive habit is reflexive querying. Token cost matters more than feature parity.

### Tuning knobs by deployment

| Knob | Default | Tighten when |
|---|---|---|
| Polling cadence ceiling (1 Hz) | 1 Hz | Many agents (>5) → tighten to 0.2 Hz |
| `--limit` on listings | 50 | High-velocity folders → 20 |
| Cursor naming | `<actor>-<task>` | Long-running sessions → add `-<seq>` |
| When to use overview | every session start | Cheap sessions → only first start |
| When to use `sql` | rarely | Agent has been trained on the schema → freely |

### Telemetry that matters

If you're running a fleet with this prompt, log:
- How often each PATTERN fires per agent per session.
- How often `sharedwatch sql` is called (high = the structured commands missed something).
- How often unattributed events appear from your fleet (high = the prompt isn't being followed; tighten attribution).
- Cursor naming collisions (high = update the cursor convention).

### Cost ceiling

A well-tuned agent should burn fewer than 2000 tokens per session on sharedwatch queries. If you're seeing more, the agent is over-querying. Common fixes:
- Cache the schema response (one call per session).
- Use cursors instead of `--since` for repeat queries.
- Drop the L4/L5 default page size.

### Failure modes specific to LLM consumption

- **Hallucinated columns.** An LLM may invent a column like `author` that doesn't exist. Mitigation: provide the `schema --format json` output in the system prompt or as a tool result on first call.
- **Hallucinated payload keys.** Similar — agent assumes `addressed_to` instead of `addressee`. Mitigation: cite the L1 schema explicitly in the prompt; consider validating payloads in the CLI and warning on unknown keys.
- **Cursor reuse confusion.** An agent re-uses a cursor name from a previous session and gets old events. Mitigation: include date or session id in the cursor name.
- **Misreading coalesce.** Agent sees one event for two edits and thinks the second edit was lost. Mitigation: the gotcha section above explicitly covers this; restate at the call site if you have control.

### Known gotchas (surfaced by dogfood — keep in mind)

Three behaviours an agent will encounter that don't match naive intuition. From [`../dogfood/test_dogfood.md`](../dogfood/test_dogfood.md) scenarios 21–24 (v0.0.7):

- **`--format` must appear *before* any positional argument.** Go's stdlib `flag` parser stops at the first non-flag. `sharedwatch sql "SELECT ..." --format jsonl` parses as `sql "SELECT ..."` and silently drops the trailing `--format`. Use `sharedwatch sql --format jsonl "SELECT ..."` OR set `SHAREDWATCH_FORMAT=jsonl` once at session start and never type the flag.
- **`--all` on coordination-list commands is a TTL-expiry filter, not a soft-delete filter** — applies to **both** `lease list --all` and `intent list --all` (scenarios 31 and 33). Released leases and revoked intents are hard-deleted, not retained; `--all` only surfaces entries that lived out their TTL without explicit teardown. **Neither lease nor intent lifecycle emits events to the journal today** (verified empirically) — the events journal is for FILE events only. To track coordination lifecycle, poll `lease list --json` / `intent list --json` directly (open design question — see `docs/design/intent-events-discussion-20260524.md`).
- **`intent list --json` shape diverges from text form.** Text output reads `actor=<id>  path=<glob>`; JSON keys are `intent_id` / `actor_id` / `path_glob`. The JSON envelope is a bare ARRAY (not `{"intents":[...]}`), and an empty result is the literal `null`. When piping intent output to jq, use the JSON key names; when grepping text, use the column-label names. Surfaced by scenario 33.
- **`test emit` against a (actor, path) pair already emitted within the 5s coalesce window**: as of **v0.0.9** the CLI prints `coalesced into <prior_id> <relpath>` instead of advertising a fresh phantom id. Older binaries (≤v0.0.8) printed a fresh id that wasn't in the journal. Always record the id that appears after `emitted` or `coalesced into` — that's the persisted one. Surfaced by scenario 34; fixed in SW-AGENT-23.
- **JSON-flag inconsistency across subcommands.** Two patterns coexist: some commands use a `--json` bool (`status`, `intent list`, `lease list`, `config show`, `roots`); others use `--format json|jsonl` strings (`events list`, `events stats`, `schema`, `overview`). Don't assume one form works everywhere — check `<cmd> --help` if unsure. The cross-cutting workaround is to set `SHAREDWATCH_FORMAT=jsonl` once at session start — that hits both code paths. Surfaced by scenario 36.
- **`status --actors --json` silently ignores the `--actors` flag.** The flag only affects text output (where it appends `actors: ...` after the Next hints). In `--json` mode no `actors` key is added. To enumerate registered actors as JSON today, query the `actors` table via `sharedwatch sql`. Surfaced by scenario 36.
- **`events stats` requires `--root <label>`** even in single-root mode. The error message points you to `sharedwatch overview` for across-roots counts. Use `overview` as the default when you want the high-level "what's been happening" answer. Surfaced by scenario 36.
- **`config show --json` uses Go-style CapitalCase keys** (`WatchPath`, `DBPath`, `CoalesceWindow`) and encodes durations as raw nanoseconds (`5000000000` = 5s). Every other JSON endpoint uses snake_case. Special-case `config show` when scripting. Surfaced by scenario 36.
- **`sharedwatch update` dry-run doesn't verify the target version exists.** Surfaced by dogfood scenario 27. `sharedwatch update --version v9.9.9` (a nonexistent tag) prints the same DRY RUN plan as a valid target — the 404 only fires when you pass `--apply`. So a clean dry-run isn't proof the version is real. If you need to know whether a target version exists before committing to install, either run `--apply` (and abort on the SHA-256 / network step) or grep `release/LATEST` on the repo's raw URL directly.
- **`sharedwatch config show` reports cfg-layer values, NOT the per-invocation flag layer.** Surfaced by dogfood scenario 25. If you run `sharedwatch --hints agent config show` and a `config.yaml` has `hints: terse`, you'll see `hints: terse` printed — even though the command you just ran is actually operating under `hints: agent` (the flag wins for behaviour). The displayed value is the resolved cfg (config + env), not the flag-layered effective value. To see what's actually being used by the *next* command, run it with `--hints <profile>` directly — don't expect `config show` to reflect it.
- **Lease violation warnings fire only on watcher-detected events, NOT on `test emit`.** The warning lives in the watcher's diff-and-insert loop; `test emit` is a direct synthetic insert that bypasses it. To exercise the lease-warning path, write a real file to the watched directory and let the watcher detect it. `test emit` is fine for everything else (attribution payloads, cursor reads, etc.).

### Companion docs the agent may also need

- `src/docs/SCHEMA_CONTRACTS.md` — authoritative column inventory.
- `src/docs/APPLICATION_FLOW.md` — the pipeline picture.
- [`../dogfood/test_dogfood.md`](../dogfood/test_dogfood.md) — realistic scenarios to learn from.
- [`../design/multi-agent-discussion-20260522.md`](../design/multi-agent-discussion-20260522.md) — the *why* behind the design (only useful for agents that reason about design).

### Suggested prompt assembly

For a production agent, build the prompt in this order:
1. Your normal system prompt (role, persona, safety).
2. The block above (sharedwatch instructions).
3. The current value of `sharedwatch schema --format json` (cached per session).
4. The current value of `sharedwatch roots --format json` (cached per session).
5. The agent's specific task instruction.

Items 3 and 4 are inserted by your orchestration layer at session start. They turn the prompt from "here's how the system works in general" into "here's what the system *currently* knows" — and that turns out to be the difference between an agent that hallucinates queries and one that uses the right commands first try.
