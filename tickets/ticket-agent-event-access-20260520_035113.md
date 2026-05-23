# TICKET: Open up sharedwatch as an agent-facing event journal

**ID:** SW-AGENT-1
**Filed:** 2026-05-20 03:51 UTC
**Filed by:** claude
**Status:** open, not yet claimed
**Target:** sharedwatch v0.6.x (next minor)
**Estimated:** ~1 focused engineer-day across all three features

---

## 1. Context (read this first)

Three prior deliverables in this directory establish the why:

- `sharedwatch-engineering-report-20260520_015418.md` — structural map, bug list, rubric (27/60 baseline before this session's work).
- `sharedwatch-pmm-report-20260520_015418.md` — positioning, users, launch checklist.
- `sharedwatch-agent-fit-20260520_034245.md` — **the analysis that motivated this ticket**. Read §3, §4 (gaps 1, 2), §6, §8.

And one work record:

- `tasklist_20260520_022339.md` — the open-up tasklist. Sessions 1, 2, 3 closed the build/test/DX blockers and the OSS hygiene items (LICENSE, CI, README, CHANGELOG, file lock, WAL, structured logs, init/version/help, retry, etc.). The repo is now at v0.5.0-20260520.

The repo state this ticket assumes:
- Builds clean, 11 packages green.
- Concurrent CLI works against an active `run` (WAL/busy_timeout fix already landed).
- `db.Adapter` interface is complete; no compile gaps.
- `events` table schema is stable and indexed for the queries this ticket wants.

This ticket is the *next* unit of work. It does not re-litigate anything above.

## 2. The problem

> An agent that wants to use sharedwatch as the "what happened in this folder" oracle currently has to drop to `sqlite3` and write SQL, because the CLI is shaped for humans reading digests.

Specifically — and this is from the agent-fit doc §4, with priorities reordered after this user's framing:

1. **No first-class events query.** `digest list` is the only built-in way to ask about activity, and it goes through prose summaries.
2. **No "what's new since I last looked" primitive.** Every agent has to roll its own timestamp cursor and re-discover the same edge cases (clock skew, in-same-nanosecond ordering, reconcile inserting older events later).
3. **No explicit raw-SQL escape hatch.** The agent that *does* want raw SQL has to know the DB path, install sqlite3 separately, and format its own output. We should accept that this use case exists and make it cheap, not pretend it doesn't.
4. **Output formats are human-shaped, not agent-shaped.** File event streams compress densely — a folder with 5 collaborators churning produces hundreds of events per hour. Per-line streaming (JSONL) and column projection (`--fields`) matter for piping into anything else.

Closing 1+2+3+4 turns sharedwatch from "the data is there if you SQL" into "the tool is built for me" (agent-fit §6).

## 3. In scope (this ticket)

### Feature A — `sharedwatch events list`

New top-level subcommand. Read-only. Does NOT change event status (does not consume).

**Flags (all optional):**
| Flag | Type | Purpose |
|---|---|---|
| `--since <ts>` | RFC3339 | Lower bound on `observed_at`. Inclusive. |
| `--until <ts>` | RFC3339 | Upper bound on `observed_at`. Exclusive. |
| `--type <t>` | string, repeatable | Filter to `file.created` / `file.modified` / `file.deleted` / `file.renamed`. Repeatable for OR. |
| `--source <s>` | string, repeatable | Filter to `watcher` / `reconciler` / `test`. Repeatable for OR. |
| `--status <s>` | string, repeatable | Filter to `pending` / `processing` / `processed` / `failed` / `suppressed`. Repeatable. |
| `--path-glob <g>` | string | Filter on `rel_path` (Go `filepath.Match` semantics, evaluated server-side via LIKE-conversion or post-filter). |
| `--limit <N>` | int, default 100 | Cap result count. `0` = no cap. |
| `--order <asc\|desc>` | string, default `desc` | Order by `created_at`. |
| `--format <fmt>` | string, default `text` | `text`, `json`, `jsonl`, `csv`. See §3.D. |
| `--fields <list>` | string | Comma-separated column projection: e.g. `--fields id,type,rel_path,created_at`. Default: all columns. |
| `--since-cursor <tok>` | string | See Feature B. Mutually exclusive with `--since`. |
| `--cursor-name <n>` | string | See Feature B. |
| `--no-advance` | bool | When `--cursor-name` is set, don't write back the new cursor; just peek. |

**Exit codes:** `0` always when query succeeds (including zero rows). `1` on DB error. `2` on flag-parse error.

### Feature B — Cursor primitive

Two layers, both required.

**B.1 Stateless cursor token.**
Opaque base64 of `{created_at_nano:int64, id:string}`. Tied to the DB's logical event order, not wall-clock. Re-encoded on every page.

API:
- `events list --since-cursor <tok>` → page of events newer than the cursor + `next_cursor` field embedded in the output (last line for JSONL; final element for JSON; new column for CSV; trailer line `# next_cursor=…` for text).
- `events cursor encode --created-at <ts> --id <evt_id>` → token (for testing / agent constructing one manually).
- `events cursor decode <tok>` → `{created_at, id}` (debugging).

**B.2 Named server-side cursor.**
A new `cursors` table: `(name TEXT PRIMARY KEY, created_at_nano INTEGER NOT NULL, last_id TEXT NOT NULL, updated_at TEXT NOT NULL)`.

API:
- `events list --cursor-name my-agent` → reads from saved position, advances on success (unless `--no-advance`).
- `events cursor list` → all named cursors with their positions.
- `events cursor reset <name>` → delete the cursor (next read starts from oldest).
- `events cursor set <name> --since-cursor <tok>` → seed a cursor to a specific position.

**Cursor semantics (write this down, it's where bugs live):**
- Cursor advances only on successful response delivery (commit happens after the query result is materialized).
- Ordering is `(created_at_nano ASC, id ASC)` for cursor iteration. Always ASC regardless of `--order` for display.
- When `--limit` truncates, cursor advances to the last *returned* row, not the last *matching* row.
- Reconcile-inserted events from earlier wall-clock times use their *observed* `created_at` (== insert time), not the file's mtime. So "since cursor" can include events with old mtimes but recent created_at. This is correct — the agent is asking "what did I learn about since last time", not "what changed in wall-clock order".

### Feature C — Raw SQL escape hatch

New top-level subcommand. Acknowledges that the agent will sometimes just want to write SQL, and makes it cheap.

```
sharedwatch sql "SELECT id, type, rel_path FROM events WHERE status = 'failed' LIMIT 10"
echo "SELECT COUNT(*) FROM events" | sharedwatch sql -
sharedwatch sql --file query.sql --format jsonl
```

**Flags:**
| Flag | Purpose |
|---|---|
| positional `<sql>` or `-` | SQL on the command line, or `-` to read from stdin. |
| `--file <path>` | Read SQL from a file. |
| `--format <fmt>` | `text` (default, tab-aligned), `json`, `jsonl`, `csv`. |
| `--write` | Allow non-SELECT statements. Default is read-only (refuse INSERT/UPDATE/DELETE/DROP/ALTER/CREATE/PRAGMA without this). |
| `--explain` | Print `EXPLAIN QUERY PLAN <sql>` before executing. |

**Read-only enforcement:** parse the leading keyword after stripping comments/whitespace. If it isn't `SELECT` or `WITH` or `EXPLAIN`, refuse unless `--write` is set. Yes, this can be tricked; that's fine — it's a tripwire, not a security boundary. The DB file is right there with normal POSIX perms.

**Output:** all four formats follow the same shape as Feature A's output formats. See §3.D.

### Feature D — Output formats

A small shared output package used by both `events list` and `sql`.

- **`text`** — human-readable, tab-aligned columns, no header by default; `--header` to add one. For cursor results, append `# next_cursor=<tok>` as a trailer line so an agent piping through `tail -1` can capture it.
- **`json`** — single JSON object: `{"rows": [...], "next_cursor": "..."}`. Cursor present only when relevant.
- **`jsonl`** — one row per line as a JSON object. If cursor applies, final line is `{"next_cursor": "..."}` (sentinel object — agent reads until EOF, last line carries the cursor). This is the **recommended format for agents handling high-volume event streams**.
- **`csv`** — RFC-4180. Header row always included. Cursor appended as a final commented row `# next_cursor,<tok>` (CSV consumers that don't tolerate comments should pass `--no-cursor`).

`--fields` works against all four. Field names match SQL column names exactly (so an agent reading the schema once knows what to ask for).

### Feature E — `sharedwatch schema`

Tiny but disproportionately useful. Print the live DDL so agents writing SQL don't have to grep the source.

```
sharedwatch schema                 # all tables, indexes
sharedwatch schema events          # one table
sharedwatch schema --format json   # structured: [{name, columns: [{name,type,nullable,pk}]}]
```

Implementation: read `sqlite_master` plus `PRAGMA table_info(<name>)`. ~30 lines.

## 4. Out of scope (do NOT do in this ticket)

These came up in the agent-fit doc and will get separate tickets:

- **Producer-supplied `payload_json`** (agent-fit Gap 3). Needs design on the path-prefix → tags mapping and on the `test emit --payload` ergonomics. Separate ticket: SW-AGENT-2.
- **Multi-folder watching** (Gap 5). Real schema impact (`snapshots.path` becomes part of the key). Separate ticket: SW-AGENT-3.
- **Content hash population** (Gap 6). Cheap to do but changes per-tick CPU cost; needs a `--hash on|off|size-cap=N` flag. Separate ticket: SW-AGENT-4.
- **Producer attribution `producer_id`** (Gap 7). Schema migration. Separate ticket: SW-AGENT-5.
- **Include-only path globs on the watcher** (Gap 8). Lowest priority. Separate ticket: SW-AGENT-6.

This ticket touches only the *consumer* side of the existing event journal. No producer-side or schema-shape changes beyond adding the `cursors` table.

## 5. Acceptance criteria

A reviewer can copy-paste these into a terminal and they should all work.

```bash
# A1: events list with no filters
sharedwatch events list --limit 5

# A2: filter by type + path + recency
sharedwatch events list --type file.modified --path-glob 'auth/**' --since 2026-05-20T00:00:00Z --format json

# A3: stream all events as JSONL, suitable for piping
sharedwatch events list --limit 0 --order asc --format jsonl | head -1000

# A4: column projection
sharedwatch events list --fields id,type,rel_path,created_at --format csv

# B1: stateless cursor round-trip
TOK=$(sharedwatch events list --limit 1 --format json | jq -r .next_cursor)
sharedwatch events list --since-cursor $TOK --format json | jq .

# B2: named cursor that advances on read
sharedwatch events cursor reset my-agent          # idempotent if absent
sharedwatch events list --cursor-name my-agent --limit 5 --format jsonl
sharedwatch events list --cursor-name my-agent --limit 5 --format jsonl   # returns rows AFTER the first call

# B3: peek without advancing
sharedwatch events list --cursor-name my-agent --no-advance --format jsonl

# C1: read-only SQL
sharedwatch sql "SELECT COUNT(*) FROM events"

# C2: refuse writes by default
sharedwatch sql "DELETE FROM events"             # exit 2, "use --write to allow"

# C3: allow writes explicitly
sharedwatch sql --write "UPDATE events SET status='pending' WHERE status='failed'"
# (equivalent of `events retry`; we tolerate the overlap.)

# C4: stdin + jsonl
echo "SELECT id, type FROM events LIMIT 3" | sharedwatch sql - --format jsonl

# E1: schema discovery
sharedwatch schema                # prints DDL for events/digests/runtime_state/snapshots/cursors
sharedwatch schema events --format json
```

Plus: every existing test still passes; `gofmt -l .` clean; `go vet ./...` clean.

## 6. Required tests

- `internal/db/events_query_test.go` — exercise `Store.QueryEvents(filter)` (the helper backing Feature A) across each filter dimension; assert no false positives, no missing rows.
- `internal/db/cursor_test.go` — encode/decode round-trip; advance-on-read; reset; multiple named cursors don't interfere; cursor stable across a re-order (insert an event with backdated `created_at`, confirm cursor doesn't skip it on next read).
- `internal/db/sql_passthrough_test.go` — read-only enforcement; reject SQL injection of `;DROP TABLE` style multistatements (refuse `;` if not in `--write`); JSONL/CSV/JSON formatters.
- `internal/db/schema_introspect_test.go` — `Schema()` returns all tables; per-table column metadata matches what `migrate()` created.
- End-to-end dogfood (manual or scripted): the §5 acceptance block, run verbatim against a temp DB.

## 7. Open questions (please answer before claim)

1. **Should `events list` default to read-only or claim?** This ticket says read-only (does NOT change `status`). The existing `consume` is the destructive path. If we ever want `events list` to also be able to claim for some workflow, we'd need a `--claim` flag — but I'd default to never. Confirm.

2. **JSONL trailer line for cursor — sentinel object or separate stream?** Proposed: sentinel `{"next_cursor": "..."}` as the last line. Alternative: write cursor to stderr. Sentinel is simpler for piping (single stream); stderr is cleaner for spec-pure JSONL consumers. Default in this ticket: sentinel.

3. **`--write` SQL — should we *also* gate it behind a config-file `allow_raw_writes: true`?** Belt-and-suspenders against an agent that "knows what it's doing" and isn't supposed to. Default in this ticket: no — `--write` flag alone is the gate; agents that shouldn't get it shouldn't get the CLI access.

4. **Cursor storage durability — same DB as events, or a sidecar file?** Same DB is simpler and inherits the WAL concurrency story for free. Default in this ticket: same DB, new `cursors` table. A future "I want to read events without writing" use case (truly read-only DB) would need to revisit, but that's not in scope here.

5. **Backwards compat for `Adapter` interface?** This ticket adds methods (`QueryEvents`, `UpsertCursor`, `GetCursor`, `DeleteCursor`, `ListCursors`, `RawSQL`, `Schema`). Default: extend the interface, force re-implementation if a second backend is ever added. There is no second backend today.

## 8. Risk / concerns

- **The `sql` subcommand is a foot-gun.** An agent with `--write` access can wreck the DB. Mitigation: read-only default, explicit flag for writes, clear `--explain` for debugging. Accept the residual risk; the DB file is already filesystem-accessible to anyone with the process's privileges.
- **Cursor semantics around reconcile-inserted late events** are subtle (covered in §3.B). Tests must explicitly cover this case.
- **`--path-glob` evaluation cost.** SQLite's `GLOB` is fine for simple patterns; for nested `**` we may need a post-filter pass. Acceptable since `--limit` bounds the work.
- **Output format churn.** Once agents start consuming JSONL output, the shape is a contract. Add a `format_version: 1` field in the JSON/JSONL envelope so we can evolve.

## 9. Deliverables checklist (for the eventual tasklist)

- [ ] New `internal/db/events_query.go` — `Store.QueryEvents(EventFilter)` returning `[]events.Event` and a cursor.
- [ ] New `internal/db/cursor.go` + `cursors` table migration (idempotent CREATE TABLE).
- [ ] New `internal/db/raw.go` — `RawSQL(ctx, sql, allowWrite bool)` returning `[][]any` + column names.
- [ ] New `internal/db/schema.go` — `Schema()` returning table/column metadata.
- [ ] New `internal/output/format.go` — shared `text|json|jsonl|csv` formatter used by both subcommands.
- [ ] Wiring in `cmd/sharedwatch/main.go`: `events list`, `events cursor`, `sql`, `schema` subcommands; updated `help` text.
- [ ] Tests per §6.
- [ ] `CHANGELOG.md` `[Unreleased]` entries.
- [ ] `README.md` adds a short "for agents" section: `events list`, `cursor`, `sql`, `schema`.
- [ ] Dogfood pass: §5 acceptance block, run end-to-end.

## 10. References

- `sharedwatch-agent-fit-20260520_034245.md` §3, §4 (gaps 1+2), §6, §8 — motivation, what NOT to add.
- `sharedwatch-engineering-report-20260520_015418.md` §10 — interface assessment (CLI gaps).
- `sharedwatch-pmm-report-20260520_015418.md` §4 — positioning (this ticket shifts the positioning subtly; README update required).
- `tasklist_20260520_022339.md` — prior open-up work; this ticket is the next session's input.
- `sharedwatch/CHANGELOG.md` — current `[Unreleased]` block.
- `sharedwatch/internal/db/db.go` — existing schema + indexes; this ticket reuses both.

## 11. Definition of done

- All §5 acceptance commands work against a real DB.
- All §6 tests pass.
- `gofmt -l .` clean, `go vet ./...` clean, `go test ./...` clean.
- CHANGELOG + README updated.
- A new dogfood log entry in `tasklist_<bashdate>.md` (next session) describes what was claimed, what was verified, what was deferred.
- The agent-fit doc's verdict (§6 "yes, but reluctantly") can credibly be updated to "yes, comfortably".
