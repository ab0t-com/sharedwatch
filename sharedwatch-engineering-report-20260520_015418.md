# sharedwatch — Engineering Structural Mapping Report

Date: 2026-05-20 (UTC)
Source ingested: `/home/ubuntu/claw/backup/workspace_4/team/shared/workspace/sharedwatch/` (57 files)
Audience: engineers evaluating open-sourcing or extending the project.
Stance: independent re-read of source + docs. Does not trust the project's self-claimed status.

---

## 1. One-line summary
A Go single-binary CLI that polls a shared folder, normalizes file events into a SQLite-backed durable queue, and produces pull-based "digests" so a human/agent reviews changes on their own cadence instead of being interrupted.

## 2. Repository layout (canonical, ignoring artifacts)

```
sharedwatch/
├── cmd/sharedwatch/main.go         # CLI entrypoint; argv-switch dispatch
├── go.mod                          # module sharedwatch; go 1.22; modernc.org/sqlite v1.34.5
├── Makefile                        # install / build / test / fmt
├── install.sh                      # gofmt → go mod tidy → go test → go build
├── config.yaml                     # operational defaults (NOTE: not loaded at runtime)
├── .env.build                      # GOPROXY / GOSUMDB allowlist for module fetch
├── README.md, CONTRIBUTING.md, PROJECT_STATUS.md
├── AUDIT-2026-03-18.md             # Sarah's prior self-audit (highly relevant)
├── POLICY-2026-03-18.md            # supply-chain policy decision record
├── tasklist-2026-03-18.md          # running work log
├── examples/README.md              # CLI example session
├── docs/
│   ├── APPLICATION_FLOW.md
│   ├── STATE_MODEL.md
│   ├── SCHEMA_CONTRACTS.md
│   ├── OPERATOR_GUIDE.md
│   ├── PMM_REPORT.md
│   ├── TEST_PLAN.md
│   ├── JOHN_HANDOFF.md
│   └── SUPPLY_CHAIN_POLICY.md
└── internal/
    ├── app/         orchestration (App struct; Run/Status/SetActive/...)
    ├── config/      Config struct, Default(), Load() (file parser, untested wiring), policy.go
    ├── consumer/    claim → render summary → persist digest → mark events processed
    ├── db/          SQLite Store, Adapter interface, Health, retention, digest_state
    ├── digest/      Digest type + RenderHumanSummary
    ├── events/      Event/Status/Type/Source constants; Coalesce / ShouldCoalesce
    ├── mode/        Runtime struct (mode, ActiveUntil, last-run timestamps); EffectiveMode
    ├── reconcile/   periodic snapshot-diff drift recovery
    ├── watcher/     polling snapshot pipeline (snapshot, diff, rename, scan-and-queue, EmitSynthetic)
    └── catalog/     Snapshot type + BuildSnapshot + Ignored matcher
```

**Stray artifact:** there is also a literal-brace directory
`sharedwatch/{cmd/sharedwatch,internal/{app,config,db,digest,events,mode,reconcile,watcher},docs}`
(empty). Created by a quoted `mkdir -p "{...}"` that suppressed brace expansion. Harmless to the build; should be `rm -rf`'d before any external release.

## 3. Component map

| Package | Type | Public surface | Inbound deps | Outbound deps |
|---|---|---|---|---|
| `cmd/sharedwatch` | `main` | argv dispatch | — | `internal/app`, `internal/config` |
| `internal/app` | orchestration | `App`, `New`, `Run`, `Status`, `SetActive/Passive`, `ConsumeNow`, `TestEmit`, `ListDigests`, `GetDigest`, `Close` | `cmd/sharedwatch` | all other internal pkgs |
| `internal/config` | data | `Config`, `Default`, `Load`, `SupplyChainPolicy` | `app`, `cmd` | stdlib only |
| `internal/db` | storage | `Adapter` interface, `Store`, `Open`, `OpenAdapter`, `Health`, retention helpers | `app`, `consumer`, `reconcile`, `watcher` | `modernc.org/sqlite`, internal `catalog/digest/events/mode` |
| `internal/events` | data + logic | `Event`, type/status/source constants, `Coalesce`, `ShouldCoalesce` | most pkgs | stdlib |
| `internal/digest` | data + logic | `Digest`, `Status`, `RenderHumanSummary` | `app`, `consumer`, `db` | `events` |
| `internal/mode` | data + logic | `Runtime`, `EffectiveMode`, `DefaultRuntime`, mode constants | `app`, `db`, `reconcile` | stdlib |
| `internal/watcher` | pipeline | `Service` (`ScanAndQueue`, `EmitSynthetic`), `DiffSnapshots`, `DetectRenames`, `BuildSnapshot` alias | `app`, `reconcile` | `db`, `events`, `catalog` |
| `internal/catalog` | pipeline | `Snapshot`, `FileState`, `BuildSnapshot`, `SnapshotHash`, `Ignored` | `watcher`, `reconcile`, `db` | stdlib |
| `internal/consumer` | pipeline | `Service` (`ConsumePending`), `Summarize` (unused) | `app` | `db`, `digest`, `events` |
| `internal/reconcile` | pipeline | `Service` (`RunNow`) | `app` | `catalog`, `db`, `events`, `mode`, `watcher` |

Dependency direction is clean: `cmd → app → (pipelines) → (data + storage)`. No internal import cycles.

## 4. Data model (as actually present in code)

### `events` table (DDL in `db.go:47`)
`id, type, path, rel_path, old_path, source, status, retry_count, created_at, observed_at, processed_at, file_size, mtime, content_hash, coalesced_into, payload_json`

Indexes: `(status, created_at)`, `(rel_path, status, created_at)` — appropriate for the claim path and the coalesce lookup.

**Behavioural quirk:** `InsertEvent` writes `created_at` and `observed_at` from the same `e.Timestamp` — they are always equal. There is no separation between "when the watcher saw it" and "when it landed in the DB". `processed_at` is set when `MarkEventsProcessed` runs.

### `digests` table
`id, created_at, window_start, window_end, mode, event_count, summary_text, status`
Index: `(status, created_at)`.

### `runtime_state` table
Single-row, key=`'runtime'`, value=JSON blob serialized from `mode.Runtime`. Upsert via `ON CONFLICT(key) DO UPDATE`.

### `snapshots` table
`id, created_at, source, snapshot_json`. **Unbounded growth** — there is no pruning of historical snapshots, only the latest per source is ever read (`LatestSnapshot`). Every watcher tick (≈ every 2s) and every reconcile (≈ every 30m) inserts a new row. At default cadence this is ~43k rows/day from the watcher alone.

## 5. Data flow

### Normal path (passive mode, default)
```
filesystem → watcher.ScanAndQueue (every 2s, hardcoded)
              ├── catalog.BuildSnapshot (walks WatchPath, applies ignore patterns)
              ├── db.LatestSnapshot(source="watcher")
              ├── DiffSnapshots(prev, curr) → []Event{created|modified|deleted}
              ├── DetectRenames(...)  ⇒ collapses delete+create with identical size+mtime
              ├── for each event: db.InsertOrCoalesceEvent
              │       └── FindRecentPendingByRelPath → ShouldCoalesce → Coalesce | Insert
              └── db.SaveSnapshot(source="watcher")

consumer ticker (every ActiveInterval=5s, hardcoded clock; gated by shouldConsume)
       └── app.shouldConsume(): if active → yes; else compare LastConsumerRun vs PassiveInterval
                                (uses 5m if any event in last 30m, else 10m)
       └── consumer.ConsumePending(mode, MaxBatchSize=100)
              ├── db.ClaimPendingEvents (tx: SELECT pending → UPDATE status='processing')
              ├── digest.RenderHumanSummary(events)
              ├── db.InsertDigest(status='pending')
              └── db.MarkEventsProcessed(ids)  (on success)
                  db.MarkEventsFailed(ids)     (on digest insert failure)
       └── app updates rt.LastConsumerRun, and if active → extends rt.ActiveUntil
```

### Reconcile path (every 30m + on startup)
```
reconcile.RunNow
   ├── catalog.BuildSnapshot
   ├── db.LatestSnapshot(source="reconciler")
   ├── DetectRenames(DiffSnapshots(prev, curr, source=reconciler))
   ├── for each event: db.InsertOrCoalesceEvent
   ├── db.SaveSnapshot(source="reconciler")
   ├── update rt.LastReconcileRun + rt.LastSnapshotHash
   ├── db.PruneOldProcessedEvents(30*24h)  ← hardcoded; ignores cfg.RetentionDays
   └── db.PruneArchivedDigests(30*24h)     ← same
```

### Manual / CLI paths
- `sharedwatch run` — starts the long-lived loop: kicks an initial scan + reconcile, then `select`s on three tickers (watch 2s / consumer ActiveInterval=5s / reconcile ReconcileInterval=30m).
- `sharedwatch status` — `Health()` + runtime snapshot, formatted as single line.
- `sharedwatch mode active --ttl <dur>` / `mode passive` — flip `rt.Mode` and `rt.ActiveUntil`.
- `sharedwatch digest list|show|archive` — read/update the digests table; `show` auto-marks `read`.
- `sharedwatch reconcile now` — invokes `reconcile.RunNow`.
- `sharedwatch test emit [relpath]` — `Watcher.EmitSynthetic(..., source=test)`; injects a fake event for end-to-end smoke.
- `sharedwatch consume` — one-shot `ConsumeNow`.

## 6. What the system is for (use cases)

**Designed for:** a calm, single-host shared-folder collaboration bus where a human (or an agent acting for the human) wants:
- to know *that* something changed in a shared dir,
- but be told on their own cadence (5–10m),
- with a temporary fast-review mode for live back-and-forth,
- and a safety-net reconcile to catch drift after restarts or missed events.

**Out-of-scope (explicit non-goals in spec):** full content diff, multi-user auth, networked queue, browser UI, semantic clustering, push notifications to a chat stream.

**Concrete situations it fits:**
- a single dev box where two assistants share a workspace and want non-interrupting status awareness;
- a directory used as a low-friction "drop folder" between collaborators on the same machine;
- any scripted producer where you want a normalized, queryable history of file mutations.

**Situations where it does not fit (in current form):**
- multi-host / distributed file shares (no leader election, no remote queue);
- high-volume writes (polling + per-event row + snapshot-per-tick is O(N·ticks));
- security-sensitive workflows (no auth, no path sandboxing beyond `WatchPath`);
- consumers that want push notification (it is exclusively pull).

## 7. Current state — verified against source

### What is implemented and correct
- SQLite schema and migration; transactional `ClaimPendingEvents` with `pending → processing` flip in one tx.
- Polling snapshot diff producing create/modify/delete events.
- Heuristic rename pairing (same size + same mtime) inside the diff output.
- Coalescing: same-relpath, pending status, within window — collapses `modified→modified` and `created→modified` (created wins type).
- Active/passive runtime with TTL persisted to DB; passive cadence dynamically tightens to 5m if any event in the last 30m.
- Active TTL auto-extension on each successful `ConsumeNow` while active.
- Reconcile-driven retention pruning (hardcoded 30d).
- Digest lifecycle pending → read → archived (CLI `show` flips read, `archive` flips archived).
- Health surface in `status`: pending, failed, total/unread/archived digests, last-event/consumer/reconcile timestamps.
- `Adapter` interface boundary for future non-SQLite backends.
- Unit tests for: coalesce, rename detection, diff, ignore, snapshot ignore, mode effective, db claim/digest flow, health, render, config Load, consumer summarize, app active TTL, app status formatting.

### What is broken or fragile (engineering bugs)

**B1 — Will not compile as written.**
`internal/reconcile/reconcile.go:59-60` calls `s.Store.PruneOldProcessedEvents(...)` / `PruneArchivedDigests(...)` and `cmd/sharedwatch/main.go:102,108` calls `a.Store.MarkDigestRead/Archived(...)`. All four methods are defined on `*db.Store` only and **not** on the `db.Adapter` interface. Since `app.App.Store` and `reconcile.Service.Store` are typed as `db.Adapter`, the Go compiler will reject the references. Either (a) add these four methods to `Adapter`, or (b) type-assert/use a `*Store` field on the call sites. The audit (2026-03-18) does not flag this, suggesting it was introduced after the audit when the Adapter interface was added.

**B2 — `config.yaml` is dead.**
`config.Load(path, base)` exists and is tested (`file_test.go`), but `cmd/sharedwatch/main.go:20` calls `config.Default()` and never invokes `Load`. `config.yaml` and `.env.build` describe behaviour the binary cannot exhibit.

**B3 — `RetentionDays` is unused.**
`Config.RetentionDays` is parsed and defaulted, but `reconcile.go:58` hardcodes `30 * 24 * time.Hour`. Changing config has no effect.

**B4 — Snapshots table grows without bound.**
`watcher.ScanAndQueue` inserts a new snapshot row every tick (default 2s). `reconcile.RunNow` adds one every 30m. Only the most recent per `source` is ever read. No pruning. At default cadence: ≈ 43,200 rows/day from the watcher alone.

**B5 — `renameKey` is fragile.**
`rename.go:44`: `string(rune(e.Size)) + "|" + e.MTime...`. Converting an `int64` size through `rune()` is meaningful only up to `0x10FFFF` (≈1.1MB); above that the rune is `�` (replacement), collapsing all large files of any size into a single key. Even below the limit it is unidiomatic — `strconv.FormatInt(e.Size, 10)` is the right tool. Practical impact: rename heuristic mis-pairs large files when multiple are renamed in the same scan.

**B6 — Watcher ticker is hardcoded.**
`app.go:119`: `time.NewTicker(2 * time.Second)`. There is no `WatchInterval` config knob and no respect for `ActiveInterval` / `PassiveInterval` for scanning. Spec asked for cadence to be configurable.

**B7 — Two summary implementations.**
`consumer.Summarize` exists (tested), but `consumer.ConsumePending` calls `digest.RenderHumanSummary` instead. Pick one; the unused path will rot.

**B8 — `Run()` swallows errors as stdout prints.**
`fmt.Println("watch scan error:", err)` etc. Spec called for structured logs. No `log/slog` usage.

**B9 — `InsertEvent` collapses `created_at` and `observed_at`.**
Both written from `e.Timestamp`. The schema makes the distinction but the code doesn't use it.

**B10 — Coalesce window doesn't survive failed digest insert.**
On `InsertDigest` failure, events are marked `failed` (not returned to `pending`). There is no retry path that re-queues them — `failed` events just accumulate in `Health.FailedEvents`.

**B11 — `nullableTime` returns Go `any` for SQL bind.**
Works in practice with `database/sql` but loose. `sql.NullString` is the idiomatic shape.

### Stubs / not-implemented surface
- **No real fsnotify integration.** All "watching" is polling. Documented as an intentional v1 tradeoff (`PROJECT_STATUS.md`, `OPERATOR_GUIDE.md`).
- **No JSONL or alternate adapter.** `OpenAdapter` switches only on `"sqlite"` / `""`; any other value errors. The interface exists, but there is no second implementation to validate the abstraction.
- **No `--config` flag.** Even if `Load` were wired, there is no path to point it at a non-default location.
- **No version flag** (`sharedwatch --version` / `version`).
- **No `--help` beyond the bare `usage()` text.**
- **No log file or `logs/` directory** despite the spec's storage layout suggestion.
- **`payload_json` is reserved but mostly unused.** Only `EmitSynthetic` populates it; no consumer reads it.
- **`Hash` field on `FileState`** is declared and written to JSON, but `BuildSnapshot` never computes it. `changed()` compares it (always empty == empty). Spec says "optional content hash"; it's just absent.
- **`coalesced_into`** column is selected and written but never set to a non-nil value anywhere.
- **`Summarize` in consumer.go** is dead (see B7).
- **No CI config** (`.github/`, `.gitlab-ci.yml`, etc.).
- **No `LICENSE` file** — a blocker for open-sourcing.
- **No `CHANGELOG.md`.**
- **No example `config.yaml` documentation in README** beyond the one-liner.

### Test coverage map
| Package | Tests present | Notable gaps |
|---|---|---|
| app | active-TTL extension; status formatting | no `Run()` loop test; no `shouldConsume` cadence test |
| config | `Load` of basic keys | no error/malformed-line test; no empty-file test |
| consumer | `Summarize` (the **unused** path) | `ConsumePending` end-to-end has no test |
| db | event insert/claim/digest insert/get; health pending+unread | no coalesce-through-store test; no retention test; no snapshot-table growth test |
| events | coalesce modify-burst; create-then-modify keeps create | no delete/rename coalesce behaviour test (currently: doesn't coalesce — fine, untested) |
| mode | EffectiveMode active vs expired | — |
| watcher | DiffSnapshots; DetectRenames basic happy path | no rename with conflicting size; no large-size renameKey edge; no missing-dir behaviour |
| catalog | Ignored matcher; snapshot-ignore tmp pattern | no recursive=false test; no symlink/permission-denied test |
| digest | renamed-event render with old path | no empty-events case |
| reconcile | none | service has zero unit coverage |

`reconcile` and `consumer.ConsumePending` having no unit coverage is the most concerning gap — those are the two paths the spec calls out as critical.

## 8. Interface assessment

### CLI surface — is it good?
Largely yes for v1. Verb–noun shape is clean, matches the spec's suggestions, and operator commands map 1:1 to documented mental model. Specific issues:

- **No `--config` / `--db` / `--watch-path` overrides.** Everything baked in via `Default()`. For OSS, this is the single most painful UX gap.
- **No `--json` output.** `status` returns a flat `k=v` string; not script-friendly. PMM/observability tools cannot consume it.
- **No top-level `--help` or per-subcommand help.** `usage()` is a single `Println`.
- **No version flag.** Standard expectation.
- **`mode active` accepts `--ttl` but `mode passive` does not (correctly) take args** — consistent but should be documented.
- **`digest list` returns last 20 by default**, no pagination, no filter (`--status=pending` etc.).
- **`test emit` is a footgun**: there is no guard that it should only run in dev. Anyone with the binary can inject synthetic events into a production DB.
- **Exit codes are coarse.** `fatal()` always exits 1. No distinct codes for "no events" vs "DB error".

### Storage adapter interface — is it good?
- Right idea, premature shape. The interface mixes high-level domain ops (`InsertOrCoalesceEvent`) with low-level CRUD (`InsertDigest`, `MarkEventsProcessed`). If a second adapter is ever added, every method needs implementing — including ones the alternate backend might not naturally support.
- It omits the methods callers actually use (B1) — so it doesn't even fully encapsulate the SQLite specifics it was meant to abstract.
- No `Begin/Commit` exposure: if a future adapter needs cross-call atomicity (e.g. claim+digest insert as one tx), there is no place to add it.

### Library/API surface
`internal/` is the convention — nothing is exported for embedding. To open-source as a library, several packages (events, digest, catalog, watcher, db) would need to move under `pkg/` (or out of `internal/`) and have their exported types stabilized.

### Schema interface
The published `docs/SCHEMA_CONTRACTS.md` matches what `db.go` actually creates. No drift between docs and DDL. Good.

## 9. Risk matrix for opening this up

| Risk | Severity | Notes |
|---|---|---|
| Will not compile (B1) | **Blocker** | Must fix before any tag/release. |
| No LICENSE | **Blocker** | Cannot OSS without it. |
| `config.yaml` is dead (B2) | High | Visible in docs; users will be confused when changes don't take effect. |
| Snapshots table unbounded (B4) | High | Disk-fills on long-running deploys. |
| Polling at 2s ignores config (B6) | Medium | Battery / CPU on idle hosts; not what the spec said. |
| Rename heuristic fragile (B5) | Medium | Subtle correctness bug at scale. |
| No structured logs (B8) | Medium | Operator observability is what the spec wanted. |
| `test emit` exposed in prod binary | Medium | Data integrity / audit. |
| No CI / no automated verification | Medium | Sarah's audit confirms tests were never run successfully in this env. |
| No fsnotify | Low | Documented as v1 tradeoff. |
| Adapter interface mis-shaped | Low | Until a second backend exists, it's vestigial. |

## 10. Use-value (engineering lens)

**What is the project worth as a code asset, today?**
- ~1,400 LOC of idiomatic Go, broken into well-named packages with clean dependency direction.
- A complete schema design and durable-queue pattern that is genuinely reusable.
- Solid coalesce + diff + ignore primitives that would survive a rewrite of the CLI.
- The PMM-style design discipline (queue-first, pull-not-push, durable state) is the real intellectual asset — it constrains future contributors away from the "interrupt the user" failure mode.

**What it is not:**
- A drop-in production tool. B1 alone blocks `go build`. B4 alone blocks 24-hour uptime.
- A general-purpose file sync / watch framework. It is scoped tightly to "one folder, one DB, one host."

**Estimated work to reach external-release quality:** 3–5 focused engineer-days.
- 0.5d: fix B1 (Adapter interface) and re-run tests.
- 0.5d: wire `config.Load` + `--config` flag (B2, B3).
- 0.5d: snapshot pruning + watcher cadence config (B4, B6).
- 0.5d: structured logs + JSON `--json` flag for `status` (B8).
- 0.5d: fix `renameKey` and add tests (B5).
- 0.5d: LICENSE, CHANGELOG, version flag, CI workflow (Linux only is fine for v1).
- 1d: real `go build` + `go test ./...` + manual smoke on a Linux host with the SQLite module fetch unblocked; resolve the original `tls: failed to verify certificate` cited in `AUDIT-2026-03-18.md`.

## 11. LLM-as-judge rubric for the framework

Score each dimension 0–5. Includes pass criteria for "ready to open-source" (3+ on every dimension) and "production-ready" (4+ on every dimension).

| # | Dimension | What to inspect | 0 | 3 | 5 | Current |
|---|---|---|---|---|---|---|
| R1 | **Correctness & buildability** | `go build` succeeds; `go test ./...` green | doesn't build | builds + tests pass; minor known bugs | builds + tests + lint + race detector all green | **1** (B1 breaks build) |
| R2 | **Design fidelity to spec** | queue-first; pull-based; durable; active+passive; reconcile | core principle violated | matches spec | exceeds spec with documented rationale | **4** |
| R3 | **Code readability** | naming, package boundaries, function length | spaghetti | follows Go idioms; small files | exemplary; doc comments on every export | **4** |
| R4 | **Test coverage of critical paths** | consumer, reconcile, coalesce, claim, mode | <30% | core happy paths covered | edge cases + property tests | **2** (consumer/reconcile not tested) |
| R5 | **Observability** | logs, metrics, status command, error context | none | basic status + stdout | structured logs + Prometheus/OTel | **2** |
| R6 | **Configurability** | flags, config file, env vars, defaults | hardcoded | file or flags supported | file + flags + env + validation | **1** (config file unused) |
| R7 | **Documentation** | README, operator guide, schema contract, why-doc | none | sufficient for new operator | excellent + diagrams + ADRs | **4** |
| R8 | **Operational safety** | retention, growth bounds, restart safety, idempotency | unsafe | safe but coarse | tunable, observable, audited | **2** (B4 unbounded snapshots) |
| R9 | **Extensibility / interface quality** | adapter, plugin points, exported API | monolithic | abstractions exist | second implementation proves abstraction | **2** (Adapter mis-shaped, no 2nd impl) |
| R10 | **OSS-readiness** | LICENSE, CHANGELOG, CONTRIBUTING, CI, version flag | nothing | CONTRIBUTING + LICENSE | + CI + release process | **1** (no LICENSE/CI/version) |
| R11 | **Security posture** | input validation, path traversal, untrusted producers, secrets | unsafe | basic; runs as user | sandboxed, audited, threat-modeled | **2** (no path constraints, `test emit` exposed) |
| R12 | **Failure handling** | retry, dead-letter, transactional integrity, error wrapping | swallows | wrapped errors + status | retries + DLQ + observability | **2** (failed events have no retry path) |

**Aggregate:** 27 / 60.
- Ready-to-open-source threshold (all ≥3): **not met** — fails R1, R4, R5, R6, R8, R9, R10, R11, R12.
- Production-ready threshold (all ≥4): **not met** — only R2 and R3 reach 4.

**Verdict for "should we open this up":** the design and docs are open-source-worthy; the code is not yet. The shortest path to "yes" is the 3–5 day plan in §10 plus a LICENSE choice.

## 12. Recommended order of operations before public release
1. Fix B1 (Adapter completeness) — unblocks everything else.
2. Wire `config.Load` + `--config` flag, honour `RetentionDays`.
3. Bound the `snapshots` table (cap at N per source or prune by age).
4. Replace `fmt.Println` with `log/slog`; add `--log-format=json`.
5. Rewrite `renameKey` to `fmt.Sprintf("%d|%s", size, mtime)`.
6. Add LICENSE, CHANGELOG, `--version`, GitHub Actions CI on Linux.
7. Move stable types out of `internal/` if library use is a goal.
8. Add unit tests for `reconcile.RunNow` and `consumer.ConsumePending`.
9. Either delete `consumer.Summarize` (and its test) or switch the consumer to use it.
10. Delete the orphan `{cmd/sharedwatch,internal/{...},docs}` directory.
