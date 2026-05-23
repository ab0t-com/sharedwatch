---
name: sharedwatch-contributor
description: Understand the sharedwatch repository as a contributor (human or AI agent) — its product intent (a calm, durable, pull-based local folder-activity journal designed for AI-agent and human collaboration in a shared workspace), its team model (today Mike/Sarah/John; scaling to larger teams), its package layout (cmd/sharedwatch, internal/{watcher,db,consumer,reconcile,catalog,events,digest,mode,config,output,app}), its load-bearing invariants (queue-first, pull-over-push, durable-by-default, one-folder-one-process, polling-is-fine), its file-naming conventions (idea-/plan-/decision-/tasks-/ticket-/tasklist-), its engineering rules (Go 1.22+, gofmt, no panics outside main, errors wrapped, one external dep), and its open work (tickets in `tickets/`, status reports at the top level). Use when (1) opening the sharedwatch repo for the first time, (2) about to modify code in `sharedwatch/internal/*`, `cmd/`, or `docs/`, (3) reading or writing files under `tickets/` or top-level reports, (4) onboarding a new team member or agent to the project, (5) the user asks about repo structure / conventions / who-does-what / how-to-land-a-change, or (6) needing to align an implementation with the team's collaboration model.
---

# sharedwatch-contributor (project onboarding skill)

Load this skill when opening the sharedwatch repository for the first time, or when about to make non-trivial changes. The skill describes what sharedwatch is, why it exists, how the code is organized, how the team works, and how to land changes without violating load-bearing invariants.

## Product intent (one paragraph)

sharedwatch is a calm, durable, pull-based activity feed for a local shared folder. It watches a folder, captures every change into a SQLite-backed queue, and lets a human or AI agent review activity on their own cadence (every 5–10 minutes by default, near-real-time on demand). The watcher never interrupts — you pull a digest when you're ready. The intended business problem: **multiple AI agents (and humans) working in the same folder system without knowing what each other is doing.** sharedwatch is the substrate they use to find out.

## Mental model of the codebase

Three independent moving parts, all writing through one SQLite DB:

1. **Watcher** scans the folder on a tick, diffs against the last snapshot, coalesces noisy modify-bursts, and writes pending events. (`internal/watcher/`, `internal/catalog/`)
2. **Consumer** reads pending events on schedule, produces a digest row, marks events processed. (`internal/consumer/`, `internal/digest/`)
3. **Reconciler** runs as a heartbeat — re-diffs the folder to catch anything the watcher missed and enqueues recovery events. (`internal/reconcile/`)

Storage layer: `internal/db/` (SQLite via `modernc.org/sqlite`, WAL mode). Schemas live in `internal/db/db.go`'s `migrate()`. Query surface (events list, cursors, raw SQL, schema discovery) is in `internal/db/{events_query,cursor,raw,schema}.go`.

Orchestration: `internal/app/` runs the three loops with the file lock + signal handling. CLI entry: `cmd/sharedwatch/main.go`.

Repo layout, package-by-package detail, and "where to find X" tables are in `references/repo-map.md`.

## Load-bearing invariants (do not violate)

From `CONTRIBUTING.md` and the design notes:

1. **Queue-first stays queue-first.** Any change must preserve the "watcher writes only to the queue; consumer pulls digests" contract. Direct notification paths from watcher to consumer are out of scope.
2. **Pull over push.** No mechanism that interrupts the consumer mid-task. Active mode is allowed; auto-triggered consumer wake-ups outside the configured cadence are not.
3. **Durable by default.** Any state worth surviving a restart goes through the SQLite layer, not in-memory.
4. **One folder, one process.** No multi-host, no clustering, no networked queue. (Multi-folder *within* one process is being added — see SW-AGENT-3 — but multi-process and multi-host are out.)
5. **Polling is fine.** fsnotify is welcome as an additive option behind a flag; not a replacement. v1 stays portable and predictable.
6. **Duplicates beat misses.** The reconcile pass is the safety net for any change the watcher missed. Designs that "fix" reconcile duplicates by removing it are not improvements.

Every PR is held to these. If a change conflicts with one, the change is rejected or scope-changed — not the invariant.

## Engineering conventions

Quick list. Full details in `references/conventions.md`.

- **Go 1.22+.** `gofmt -w .` before committing. CI enforces.
- **Errors are returned, wrapped** with `fmt.Errorf("context: %w", err)`. No `panic` in non-test code outside `main`.
- **New code paths get at least one unit test.** Integration tests use `t.TempDir()` DBs.
- **No new dependencies without a clear reason.** `modernc.org/sqlite` is the only required external import. Adding a dep is a discussion, not a fait accompli.
- **Default to no comments.** When a comment IS warranted, document *why*, not *what*. Don't reference the current task / fix / caller — that rots.
- **Schema migrations are additive and idempotent.** Use the existing `addColumnIfMissing` helper. Never `DROP COLUMN`. New indexes use `IF NOT EXISTS`.
- **Public output shapes (`--format json`/`jsonl`) are contracts.** Add fields freely; never rename, never remove.

## Team model

Today (as of 2026-05):

- **Mike** — owner; final decision-maker; can assign work, priorities, approval scope.
- **Sarah** — structure, execution, documentation, coordination, follow-through; strong fit for task breakdowns, operating notes, decisions, checklists, making work concrete.
- **John** — ideas, design sense, positioning, messaging, shaping rough thoughts into clearer direction; strong fit for concept development, framing, narrative, creative refinement.

Working model: John explores ideas → Sarah structures into plans and durable artifacts → Mike approves where approval is needed.

This shape will grow. The team will scale beyond three. The skill that future contributors load should still describe a *role* model that scales (ideas/explore, structure/execution, ownership/approval, and increasingly: specialized agents per workstream). Treat role names in current artifacts as today's-Mike / today's-Sarah / today's-John but design conventions so that role *kinds* generalize.

Full team conventions, escalation rules, and growth strategy in `references/team.md`.

## File naming conventions

When you create artifacts in the repo (especially under the shared `/` directory, `tickets/`, or `docs/`), use these prefixes:

| Prefix | Purpose |
|---|---|
| `idea-YYYY-MM-DD-<short-name>.md` | John-mode: exploratory; goal + current idea + open questions + help wanted |
| `plan-YYYY-MM-DD-<short-name>.md` | Sarah-mode: structured plan turned from an idea |
| `decision-YYYY-MM-DD-<short-name>.md` | Recorded decision, who made it, why, what's settled |
| `tasks-YYYY-MM-DD-<short-name>.md` | Concrete TODO breakdown |
| `tasklist_YYYYMMDD_HHMMSS.md` | Session worklog (executed work record) |
| `ticket-<id>-<short-name>-YYYYMMDD_HHMMSS.md` | Filed work unit; lives in `tickets/` |
| `<topic>-YYYYMMDD_HHMMSS.md` | Reports, discussion docs at top level |

Status line near the top of any artifact: `Status: draft|proposed|approved|blocked|done`.

## Shared-folder collab policy

The repo root doubles as the team's shared folder (`docs/TEAM.md`, `tickets/`, `docs/reports/`). The policy from `docs/TEAM.md`:

- Files in this shared folder are approved for free internal team use.
- Shared-folder content may be read and written by team members for collaboration.
- Do not assume this permission extends outside the folder.
- **No destructive edits unless Mike explicitly asks.** Prefer additive updates.

For an agent operating in this folder: never delete a top-level artifact without explicit authorization. Add new files (or new sections in existing files) rather than overwriting.

## How to land a change

Standard flow:

1. **Read the ticket** (if one exists) or open a discussion artifact (`idea-...md`) if not.
2. **Branch** from `main`. (The current default branch is `master` in some forks — confirm.)
3. **Implement** with tests. `make ci` should pass locally:
   ```bash
   cd src && make ci   # = gofmt -l, go vet, go test ./...
   ```
4. **Smoke test** the user-visible flow:
   ```bash
   make smoke                  # builds + runs the README quickstart end-to-end
   ```
5. **Update docs** if behavior changed:
   - `src/README.md` if the Quickstart or command table changes
   - `src/CHANGELOG.md` under `[Unreleased]`
   - `src/docs/SCHEMA_CONTRACTS.md` if the DB schema changes
   - `src/docs/APPLICATION_FLOW.md` or `src/docs/STATE_MODEL.md` for state transition changes
6. **Open a PR.** Reference the ticket id (`SW-AGENT-N`) in the title.
7. **Mike approves** or asks for changes.

## Where to find things

| Looking for | Where |
|---|---|
| Repo layout, package responsibilities | `references/repo-map.md` |
| Engineering style details, test patterns | `references/conventions.md` |
| Team roles, growth plan, escalation | `references/team.md` |
| Currently open tickets | `references/tickets.md` |
| Original product spec | `docs/specs/shared-drive-watcher-spec.md` |
| All design / report / spec / agent docs | `docs/` (start at `docs/README.md`) |
| Application flow diagram | `src/docs/APPLICATION_FLOW.md` |
| State model | `src/docs/STATE_MODEL.md` |
| Schema contracts | `src/docs/SCHEMA_CONTRACTS.md` |
| Design intent ("why is it like this?") | `src/docs/JOHN_HANDOFF.md` |
| Operating guide | `src/docs/OPERATOR_GUIDE.md` |
| Recent discussion docs | top level: `sharedwatch-*-discussion-*.md`, `sharedwatch-*-report-*.md` |

## What this project is NOT

To save you from proposing things that will be rejected:

- Not a content-sync tool (no file contents are captured)
- Not push-based (consumer is always pull)
- Not multi-host (single host, single process)
- Not a messaging system (events ≠ messages; addressee is a hint, not a delivery channel)
- Not a locking system (leases are advisory)
- Not real-time (5 s minimum cadence; designed for "calm")
- Not a security/audit tool (no authentication; trust the local FS)

These boundaries are deliberate. If a customer ask sits outside, the answer is usually "publish sharedwatch events to a different tool that does that," not "extend sharedwatch."

## Where to look next

| Question | File |
|---|---|
| Detailed file inventory and package map | `references/repo-map.md` |
| Engineering conventions, test patterns, comment policy | `references/conventions.md` |
| Team roles, growth model, escalation triggers | `references/team.md` |
| Currently filed work | `references/tickets.md` |
