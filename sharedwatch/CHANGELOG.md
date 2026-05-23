# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added — actors registry + heartbeats (SW-AGENT-8)
- New `actors` table (idempotent migration): `(actor_id PK, label, actor_kind, focus, last_heartbeat, metadata_json)` + index on `last_heartbeat`. Schema visible via `sharedwatch schema --format json`.
- New top-level subcommand `sharedwatch actor heartbeat <actor-id> [--focus <glob>] [--kind <k>] [--label <l>] [--metadata <json>]`. Upserts the row: a thin heartbeat (just `actor_id`) refreshes `last_heartbeat` while preserving prior metadata; non-empty fields overwrite their slot. Cadence recommendation: ≤ 1/min per actor (cheap but additive).
- New `status --actors` (and `status --actors --json`) emits the live registry. Each row carries `stale=true` when `last_heartbeat < now - actor_ttl`. The `actors` array is only included when the flag is set, so existing `status --json` consumers are unaffected.
- New `Config.ActorTTL` (default 5 m), parseable from `config.yaml` under key `actor_ttl: 5m`. Drives both the staleness check and the retention prune.
- Reconcile loop now also calls `Store.PruneStaleActors(ctx, 2 * ActorTTL)` each pass — recently-stale actors stick around for one diagnostic cycle, then disappear.
- Soft-warn on actor_kind change between heartbeats (the most common identity-confusion smell). The heartbeat itself is never rejected; the warning is a slog `WARN` line.
- Tests: `internal/db/actors_test.go` (upsert idempotency, thin-heartbeat metadata preservation, list-ordering, prune, kind-change tolerance).

### Fixed — actor-aware coalesce (SW-AGENT-11)
- **Cross-actor events on the same path no longer silently merge.** The coalesce key is now `(rel_path, actor)` where `actor` is parsed from `payload_json.actor` via the new `events.ExtractActor` helper. Two different actors editing the same file inside the 5 s window produce two distinct events; same-actor events still coalesce; legacy empty-actor events still coalesce with other empty-actor events. Fixes the silent attribution loss exposed by dogfood scenario 17 (`test_dogfood.md`).
- New `Store.FindRecentPendingByRelPathAndActor(ctx, relPath, actor, since)` returns the most recent pending event on `relPath` whose actor matches. Implementation fetches a bounded candidate window (LIMIT 16) and filters actor in Go via `events.ExtractActor` — avoids any dependency on SQLite's JSON1 extension for a hot-path query. `Store.FindRecentPendingByRelPath` is retained for callers that don't need actor scoping.
- `events.ShouldCoalesce` documents and enforces the actor-equality contract.
- Regression test `TestCoalesceScopedToActor` covers six cases: same-actor-merge, cross-actor-distinct, empty+empty-merge, empty+named-distinct, outside-window-distinct, three-actors-interleaved.

### Added — agent-fit follow-up (SW-AGENT-7 — payload v1 attribution)
- **`payload_json` v1 schema + helpers (new `internal/events/payload.go`):** canonical `PayloadV1` struct with fields `actor`, `actor_kind`, `session`, `task`, `intent`, `addressee`, `ref_event_id`, `tags`. `BuildPayloadV1` always sets `schema_version: 1`, omits empty optional fields, and returns `""` for an empty struct so the historical "no payload" behaviour is preserved when attribution isn't requested. Companion `ExtractActor(payloadJSON)` returns the actor field tolerantly (unknown keys ignored, forward-version payloads still readable).
- **First-class attribution flags:** `--actor`, `--actor-kind`, `--session`, `--task`, `--intent`, `--addressee`, `--ref` (sets `ref_event_id`), `--tag` (repeatable, comma-aware). Available on both the root flagset and on `test emit`; subcommand values override root values per-field, non-empty wins.
- **Mutual exclusion:** `--payload '<raw-json>'` and the attribution flags refuse to be mixed on `test emit`; the user picks one form.
- **Watcher + reconciler propagation:** when any attribution flag is set on the root flagset, every event emitted by the watcher diff loop AND the reconciler's drift-recovery / cold-start path stamps the v1 payload onto events that don't already carry one. Plumbed via new `Config.PayloadJSON` → `watcher.Service.PayloadJSON` / `reconcile.Service.PayloadJSON`. Empty `PayloadJSON` is strictly legacy behaviour (existing single-tenant callers unaffected).
- **Tests:** `internal/events/payload_test.go` (build/empty/round-trip/schema-version-coercion/ExtractActor table). `internal/app/attribution_test.go` (watcher path, reconciler cold-start path, empty-attribution legacy-behaviour preservation).

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
