# Engineering conventions

The "how we write code here" reference. Pulled from `CONTRIBUTING.md`, `JOHN_HANDOFF.md`, and observed patterns across the codebase.

## Contents
1. Language and tooling
2. Error handling
3. Tests
4. Comments
5. Dependencies
6. Schema migrations
7. Output contracts
8. Naming
9. Concurrency
10. Logging
11. CI gates

## 1. Language and tooling

- **Go 1.22+.** Some code uses `for i, v := range slice` and the post-1.22 loop-variable semantics; don't relax.
- **`gofmt -w .`** before every commit. CI rejects unformatted code (`gofmt -l .` is the gate).
- **`go vet ./...`** must pass.
- **`go test ./... -count=1 -race`** must pass — note `-race` is on in CI; data races fail the build.
- Build flags: `go build -ldflags "-X main.Version=<sha>" -o .bin/sharedwatch ./cmd/sharedwatch`.

## 2. Error handling

- Errors are **returned**, never panicked. Exceptions:
  - In `cmd/sharedwatch/main.go` you may `os.Exit(N)` after logging.
  - In test code, `t.Fatal(err)` is fine.
- Wrap with context: `fmt.Errorf("read sqlite_master: %w", err)`. Keep the verb consistent — "open", "read", "scan", "insert", "update", "migrate", etc.
- Sentinel errors for caller-distinguishable cases: `ErrDigestNotFound`, `ErrInvalidRelPath`. Compare with `errors.Is`.
- Never swallow errors silently. If you genuinely don't care, `_ = ...` with a one-line comment explaining why.

## 3. Tests

- New code paths get at least one unit test.
- Integration tests use `t.TempDir()` for ephemeral DBs.
- Test files are colocated: `internal/db/db_test.go`, `internal/db/cursor_test.go`, etc.
- Table-driven tests are the norm; see `internal/watcher/diff_test.go` for the canonical pattern.
- Don't mock the DB — use a real SQLite via `db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))`. Mocked DB tests have burned us before (per project memory).
- Race detector must pass: `go test ./... -race`. If you introduce a goroutine, add a test that exercises concurrency.

## 4. Comments

Default: **no comments.** Add one only when WHY is non-obvious:
- a hidden constraint
- a subtle invariant
- a workaround for a specific bug (link the bug)
- behavior that would surprise a reader

Don't:
- explain WHAT the code does (well-named identifiers do that)
- reference the current task or fix or callers ("used by X", "added for the Y flow")
- write multi-paragraph docstrings
- add a comment to every exported function

If removing the comment wouldn't confuse a future reader, don't write it.

## 5. Dependencies

- **One external import: `modernc.org/sqlite`** (CGO-free SQLite). Adding a second dep is a deliberate discussion, not a unilateral choice.
- Standard library first: `database/sql`, `encoding/json`, `path/filepath`, `time`, `os`, `context`, etc.
- For tests, the stdlib `testing` package is sufficient; no testing frameworks.
- Internal packages may freely depend on each other but should avoid cycles (`go vet` catches).

## 6. Schema migrations

- Migrations live in `internal/db/db.go`'s `migrate()` function.
- Use `CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS`. Always.
- For column adds, use the `addColumnIfMissing` helper — checks `PRAGMA table_info` first.
- **Never `DROP COLUMN`.** Sqlite supports it but the operational risk on a live journal isn't worth it.
- **Never rename a column.** Add a new one and migrate data in code if needed.
- Add tests for migration paths: open a pre-migration DB, re-open with the new binary, assert the column exists and old rows have the expected default.

## 7. Output contracts

- `--format json` and `--format jsonl` are **contracts**. Once a field is published, never rename or remove it.
- Adding fields is always safe (JSON consumers should tolerate unknown keys).
- `--format text` is **not** a contract. Rendering can change. If an agent depends on parsing text output, the agent is wrong — point them at JSON.
- Output envelopes should include `schema_version` going forward for future-proofing.

## 8. Naming

- Packages: short, lower-case, single word where possible (`db`, `watcher`, `events`).
- Exported types: PascalCase (`Event`, `Store`, `Config`).
- Exported functions/methods: PascalCase (`InsertEvent`, `ClaimPendingEvents`).
- Unexported: camelCase.
- File names: lower-case, underscore-separated (`events_query.go`, `producer_payload_test.go`).
- Test names: `TestXxx` for tests; `BenchmarkXxx` for benchmarks (if any).

## 9. Concurrency

- The orchestrator in `internal/app/` runs three goroutines: watcher, consumer, reconcile. Each owns its own ticker.
- The DB is the synchronization point. WAL mode + `busy_timeout=5000` handles contention.
- Don't add cross-goroutine channels for "fast path" notifications between watcher and consumer — that violates the queue-first invariant.
- If you need to coordinate, do it through the DB (a row in `runtime_state` or a status flip on an event).
- `ctx.Done()` is the universal cancellation signal; respect it in every loop.

## 10. Logging

- Use `log/slog` (stdlib structured logging).
- Levels: `debug` for trace, `info` for operational milestones, `warn` for degraded behavior, `error` for things the operator needs to see.
- `--log-format text` (human) or `json` (aggregators).
- Don't log inside tight loops without throttling.
- Don't log secrets or full file contents.

## 11. CI gates

CI (`.github/workflows/ci.yml`) runs:
1. `gofmt -l .` — must be empty
2. `go vet ./...`
3. `go test ./... -count=1 -race`
4. `go build -ldflags "-X main.Version=<sha>" -o .bin/sharedwatch ./cmd/sharedwatch`
5. Smoke: `init` → `test emit` → `consume` → `digest list` against a temp dir

Local equivalent: `make ci` (fmt+vet+test+build) and `make smoke`.

## Gotchas observed across the codebase

- **`payload_json` is `'{}'` not `null`.** The migration default is the literal `'{}'`. Don't write code that expects `NULL`.
- **`producer_id` was added by a later migration.** Code reading old DBs should expect `''` for legacy rows.
- **Cursor `created_at` is stored as RFC3339Nano string**, not unix nanos. Comparison must use string-sort semantics (UTC RFC3339Nano sorts the same as time order — but only for UTC).
- **`PRAGMA table_info(...)` cannot be parameterized.** Direct interpolation is used in `tableColumns` and `addColumnIfMissing`, controlled because the input comes from `sqlite_master`. Don't generalize without thinking about injection.

## When in doubt

- Read `JOHN_HANDOFF.md` for design intent.
- Read the closest test file for examples of patterns in use.
- Ask Mike (or open a discussion artifact) before introducing a new dependency, new external service, new public output field, or new invariant.
