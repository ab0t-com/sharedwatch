# Tickets — current and historical

The work-tracking surface for sharedwatch. Updated 2026-05-22.

Tickets live in `tickets/` at the repo root. Naming: `ticket-<short-topic>-YYYYMMDD_HHMMSS.md`. Session worklogs are `tasklist_YYYYMMDD_HHMMSS.md` in the same directory.

## Contents
1. How tickets work
2. Landed tickets (reference)
3. Open tickets
4. Discussion docs (pre-ticket)
5. Filing a new ticket

## 1. How tickets work

A ticket is a filed-and-tracked work unit with:
- **ID** — `SW-AGENT-<N>` for agent-fit work, `SW-<N>` for other work.
- **Context** — why this matters, with back-references to prior reports.
- **In-scope** — what this ticket does.
- **Out-of-scope** — what it deliberately does NOT do.
- **Acceptance criteria** — copy-paste shell commands that should work.
- **Required tests** — what tests must exist before merge.
- **Open questions** — for the implementer to resolve before claiming.
- **Risks** — known sharp edges.
- **Deliverables checklist** — for the eventual tasklist.

A ticket is *not* a tasklist. A tasklist is a session log of what actually got done.

## 2. Landed tickets (reference)

### SW-AGENT-1 — Open up sharedwatch as an agent-facing event journal
File: `tickets/ticket-agent-event-access-20260520_035113.md`
Status: **LANDED**

Shipped:
- `sharedwatch events list` with full filter set (`--since`, `--until`, `--type`, `--source`, `--status`, `--path-glob`, `--limit`, `--order`, `--format`, `--fields`)
- Stateless cursor tokens + named server-side cursors (`events cursor list/reset/set/encode/decode`)
- Raw SQL escape hatch (`sharedwatch sql` with read-only enforcement)
- Schema discovery (`sharedwatch schema --format json`)
- Output formats: text / json / jsonl / csv
- `--payload-key`/`--payload-value` post-fetch filter on `payload_json`

Cross-refs:
- `sharedwatch-agent-fit-20260520_034245.md` — original motivation
- `sharedwatch-engineering-report-20260520_015418.md` §10 — interface gap analysis

## 3. Open tickets

### SW-AGENT-3 — Multi-folder watching
File: `tickets/ticket-multi-folder-watching-20260520_101921.md`
Status: **OPEN, not yet claimed**
Target: sharedwatch v0.8.x
Estimated: ~1.5 focused engineer-days

What it adds:
- One process, one DB, multiple watch-roots.
- `watch_root` column on `events`, `digests`, `snapshots`.
- `--root <label>=<path>` (definition) and `--root <label>` (filter).
- Per-root cold-start, per-root snapshot lookup.
- Cross-root correctness invariants (no coalesce / rename across roots).
- `status --json` includes `roots[]`.

Critical: cross-root correctness is non-negotiable. See the ticket's §3 (pipeline correctness) and §6 (required tests).

Cross-refs:
- `sharedwatch-multifolder-design-20260522.md` — design quality layer (storage, access, defaults, control surface)
- `sharedwatch-agent-fit-20260520_034245.md` §4 Gap 5 — original motivation

## 4. Discussion docs (pre-ticket)

These live at the repo root, not in `tickets/`. They are read-only thinking artifacts that *may* become tickets:

| Doc | Topic | Status |
|---|---|---|
| `sharedwatch-multi-agent-discussion-20260522.md` | Multi-agent feature inventory, business problem framing | Discussion |
| `sharedwatch-disclosure-attribution-discussion-20260522.md` | Progressive disclosure (L1–L6) + attribution layers | Discussion |
| `sharedwatch-multifolder-design-20260522.md` | Multi-folder design quality (over SW-AGENT-3) | Discussion (extends an open ticket) |
| `sharedwatch-agent-system-prompt-20260522.md` | System prompt template for agents using sharedwatch | Reference |
| `test_dogfood.md` | Runnable dogfood scenarios | Reference |

Likely next tickets, derived from the discussion docs (not yet filed):

- **SW-AGENT-7** — Documented `payload_json` v1 schema + `--actor`/`--session`/`--task`/`--intent`/`--addressee`/`--ref`/`--tag` flags. ~0.5d.
- **SW-AGENT-8** — `actors` registry + `actor heartbeat` + `status --actors`. ~1d.
- **SW-AGENT-9** — `overview` (L1) endpoint + `drill` hint contract. ~1d.
- **SW-AGENT-10** — `events stats --root <X>` (L2) endpoint. ~0.5d.
- **SW-AGENT-11** — Actor-aware coalesce (don't merge two actors' events on the same path). ~1d. Discovered via dogfood scenario #17.
- **SW-AGENT-12** — `intents` and `leases` cooperative coordination. ~1.5d.

These are speculative — they live as discussion until someone (typically Sarah-mode) lifts them into formal tickets with acceptance criteria.

## 5. Filing a new ticket

If you have a unit of work big enough to deserve a ticket:

1. Pick an ID: `SW-AGENT-<N>` for agent-fit work, `SW-<N>` otherwise. Check `tickets/` for the next free number.
2. Filename: `tickets/ticket-<short-topic>-YYYYMMDD_HHMMSS.md`. Use `date +%Y%m%d_%H%M%S` for the timestamp.
3. Use the existing tickets as templates (especially SW-AGENT-1 and SW-AGENT-3 — they're both well-structured).
4. Required sections:
   - Context (with cross-refs to prior reports / tickets / discussions)
   - Why this matters
   - In scope
   - Out of scope
   - Acceptance criteria (shell commands that should pass)
   - Required tests
   - Open questions
   - Risks
   - Deliverables checklist
5. Set `Status: open, not yet claimed` at the top.
6. Add a brief mention to this `tickets.md` reference under §3.

When the ticket lands, move it from §3 to §2 and add a one-paragraph summary of what shipped.

## Session worklogs

Tasklists (`tasklist_*.md`) live in `tickets/` and are session-by-session worklogs. Each one captures what was actually done in one focused work session: commits, decisions, blockers, deferrals. Tasklists are the historical record; tickets are the forward plan.

When you start a focused work session that touches more than ~3 files or runs more than ~30 minutes, open a new `tasklist_YYYYMMDD_HHMMSS.md` and update it as you go. Future contributors will thank you.
