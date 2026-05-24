# sharedwatch — CLI reference (today's surface)

Authoritative reference for the sharedwatch commands available as of v0.0.5. Cross-checks with the live binary via `sharedwatch help`. When in doubt, run `sharedwatch <subcommand> --help`.

## Contents
1. Global flags + env vars (`SHAREDWATCH_*` resolution chain)
2. Lifecycle (`init`, `run`, `stop`, `version`, `help`, `update`)
3. Status + introspection (`status`, `roots`, `config show`, `mode`)
4. Consumption (`consume`, `digest`)
5. Event journal (`events list`, `events cursor`, `events retry`, `events recover-stuck`, `events stats`)
6. Cross-root summary (`overview`)
7. Cooperative coordination (`actor heartbeat`, `intent declare`, `lease acquire`)
8. Raw SQL (`sql`)
9. Schema discovery (`schema`)
10. Reconciliation (`reconcile`)
11. Synthetic emission (`test emit`)
12. Output formats + smart hints (`next[]`)
13. Exit codes

> **Quick note for agents**: see the "Agent startup ritual" in [`../SKILL.md`](../SKILL.md) for the `export SHAREDWATCH_*` block that lets you skip identity flags on every command.

## 1. Global flags

Set BEFORE the subcommand: `sharedwatch <global flags> <subcommand> <subflags>`.

| Flag | Default | Notes |
|---|---|---|
| `--config <path>` | `config.yaml` | Optional; silently ignored if missing |
| `--watch-path <dir>` | `$XDG_DATA_HOME/sharedwatch/watch` | Overrides config |
| `--db <file>` | `$XDG_DATA_HOME/sharedwatch/queue.db` | Overrides config |
| `--data-dir <dir>` | `$XDG_DATA_HOME/sharedwatch` | Informational; reported by `init` |
| `--ignore <pat>` | (empty) | Repeatable, comma-aware; appends to default ignore patterns |
| `--include <pat>` | (empty) | Repeatable, comma-aware; include-only filter applied BEFORE ignores. Empty = include all. |
| `--hash on\|off` | `off` | Enable per-file SHA-256 content hashing (skips files above `HashMaxSize`, default 1 MB) |
| `--producer <str>` | `<host>:<pid>` | Sets `events.producer_id` stamped on emitted events. **Flag is `--producer` (singular), not `--producer-id`.** |
| `--actor <id>` | (empty) | payload_json v1 attribution — see "Attribution flags" below |
| `--actor-kind <k>` | (empty) | `human` / `ai_agent` / `automation` |
| `--session <id>` | (empty) | logical-run id |
| `--task <label>` | (empty) | short work label |
| `--intent <text>` | (empty) | one-sentence reason |
| `--addressee <id>` | (empty) | who the change is FOR |
| `--ref <event-id>` | (empty) | causal predecessor (ref_event_id) |
| `--tag <t>` | (empty) | payload tag (repeatable, comma-aware) |
| `--log-format text\|json` | `text` | `json` for log aggregators |
| `--log-level debug\|info\|warn\|error` | `info` | |
| `--version` | — | Same as the `version` subcommand |

Precedence: defaults < config file < flags. The config file does NOT accept `--include`, `--hash`, or `--producer` keys — these are flag-only (see `references/config-file.md`).

## 2. Lifecycle

```bash
sharedwatch init           # create data dir + DB; print resolved paths
sharedwatch run            # start watcher + consumer + reconcile loop (blocks)
sharedwatch version        # print version
sharedwatch help           # print help
```

`run` acquires a file lock (`<data_dir>/sharedwatch.lock`). One `run` per data dir.

## 3. Status & mode

```bash
sharedwatch status                # human-readable
sharedwatch status --json         # machine-readable

sharedwatch mode active --ttl 30m # fast cadence (5 s); auto-decays
sharedwatch mode passive          # force passive (10 min cadence)
```

`status --json` fields (today): `mode`, `pending`, `failed`, `processed`, `digest_count`, `last_*_at`. Multi-root will add a `roots[]` array.

## 4. Consumption

```bash
sharedwatch consume                                     # one consume cycle, on demand
sharedwatch digest list [--limit N] [--status <s>]      # newest digests first
sharedwatch digest show <id>                            # full prose summary; marks read
sharedwatch digest archive <id>                         # mark archived
```

`digest list --status` accepts `pending` / `read` / `archived` (empty = all). Default `--limit` is 20.

Agents prefer `events list` over `digest`.

## 5. Event journal

### `events list`

| Flag | Notes |
|---|---|
| `--since <ts>` | RFC3339 lower bound on `created_at` (inclusive); also accepts `1h`, `15m` durations |
| `--until <ts>` | RFC3339 upper bound (exclusive) |
| `--type <t>` | repeatable; `file.created` / `file.modified` / `file.deleted` / `file.renamed` |
| `--source <s>` | repeatable; `watcher` / `reconciler` / `test` |
| `--status <s>` | repeatable; `pending` / `processing` / `processed` / `failed` / `suppressed` |
| `--producer <id>` | repeatable; filter by `producer_id` exact match |
| `--path-glob <g>` | `filepath.Match` semantics + `**` for any depth |
| `--limit <N>` | default 100; `0` = no cap |
| `--order asc\|desc` | default `desc` |
| `--format text\|json\|jsonl\|csv` | default `text` |
| `--fields <list>` | comma-separated column projection (see below for valid names) |
| `--since-cursor <tok>` | stateless cursor token (mutually exclusive with `--since`) |
| `--cursor-name <n>` | named server-side cursor; advances on read |
| `--no-advance` | when `--cursor-name` set, peek without advancing |
| `--payload-key <k>` `--payload-value <v>` | post-fetch filter on `payload_json[k] == v` (flat top-level keys only) |

**Valid `--fields` column names** (as exposed by the CLI):

`id`, `type`, `rel_path`, `old_path`, `source`, `status`, `retry_count`, `created_at`, `file_size`, `mtime`, `content_hash`, `coalesced_into`, `producer_id`, `payload_json`

Important quirk: the CLI projects a column labeled `created_at` whose **value comes from the DB column `observed_at`** (the watcher's observation timestamp). The DB has both columns but the CLI surfaces only the one labeled `created_at`. For SQL queries (`sharedwatch sql ...`), both `created_at` and `observed_at` are addressable directly.

Cursor semantics:
- When `--cursor-name` or `--since-cursor` is set, iteration is ASC over `(created_at, id)` regardless of `--order`.
- Cursor advances to the last *returned* row, not the last *matching* row.
- Reading without a cursor preserves your `--order` preference and does NOT advance any state.

### `events cursor`

```bash
sharedwatch events cursor list                    # all named cursors
sharedwatch events cursor reset <name>            # delete; next read starts from oldest
sharedwatch events cursor set <name> --since-cursor <tok>   # seed to a token
sharedwatch events cursor encode --created-at <ts> --id <evt_id>   # build a token
sharedwatch events cursor decode <tok>            # inspect a token
```

### `events retry`

```bash
sharedwatch events retry                          # requeue ALL failed events to pending
sharedwatch events retry --max-retries 3          # skip events that have already failed N+ times (0 = no cap)
```

Reports the count requeued. Use after fixing whatever caused the consumer failures.

### `events recover-stuck`

```bash
sharedwatch events recover-stuck                  # default --older-than 5m
sharedwatch events recover-stuck --older-than 10m
```

Flips events stuck in `status='processing'` (older than the threshold) back to `pending`. Catches the case where a consumer crashed between `ClaimPendingEvents` and `MarkEventsProcessed`. Idempotent — safe to run anytime.

## 6. Raw SQL

```bash
sharedwatch sql "SELECT COUNT(*) FROM events"
sharedwatch sql -                       # read from stdin
sharedwatch sql --file query.sql --format jsonl
sharedwatch sql --explain "SELECT ..."  # prints EXPLAIN QUERY PLAN before executing
sharedwatch sql --write "UPDATE ..."    # requires explicit flag
```

Read-only enforcement: anything whose leading keyword (after stripping comments/whitespace) is not one of `SELECT`, `WITH`, `EXPLAIN`, `PRAGMA TABLE_INFO`, `PRAGMA INDEX_LIST`, `PRAGMA INDEX_INFO` is refused unless `--write` is set. Multi-statement input (any `;` not at the trailing position) is also refused without `--write`. This is a tripwire, not a security boundary — the DB file is on disk with normal POSIX perms.

`--format` accepts `text|json|jsonl|csv` (same as `events list`).

## 7. Schema discovery

```bash
sharedwatch schema                  # all tables, printed as DDL
sharedwatch schema events           # one table
sharedwatch schema --format json    # structured: [{name, sql, columns: [{name,type,not_null,primary_key,default}]}]
```

`--format` here accepts **only `text` or `json`** (not `jsonl`/`csv`).

Recommended: run `sharedwatch schema --format json` once per session, cache the result in the agent's working memory. Eliminates a class of hallucinated-column queries.

## 8. Reconciliation

```bash
sharedwatch reconcile now           # run reconcile pass immediately
```

Reconcile re-snapshots the folder, diffs against the last reconcile snapshot, and enqueues recovery events for anything the live watcher missed. Idempotent — safe to run anytime.

## 9. Synthetic emission

```bash
sharedwatch test emit <relpath>                                       # default payload
sharedwatch test emit <relpath> --actor X --session Y --task Z ...    # SW-AGENT-7 flags
sharedwatch test emit <relpath> --payload '{"k":"v"}'                 # raw JSON
```

`relpath` is interpreted relative to `--watch-path`. Paths escaping the root are rejected.

Attribution flags (`--actor`, `--actor-kind`, `--session`, `--task`, `--intent`, `--addressee`, `--ref`, `--tag`) are available on `test emit` AND inherit from the root flagset when both forms are used; subcommand non-empty values override root per-field. **`--payload` is mutually exclusive with the attribution flags on the same command** — pick one form.

### Attribution flags

Available on root (apply to every event during the invocation, including watcher-detected events during `run`) and on `test emit` (override per-event). Flag-to-payload-key mapping:

| Flag | payload_json key |
|---|---|
| `--actor` | `actor` |
| `--actor-kind` | `actor_kind` |
| `--session` | `session` |
| `--task` | `task` |
| `--intent` | `intent` |
| `--addressee` | `addressee` |
| `--ref` | `ref_event_id` |
| `--tag` (repeatable) | `tags[]` |

`schema_version` is always set to `1` on the emitted payload; you do not need to (and cannot) override it.

## 10. Output formats

- **`text`** — tab-aligned, no header. For cursors, trailing `# next_cursor=<tok>` line.
- **`json`** — `{"rows": [...], "next_cursor": "..."}`. Cursor key present only when relevant.
- **`jsonl`** — one JSON object per line. For cursors, final line is `{"next_cursor": "..."}`. **Preferred for agent streaming consumption.**
- **`csv`** — RFC-4180 with header. Cursor appended as a commented row.

`--fields` works against all four. Field names match SQL column names exactly.

## 11. Exit codes

- `0` — success (including zero rows)
- `1` — any operational error (DB error, SQL violation rejected by `--write` guard, invalid `--payload` JSON, etc.) — routed through `fatal()` which prints `error: <msg>` to stderr.
- `2` — flag-parse / usage error (bad subcommand, missing required positional)

There is no separate exit code for SQL violations — they are operational errors and exit `1`. Always check exit code in scripts, not just stdout content.
