# Repository map — sharedwatch

The where-is-what reference. Cross-checks the live tree at `/home/ubuntu/claw/workspace/sharedwatch/` as of 2026-05-23 (post-release-prep restructure).

## Contents
1. Top-level layout
2. The `src/` Go module
3. Package responsibilities
4. `docs/` — the team's shared documentation folder
5. `tickets/` — filed work
6. `src/docs/` — program-internal docs
7. Quick "where do I add X?" table

## 1. Top-level layout

```
/
├── README.md                              # thin orientation, points to src/README.md and docs/
├── LICENSE                                # MIT
├── manifest.yaml                          # release manifest template
├── .gitignore
├── .gitleaks.toml                         # secret-scan config (allowlists for test fixtures)
├── docs/                                  # team docs, design discussions, reports, specs
│   ├── README.md                          # docs index / manifest with descriptions
│   ├── TEAM.md                            # team contract, shared-folder policy
│   ├── design/                            # design discussions and evaluations
│   ├── reports/                           # engineering / PMM / branch reports
│   ├── specs/                             # original product specs
│   ├── agent/                             # agent-specific docs (system prompt, fit analysis)
│   └── dogfood/                           # test_dogfood.md + per-pass findings
├── src/                                   # the Go module (everything Go lives here)
│   ├── cmd/sharedwatch/main.go            # binary entry point
│   ├── internal/                          # all packages (see §2)
│   ├── docs/                              # program-internal docs (APPLICATION_FLOW, SCHEMA_CONTRACTS, ...)
│   ├── go.mod  go.sum  Makefile  config.yaml
│   ├── CHANGELOG.md  README.md  CONTRIBUTING.md  LICENSE
│   └── examples/
├── scripts/
│   ├── install.sh                         # public installer (GitHub releases + local-dev fallback)
│   ├── rebuild.sh                         # fmt + vet + test + build into src/.bin/
│   └── release.sh                         # cut a release (cross-compile + dist/manifest.yaml)
├── html/                                  # GitHub-Pages-style landing page
├── tickets/                               # filed work tickets + tasklists
│   ├── ticket-agent-event-access-*.md     # SW-AGENT-1 (landed)
│   ├── ticket-multi-folder-watching-*.md  # SW-AGENT-3 (landed in v0.8)
│   ├── ticket-content-diff-*.md           # SW-AGENT-15 / SW-AGENT-16 (open)
│   ├── tasklist_*.md                      # session worklogs
│   └── ...
└── Skills/                                # agent-skill packages
    ├── sharedwatch-client/
    ├── sharedwatch-client-future/
    └── sharedwatch-contributor/           # this one
```

The repo root doubles as the team's shared workspace. Discussion docs, reports, tickets, and tasklists all live there. The Go code lives in the `sharedwatch/` subdirectory.

## 2. The `sharedwatch/` Go project

```
sharedwatch/
├── README.md                  # user-facing intro and Quickstart
├── CHANGELOG.md               # release notes (Keep-a-Changelog style)
├── CONTRIBUTING.md            # ground rules + how to land a change
├── LICENSE                    # MIT
├── Makefile                   # build/test/smoke/ci targets
├── install.sh                 # one-shot installer
├── config.yaml                # example config
├── go.mod / go.sum            # one external dep: modernc.org/sqlite
├── PROJECT_STATUS.md          # rolling status
├── POLICY-2026-03-18.md       # supply-chain policy (snapshot date)
├── AUDIT-2026-03-18.md        # audit log (snapshot date)
├── tasklist-2026-03-18.md     # in-project work log
├── .env.build                 # GOPROXY/GOSUMDB pinning for builds
├── .github/workflows/ci.yml   # CI: gofmt, vet, test, build, smoke
├── cmd/sharedwatch/main.go    # the CLI entry point
├── docs/                      # design docs
│   ├── APPLICATION_FLOW.md    # the pipeline picture
│   ├── STATE_MODEL.md         # event/digest state transitions
│   ├── SCHEMA_CONTRACTS.md    # authoritative column inventory
│   ├── OPERATOR_GUIDE.md      # ops perspective
│   ├── JOHN_HANDOFF.md        # design intent ("why?")
│   ├── PMM_REPORT.md          # positioning (in-project copy)
│   ├── SUPPLY_CHAIN_POLICY.md
│   └── TEST_PLAN.md
├── examples/README.md
└── internal/                  # core packages — see §3
    ├── app/                   # orchestration (watcher + consumer + reconcile)
    ├── catalog/               # snapshot building, ignore/include patterns
    ├── config/                # Config struct, defaults, file/flag parsing
    ├── consumer/              # claim pending events → digest → mark processed
    ├── db/                    # SQLite adapter, migrations, all queries
    ├── digest/                # Digest types, prose summary renderer
    ├── events/                # Event types, status enums, coalesce logic
    ├── mode/                  # active/passive runtime state
    ├── output/                # text|json|jsonl|csv formatters
    ├── reconcile/             # drift-recovery loop
    └── watcher/               # snapshot + diff + rename + scan loop
```

## 3. Package responsibilities

### `internal/app/`
Lifecycle orchestration. Holds the long-running `run` process, the three goroutines (watcher tick, consumer tick, reconcile tick), the file lock that prevents two `run`s against the same data dir, and signal handling.

Key files:
- `app.go` — `New`, `Run`; wires Store + Watcher + Consumer + Reconciler.
- `lock.go` — file-lock acquire/release.
- `app_test.go`, `status_test.go` — orchestration tests.

### `internal/catalog/`
Snapshot building. `BuildSnapshot()` walks a directory, applies ignore/include patterns, optionally hashes files, returns a `Snapshot{TakenAt, Files: map[relPath]FileState}`.

Key files:
- `snapshot.go` — `BuildSnapshotWithOptions`, `SnapshotHash`, `hashFile`.
- `ignore.go` — `Ignored()`, `Included()` glob matching.

### `internal/config/`
Configuration. `Config` struct with all tunables. Defaults via `Default()` (XDG-aware paths). File parsing (`file.go`), flag parsing handled in `cmd/`. Policy (`policy.go`) wraps environment-derived security defaults.

### `internal/consumer/`
The pull-side. `ConsumePending(ctx, mode, batchSize)`:
1. `ClaimPendingEvents` (atomic flip pending → processing)
2. Build digest via `digest.RenderHumanSummary`
3. `InsertDigest`
4. `MarkEventsProcessed`

Never blocks the watcher; uses its own DB transaction.

### `internal/db/`
The storage adapter. SQLite via `modernc.org/sqlite` (CGO-free). WAL mode + `busy_timeout=5000` for multi-reader concurrency.

Key files:
- `db.go` — `Open`, `migrate`, base event/digest/snapshot CRUD.
- `schema.go` — migration helpers; live DDL introspection for `sharedwatch schema`.
- `adapter.go` — `Adapter` interface (one impl: `*Store`).
- `events_query.go` — `QueryEvents(filter)` for `events list`.
- `cursor.go` — named-cursor get/upsert/list/delete + encode/decode tokens.
- `raw.go` — `RawSQL(ctx, sql, allowWrite)` for the `sql` subcommand.
- `digest_state.go` — read/archive transitions.
- `health.go` — `PendingCount`, `FailedCount`, etc., for `status`.
- `retention.go` — retention-day pruning.
- `producer_payload_test.go` — payload + producer_id coverage.

### `internal/digest/`
- `digest.go` — `Digest` struct, status enums.
- `render.go` — `RenderHumanSummary(events) string`. Prose for humans.

### `internal/events/`
- `events.go` — `Event` struct, `Type` (file.created/...), `Status` (pending/...), `Source` (watcher/...).
- `coalesce.go` — `ShouldCoalesce`, `Coalesce`. The merge logic for the 5s window.

### `internal/mode/`
- `mode.go` — `Runtime` struct holding current mode, TTLs, last-run timestamps. Persisted to `runtime_state` table.

### `internal/output/`
- `format.go` — the text|json|jsonl|csv formatter shared by `events list` and `sql`.

### `internal/reconcile/`
- `reconcile.go` — `Service.RunNow(ctx)` — same diff logic as the watcher but framed as drift recovery. Cold-start cascade when DB is empty.

### `internal/watcher/`
- `watcher.go` — wrapper types.
- `snapshot.go` — re-exports `catalog.BuildSnapshot`.
- `diff.go` — `DiffSnapshots(old, new, source)` produces events.
- `rename.go` — `DetectRenames(events)` pairs delete+create by size+mtime.
- `service.go` — `Service.ScanAndQueue`, `EmitSynthetic`, `EmitSyntheticWithPayload`.

## 4. `docs/` — the team's shared documentation folder

All free-form team docs live under `docs/`. Start at `docs/README.md` for the indexed manifest with one-line descriptions.

- **`docs/TEAM.md`** — collaboration contract; roles; file naming conventions; shared-folder policy.
- **`docs/specs/shared-drive-watcher-spec.md`** — original product spec.
- **`docs/specs/note-to-sarah-about-watcher-spec.md`** — follow-up note on the spec.
- **`docs/reports/engineering-report-20260520.md`** — engineering status snapshot.
- **`docs/reports/pmm-report-20260520.md`** — positioning/marketing.
- **`docs/reports/branch-report-feature-future-20260523.md`** — ship report for v0.8.0.
- **`docs/agent/agent-fit-20260520.md`** — multi-agent fit analysis (gap inventory).
- **`docs/agent/agent-system-prompt-20260522.md`** — system prompt for AI agents using sharedwatch.
- **`docs/design/multi-agent-discussion-20260522.md`** — multi-agent feature discussion.
- **`docs/design/disclosure-attribution-discussion-20260522.md`** — progressive disclosure + attribution design.
- **`docs/design/multifolder-design-20260522.md`** — multi-folder design quality layer (over SW-AGENT-3).
- **`docs/design/content-storage-evaluation-20260523.md`** — diff vs blob storage evaluation for SW-AGENT-16.
- **`docs/design/design-questions-20260523.md`** — living Q&A log (Q1: git replacement? Q2: touch events vs content? Q3: auto-commit?).
- **`docs/design/future-features-20260523.md`** — 15 predicted future features ranked by usage × need × design-fit.
- **`docs/dogfood/test_dogfood.md`** — runnable dogfood scenarios for the v1 product.
- **`docs/dogfood/findings-20260523.md`** — per-scenario findings from the v0.8.0 dogfood pass.

## 5. `tickets/` — filed work

```
tickets/
├── ticket-agent-event-access-20260520_035113.md   # SW-AGENT-1: events query surface — LANDED
├── ticket-multi-folder-watching-20260520_101921.md # SW-AGENT-3: multi-folder — OPEN
├── tasklist_20260520_022339.md  # open-up tasklist (session 1)
├── tasklist_20260520_040435.md  # session 2
├── tasklist_20260520_073211.md  # session 3
└── ...
```

Tickets are filed-and-tracked work units. Tasklists are session worklogs (what was actually done in a session).

## 6. `sharedwatch/docs/`

Design and operations docs that live with the code:

| File | Audience | Purpose |
|---|---|---|
| `APPLICATION_FLOW.md` | contributors | pipeline diagram + walkthrough |
| `STATE_MODEL.md` | contributors | event + digest state machines |
| `SCHEMA_CONTRACTS.md` | contributors + agents | authoritative column inventory |
| `OPERATOR_GUIDE.md` | operators | running it in production |
| `JOHN_HANDOFF.md` | contributors | design intent ("why is it like this?") |
| `PMM_REPORT.md` | product | positioning |
| `SUPPLY_CHAIN_POLICY.md` | release | how we trust deps |
| `TEST_PLAN.md` | contributors | what's tested and how |

## 7. Quick "where do I add X?" table

| Adding... | Goes in... |
|---|---|
| A new event type | `internal/events/events.go` (enum) + diff/coalesce updates |
| A new CLI subcommand | `cmd/sharedwatch/main.go` + a new package or file under `internal/` |
| A new query | `internal/db/events_query.go` or `internal/db/<new>.go` |
| A new schema migration | `internal/db/db.go` `migrate()` (additive, idempotent) |
| A new output format | `internal/output/format.go` |
| A new ignore-pattern semantic | `internal/catalog/ignore.go` |
| A new test scenario | `*_test.go` next to the code being tested |
| A new design doc | top-level `sharedwatch-<topic>-YYYYMMDD_HHMMSS.md` |
| A new ticket | `tickets/ticket-<topic>-YYYYMMDD_HHMMSS.md` |
| A new session worklog | `tickets/tasklist_YYYYMMDD_HHMMSS.md` |
| A new agent-facing skill | `Skills/<skill-name>/SKILL.md` (+ references/, scripts/) |
| README-visible behavior change | update `sharedwatch/README.md` + `sharedwatch/CHANGELOG.md` |
