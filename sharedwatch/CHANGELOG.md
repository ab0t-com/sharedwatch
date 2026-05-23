# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added — agent-fit follow-up (SW-AGENT-2, 4, 5, 6 + small fixes)
- **Producer-supplied payload (SW-AGENT-2):** `sharedwatch test emit <relpath> --payload '<json>'` accepts arbitrary JSON object payload; stored in `events.payload_json`. Validated as JSON before insert.
- **Payload filter (SW-AGENT-2):** `events list --payload-key K --payload-value V` post-filters by parsing `payload_json` as an object and matching `obj[K] == V`.
- **Content hashing (SW-AGENT-4):** `--hash on|off` global flag enables SHA-256 of file content during snapshot building. Bounded by `HashMaxSize` (default 1 MB) so big binaries aren't hashed. Hash now propagates correctly into `file.created`/`file.modified`/`file.deleted` events (was previously empty — a real bug discovered in dogfooding). Detects same-size, same-mtime, different-content changes.
- **Producer attribution (SW-AGENT-5):** new `events.producer_id` column (migrated idempotently). Default producer is `<hostname>:<pid>`; `--producer <name>` global flag overrides. All emitted events (watcher, reconcile, test emit) stamp the producer. `events list --producer <name>` (repeatable) filters.
- **Include-only globs (SW-AGENT-6):** `--include <pattern>` global flag (repeatable + comma-aware) restricts the watcher/reconciler to a positive set of files. Applied before `IgnorePatterns`.
- **Retry cap:** `events retry --max-retries N` only requeues events whose `retry_count < N`. Default 0 = no cap (current behavior).
- **Stuck-event recovery:** `events recover-stuck [--older-than 5m]` flips events stuck in `status='processing'` (e.g. from a crashed consumer) back to `pending`.
- `--fields` now exposes `retry_count` and `coalesced_into` (previously silently dropped).

### Fixed
- **Hash propagation in diff:** `watcher.DiffSnapshots` was constructing event rows without `Hash`, so even with hashing enabled the modified-event would carry an empty hash, and coalesce-on-pending would preserve the stale baseline hash. Now propagates `oldFile.Hash` / `newFile.Hash` into create/modify/delete events. Dogfood: frozen-mtime same-size content swap now produces a `file.modified` with the correct new hash.

### Added — agent-facing event surface (SW-AGENT-1)
- `sharedwatch events list` — read-only event query with `--since`, `--until`, `--type` (repeatable), `--source` (repeatable), `--status` (repeatable), `--path-glob` (supports `**`), `--limit`, `--order asc|desc`, `--format text|json|jsonl|csv`, `--fields` (column projection), `--since-cursor`, `--cursor-name`, `--no-advance`. Does NOT consume events.
- Cursor primitive:
  - opaque stateless tokens via `--since-cursor <tok>` (base64-encoded `{created_at_nano, id}`)
  - named server-side cursors persisted in a new `cursors` table; advance on read by default, `--no-advance` to peek
  - `sharedwatch events cursor list | reset <name> | set <name> --since-cursor <tok> | encode --created-at <ts> --id <evt_id> | decode <tok>`
- `sharedwatch sql <query>` — raw SQL escape hatch. Read-only by default (rejects DELETE/UPDATE/CREATE/etc + multi-statement); `--write` to opt in; `--explain` prints `EXPLAIN QUERY PLAN` to stderr; `--format text|json|jsonl|csv`. Accepts query inline, via `-` (stdin), or `--file <path>`; works with flags in any order relative to `-`.
- `sharedwatch schema [<table>] [--format text|json]` — prints live DDL from `sqlite_master` + structured per-column metadata.
- Shared `internal/output` package with `text|json|jsonl|csv` renderers, cursor sentinel handoff, `--fields` projection, and a `format_version: 1` field embedded in JSON/JSONL envelopes for future evolution.
- New `cursors` table: `(name PK, created_at_nano, last_id, updated_at)`.

### Added
- `sharedwatch events retry` subcommand: requeues all `failed` events back to `pending` so a follow-up `consume` can pick them up. Closes the engineering report's B10 gap (failed events had no recovery path).
- `sharedwatch digest list --status pending|read|archived` filter.
- `Makefile` targets: `ci`, `smoke`, `run`, `clean`, with `VERSION` overridable for build-time linking.
- `sharedwatch init` subcommand that materializes the data directory + DB and prints the resolved paths.
- `sharedwatch version` subcommand and `--version` global flag (versioned via `-ldflags -X main.Version=…`).
- `sharedwatch help` / `--help` with a full command table and examples.
- `--config` global flag now actually loads the file (the previous `config.Load` was dead code).
- `--watch-path`, `--db`, `--data-dir` global flag overrides (highest precedence over config file).
- `--log-format=text|json` and `--log-level=debug|info|warn|error` global flags.
- `--ignore <pattern>` global flag, repeatable and comma-aware, appended onto `cfg.IgnorePatterns`.
- `sharedwatch status --json` for machine-readable status output.
- `active_expires_in` derived field on `status` (shows the remaining active TTL).
- Snapshot-table pruning: latest 5 snapshots retained per source. Stops unbounded growth from per-tick inserts.
- Synthetic-event path validation (`test emit`): rejects empty / absolute / `..`-escaping relpaths.
- Run-time mutual exclusion via OS file lock (`<data_dir>/sharedwatch.lock`); concurrent `run` against the same DB now fails fast.
- Structured logs throughout the `run` loop (`log/slog`).
- New unit tests: `reconcile.RunNow` (first-pass + drift + snapshot-bounding), `consumer.ConsumePending` end-to-end, `renameKey` for sizes > 0x10FFFF, `validateRelPath` table-driven.

### Changed
- Default paths are now XDG-style: `$XDG_DATA_HOME/sharedwatch/{watch,queue.db}` (falls back to `$HOME/.local/share/sharedwatch/...`). Previously hardcoded to `/home/node/.openclaw/...`.
- `cfg.RetentionDays` is honored by `reconcile.RunNow` (previously hardcoded to 30 days regardless of config).
- `db.Adapter` interface extended with `MarkDigestRead`, `MarkDigestArchived`, `PruneOldProcessedEvents`, `PruneArchivedDigests`, `PruneOldSnapshots`, `RequeueFailedEvents`, `ListDigestsFiltered`. The pre-existing call sites referenced the first four through `Adapter` but they were only on `*Store`; the project did not compile.
- Reconcile cold-start emits `file.created` for every pre-existing file on the first reconcile (previously: silent baseline, forcing users to call `reconcile now` twice).
- Rename digest line shows the *relative* old path (was the absolute filesystem path; visually noisy and inconsistent with every other event line).
- SQLite open enables WAL + busy_timeout via PRAGMA so concurrent CLI calls work while `run` is active. (Previously: `migrate: database is locked (SQLITE_BUSY)` whenever a second process touched the DB.)
- `CONTRIBUTING.md` rewritten with ground rules, PR checklist, and OSS-generic framing.
- `README.md` rewritten with a 60-second Quickstart, full command table, global-flag reference, mental model, and operational notes.
- Nullable SQL columns (`old_path`, `mtime`, `content_hash`, `coalesced_into`) now scan via `sql.NullString` and bind as SQL `NULL` on insert. Previously `Scan` returned `"converting NULL to string is unsupported"` against rows with no `old_path`.
- `mode active` reports the *effective* deadline (after the `<=0 → ActiveTTL` fallback), not the requested value.
- `digest show <missing>` / `digest archive <missing>` return a clean `digest not found: <id>` instead of leaking `sql: no rows in result set` / silently succeeding.
- `digest list` prints a helpful "no digests yet" line when the table is empty.
- Unknown subcommands print `unknown subcommand: <x>` + usage and exit 2, instead of failing inside `app.New` with a misleading mkdir error.
- `version` / `help` / `--version` no longer touch the DB, so they work on a fresh checkout without writable data dirs.

### Fixed
- `renameKey` constructed via `string(rune(e.Size))` collapsed all sizes > 0x10FFFF (~1.1MB) into a single key (the Unicode replacement char). Switched to `strconv.FormatInt`.
- Removed dead `consumer.Summarize` (unreachable; `digest.RenderHumanSummary` is the live path).
- Removed orphan literal-brace directory created by an accidentally-quoted `mkdir -p "{cmd/...}"`.
- `StatusSnapshot` no longer emits `0001-01-01T00:00:00Z` for empty time fields (uses `*time.Time` + `omitempty`).

### Security / supply chain
- License: MIT (added).
- `internal/app` lock prevents concurrent `run` from corrupting state.
- Synthetic-event relpath validation closes the smallest path-traversal foothold (the `test emit` subcommand).
