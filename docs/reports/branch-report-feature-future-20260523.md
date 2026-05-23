# `feature/future` — branch report

**Branch:** `feature/future` (cut from `master` after `53279ad`)
**Date:** 2026-05-23
**Status:** code complete, all CI gates green, full 18-scenario dogfood pass executed; ready for review + merge to `main`
**Estimated work shipped:** ~9 focused engineer-days, landed in one continuous session
**Test coverage delta:** +49 new tests across the affected packages

This document explains *why* this branch exists, *what* it ships, *how* it was built, and *what's next*. It is the durable artefact reviewers and future contributors should read first.

---

## 1. The executive summary (read first)

sharedwatch shipped v0.7.x as a single-folder, single-tenant, calm activity journal — useful for one human watching one folder, useless for the actual business problem the team wanted to solve: **multiple AI agents working on the same folder system without knowing what each other is doing.**

This branch closes that gap. It ships v0.8.0, which turns sharedwatch into a real multi-agent coordination substrate.

Four sub-problems were on the table:

| Sub-problem | What it means | v0.7 status | v0.8.0 status |
|---|---|---|---|
| **Awareness** | What just happened in the folder? | Single root, single feed | Multi-folder with `--root` filtering, L1 `overview` endpoint, L2 `events stats` endpoint, drill-map hypermedia hints |
| **Attribution** | Who did it, on whose behalf, why? | `producer_id` only (host:pid) | `payload_json` v1 schema (actor / session / task / intent / addressee / ref_event_id / tags), first-class CLI flags, `actors` registry with heartbeats |
| **Coordination** | Don't step on me — I'm editing this | None | `intent declare`, `lease grant/release/list/renew` (TTL-capped, advisory), **watcher emits WARN on cross-actor writes to leased paths** |
| **Conversation** | Agent ↔ agent messages tied to changes | None | `payload_json.addressee` + `ref_event_id` causal chain (verified end-to-end in the canonical handoff demo) |

All four sub-problems are now covered. The canonical handoff workflow (claude-spec → claude-code → claude-spec verify) runs end-to-end against the new binary; the transcript is captured in the agent system prompt at the top level of the repo.

---

## 2. Goals & business intent

### The customer problem (stated by Mike, owner)
> *Multiple AI agents working on the same folder system, and not knowing what each other is doing.*

In v0.7 the only way two agents could coordinate via sharedwatch was to read the prose digest and hope. There was no notion of identity (`producer_id` defaulted to `<host>:<pid>`), no notion of intent ("I'm about to edit this"), no notion of attention ("which folder do I care about?"), and no notion of conversation ("this change responds to that change").

### Goals of this branch
1. **Make multi-folder watching first-class** so a coordinator agent watching three workstreams doesn't need three sharedwatch instances.
2. **Make attribution explicit** so peers can answer "who did this, why, and is it for me?"
3. **Make coordination possible** through cooperative primitives (intents, leases) that respect the "pull, not push" doctrine.
4. **Make the journal navigable** at scale via progressive-disclosure endpoints (`overview`, `events stats`) so an agent can find what matters without burning context on raw event dumps.
5. **Lock the contract** before more agents adopt the JSON envelopes — `format_version` everywhere.
6. **Stay backward-compatible.** Existing single-folder users should see zero observable change unless they opt into the new features.

### Non-goals (deliberately)
- Multi-host or networked queue. Hand off to NATS/Redis if you grow past one host.
- Push notifications. The pull-based contract is sharedwatch's identity.
- Hard locks. Leases are *advisory*; if you need OS-level exclusion, use a different tool.
- A web UI. Agents and humans both work through CLI + JSON envelopes; that's the surface.

---

## 3. Why this branch (decision context)

Three planning documents drove this work — all in the repo root, all written before any code was touched:

- `sharedwatch-multi-agent-discussion-20260522.md` — surfaced the business problem and inventoried the gap between v0.7 capability and the multi-agent use case.
- [`../design/disclosure-attribution-discussion-20260522.md`](../design/disclosure-attribution-discussion-20260522.md) — designed the progressive-disclosure ladder (L1–L6) and the attribution layers (cooperative → inferred).
- [`../design/multifolder-design-20260522.md`](../design/multifolder-design-20260522.md) — designed the multi-folder shape, including the load-bearing rule that single-folder defaults must remain unchanged.

Plus the tickets already filed:
- [`../../tickets/ticket-multi-folder-watching-20260520_101921.md`](../../tickets/ticket-multi-folder-watching-20260520_101921.md) (SW-AGENT-3) — pre-existing, became Section 3.
- [`../../tickets/ticket-agent-event-access-20260520_035113.md`](../../tickets/ticket-agent-event-access-20260520_035113.md) (SW-AGENT-1) — already landed in v0.7.

The tasklist that drove the work was [`../../tickets/tasklist_20260523_003741.md`](../../tickets/tasklist_20260523_003741.md) — 9 sections + post-work, executed top-down with full worklog discipline.

---

## 4. What shipped — full inventory

### v0.8.0 features (in execution order)

**SW-AGENT-7 — payload_json v1 attribution** (`internal/events/payload.go`)
- `PayloadV1` struct: `actor` (required to use any other key), `actor_kind`, `session`, `task`, `intent`, `addressee`, `ref_event_id`, `tags`, plus always-1 `schema_version`.
- `BuildPayloadV1` / `ExtractActor` / `IsV1Empty` helpers.
- CLI flags: `--actor`, `--actor-kind`, `--session`, `--task`, `--intent`, `--addressee`, `--ref`, `--tag` on root flagset AND `test emit`. Subcommand non-empty wins per-field.
- Mutual exclusion: `--payload '<raw-json>'` refuses to mix with attribution flags.
- `--actor X` on root propagates to every event the watcher AND reconciler emit during the lifetime.

**SW-AGENT-11 — actor-aware coalesce** (`internal/db/db.go`, `internal/events/coalesce.go`)
- Coalesce key extended from `rel_path` to `(rel_path, watch_root, actor)`. Two actors editing the same file inside the 5s window no longer silently merge.
- New `Store.FindRecentPendingByRelPathAndActor` — bounded candidate fetch + Go-side actor filter (no JSON1 dependency on the hot path).
- Closes the silent attribution-loss bug exposed by dogfood scenario 17.

**SW-AGENT-8 — actors registry + heartbeats** (`internal/db/actors.go`)
- `actors` table: `(actor_id PK, label, actor_kind, focus, last_heartbeat, metadata_json)`.
- CLI: `sharedwatch actor heartbeat <id> --focus <glob> --kind <k> --label <l> --metadata <json>`.
- Thin heartbeats preserve prior metadata (SQL UPSERT with CASE expressions).
- `status --actors` lists actors with `stale=true` past `actor_ttl` (default 5m).
- Reconcile post-pass auto-prunes actors past 2× `actor_ttl`.
- Soft-warn on actor_kind change between heartbeats (the most common identity-confusion smell).

**SW-AGENT-3 — multi-folder watching** (every package touched)
- Schema: idempotent `watch_root` column on `events`, `digests`, `snapshots` + two new indexes. Legacy rows get `''` (the single-root sentinel).
- Config: `Config.WatchRoots []WatchRoot` (Label, Path). Inline `watch_roots: label=path,label=path` parser in config.yaml.
- CLI grammar: `--root <label>=<path>` (definition, on root flagset) vs `--root <label>` (filter, on read subcommands). Disambiguated by `=`. Mixing errors with a redirect message. Label validation: 1–64 chars `[a-zA-Z0-9_-]`, no leading dash, reserved labels (`all`, `none`, `mixed`).
- Pipeline correctness invariants (the load-bearing safety properties):
  - Coalesce keyed on `(rel_path, watch_root, actor)`.
  - `LatestSnapshot` / `SaveSnapshot` / `PruneOldSnapshots` keyed on `(source, watch_root)`.
  - `DetectRenames` runs per-root (cross-root delete+create with matching size/mtime stays as 2 events, not 1 phantom rename).
  - Cold-start emission per root.
  - Auto-mkdir per root.
- Output column visibility: `watch_root` column appears in `events list` output iff result spans >1 root OR `--include-watch-root` is passed. Single-root output byte-identical to v0.7.
- `status --json` adds `roots[]` array ONLY when multi-root configured. Single-root JSON byte-identical to v0.7.
- `digest list --root <label>` filter; digests carry `watch_root = <dominant root>` or `"mixed"`.

**SW-AGENT-9 — overview endpoint (L1)** (`internal/app/overview.go`)
- `sharedwatch overview [--since 24h] [--format text|json]`. Returns mode, pending, failed, events_in_range, by_type, top_actors, per-root counts, active actors, AND a `drill` map of canonical follow-up commands (HATEOAS-style).
- `format_version: 1` first key.
- 60s TTL cache in `runtime_state` (canonical 24h window only; bespoke windows bypass).

**SW-AGENT-10 — events stats endpoint (L2)** (`internal/app/events_stats.go`)
- `sharedwatch events stats --root <label> [--since 24h]`. `--root` REQUIRED — forces agents to pick scope before drilling.
- Returns by_type, by_actor, top_paths (with actors, sorted by event count → recency → path), hourly buckets, drill map.
- `format_version: 1` first key.

**SW-AGENT-12 — intents + leases** (`internal/db/intents.go`, `internal/db/leases.go`)
- `intents` table + `sharedwatch intent declare/list/revoke`. Forward-looking advisory ("I plan to edit X within N min").
- `leases` table + `sharedwatch lease grant/release/list/renew`. Stronger advisory ("I'm editing X now"). TTL max 1h enforced at grant AND renew.
- `--exclusive` grant refuses on conflict; default grant succeeds with `conflict_with: [...]` in the response.
- Reconcile post-pass auto-prunes expired rows.
- **S7.9 — watcher-side advisory warning** (added in punchlist after the initial ship): when an event lands on a path covered by an active lease whose actor differs from the event's actor, the watcher (and reconciler) log a structured `slog.Warn` with full context. Advisory only; does not block writes. Closes the coordination loop.

**SW-AGENT-14 — output contract** (`docs/OUTPUT_CONTRACT.md`)
- `format_version: 1` swept onto every structured envelope (`status --json`, `overview`, `events stats`, plus the existing `events list`, `sql` envelopes via the shared `internal/output` package).
- `schema --format json` documented as an explicit exception (bare array by design).
- Versioning policy: additive changes never bump; renames/removals/semantic shifts do, with a deprecation window.

**SW-AGENT-13 — renderer compression** (partial)
- `events stats --format text` collapses quiet hourly buckets into `quiet HH:MM–HH:MM (Nh)` summaries.
- JSON/JSONL/CSV deliberately NOT compressed — data is the contract.
- Path-prefix factoring, hash-stable elision, `--uncompressed` flag deferred to v0.8.1+.

**Dogfood-discovered bug fixes**
- **SZ.1**: `events list --since 5m` (and `--until`) now parse Go duration strings as well as RFC3339, matching the documented behaviour. Caught during the canonical handoff smoke run.
- **S13**: `catalog.Ignored()` now matches on directory segments, not just basename and exact rel_path. The default `.git` pattern previously leaked everything under `.git/` into the journal. Found during the full 18-scenario dogfood pass; fixed with a 10-case regression test covering positive cases (`.git/objects/abc`, `node_modules/foo/index.js`, `auth/.git/config`) and tricky negatives (`.gitignore` ≠ `.git`, `docs/git-tutorial.md` ≠ `.git`).

**CI integration (SZ.4)**
- gitleaks v8.24.3 step added to `.github/workflows/ci.yml`. Runs on every push/PR with full history. Mirrors the local pre-commit/pre-push hooks.

### Punchlist items (P1–P4) added post-ship audit
- **P1**: skill freshness sweep — eliminated 6 stale "will land" phrases across Skills + agent system prompt.
- **P2**: watcher-side lease advisory warning (S7.9) — was deferred during the initial ship; reclaimed and landed.
- **P3**: canonical handoff demo — end-to-end verification of the spec→code→verify workflow. Transcript captured in the agent system prompt.
- **P4**: agent system prompt resync — all `[FUTURE]` markers removed; verified handoff example added.

---

## 5. Engineering conventions followed

### Style (per `CONTRIBUTING.md` + `sharedwatch-contributor` skill)
- Go 1.22+; `gofmt -w .` before every commit.
- Errors wrapped with `fmt.Errorf("context: %w", err)`. No `panic` outside `main`.
- New code paths get at least one unit test. Integration tests use `t.TempDir()` databases — never mocks.
- Default to no comments; when warranted, document *why*, not *what*. Don't reference the current task / fix / caller (rots).
- New tests added: 49 across the affected packages.

### Schema discipline
- Every migration is additive and idempotent (uses existing `addColumnIfMissing` / `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`).
- **Zero `DROP COLUMN`s, zero column renames.** Forward-only schema.
- Backward compatibility: empty-string sentinel for `watch_root` carries legacy single-root rows transparently.

### Output contract
- Every JSON envelope carries `format_version: 1` as the first key (single exception: `schema --format json` is a bare array by design).
- snake_case field names; RFC3339Nano for timestamps; `omitempty` for optional fields.
- Text format is explicitly NOT a contract.

### Adapter interface
- All new persistence functions added to `db.Adapter` interface. The interface grew from ~30 to 47 methods. No second implementation exists today, but future adapters will have a clear contract.

### Multi-root invariants (non-negotiable)
- Coalesce scoped to root + actor.
- Snapshots keyed `(source, watch_root)`.
- Rename pairing never crosses roots.
- Cold-start emission per root.
- Single-root behaviour byte-identical to v0.7.

### "Duplicates beat misses" doctrine preserved
- Reconcile still re-emits events the watcher missed.
- Agents are responsible for idempotency via `(rel_path, content_hash)` dedup.

---

## 6. Major changes / architectural notes

### Adapter interface growth
The `db.Adapter` interface gained 17 new methods across the ship (actors CRUD, intents CRUD, leases CRUD, snapshot key changes, root-scoped lookups, runtime JSON cache). It's now ~47 methods, all on a single `*db.Store` implementation. If a second adapter is ever needed, all 17 will need implementation — a real cost worth noting.

### Service layer iteration
`watcher.Service.ScanAndQueue` and `reconcile.Service.RunNow` now iterate roots internally via an `effectiveRoots()` helper. Legacy single-`WatchPath` callers transparently flow through a one-element slice with `Label=""`. The pattern keeps single-root code paths simple.

### Cache for overview
`runtime_state` table gained two general-purpose helpers (`UpsertRuntimeJSON` / `GetRuntimeJSON`). Currently used only by `ComputeOverview` for the 60s TTL cache; reusable by any future endpoint that needs lightweight per-key persistence.

### Actor extraction lives in events package
`events.ExtractActor(payloadJSON)` is the canonical way to pull the actor out of a payload. Used by the actor-aware coalesce in db.go, by the overview/stats aggregations, and by the lease advisory warning. Single source of truth; tolerant of forward versions and malformed JSON.

### CLI grammar disambiguation
`--root` overload (definition vs filter, by `=` presence) was deliberate. Single overloaded flag is friendlier than two flags (`--define-root` + `--filter-root`); the disambiguator is unambiguous and the error message points users to the right form when they confuse them.

---

## 7. Tasks remaining (deferred, not blocking ship)

Filed inline in CHANGELOG; tracked as follow-up work for v0.8.1+:

1. **Renderer polish (SW-AGENT-13 S8.3–S8.6)** — path-prefix factoring (`auth/{login.go, oauth.go}`), hash-stable elision, `--uncompressed` flag. Pure text-output cosmetic. No agents depend on it. **~1 day.**

2. **`test emit --root <label>`** — multi-root mode currently tags synthetic events with the first configured root's label. Surprising and undocumented. **~30 min.**

3. **Full 18-scenario dogfood as canonical regression baseline** — this branch's dogfood pass produced the artefacts; future runs should diff against them. Folding the run-all script into `make dogfood` would make it routine. **~1 hour.**

4. **`actor` / `session` as promoted columns** — currently lives in `payload_json`; query goes through `--payload-key actor`. Promotion would speed up filtering by index but requires a migration. Should wait until usage proves the access pattern is common enough to justify schema change. **~1 day when triggered.**

5. **A second `db.Adapter` implementation** — currently SQLite-only. If/when a second adapter is needed, implementing 47 methods is non-trivial. Probably never needed; flagged for awareness.

6. **Cross-root retention / quotas** — noisy-neighbor protection. Should wait for evidence (a real noisy root pushing useful events out of the queue). **~1 day when triggered.**

7. **Schema migration test against a pre-v0.7 fixture DB** — covered for the v0.7→v0.8 path, not for older. Probably not needed; flagged.

---

## 8. Future thinking — why this branch is good

### It closes the four-quadrant gap
Awareness + Attribution + Coordination + Conversation were all named in the planning docs and all addressed in the ship. The branch is internally consistent — the design docs, the code, the tests, and the skills all describe the same product.

### The invariants are protected by tests
The load-bearing properties (single-folder backwards compat; cross-root correctness; actor-aware coalesce; format_version contract; lease TTL cap) all have dedicated tests. A future contributor breaking any of these gets caught at `go test` time.

### The Skills carry the design forward
`Skills/sharedwatch-contributor/`, `Skills/sharedwatch-client/`, `Skills/sharedwatch-client-future/` are all up-to-date as of v0.8.0. A new agent (or human) loading these skills gets a working mental model immediately. The agent system prompt at the top level carries a verified copy-pasteable example of the canonical multi-agent flow.

### The output contract is locked
`format_version` is the first key in every envelope. Agents that adopt the v0.8.0 shapes are protected against silent renames. The policy is documented in `docs/OUTPUT_CONTRACT.md` with a breaking-change checklist.

### The coordination story is real, not aspirational
Intents + leases + the watcher-side advisory warning together mean: cooperative peers see each other's claims AND get warned when an uncooperative write happens. The full canonical handoff scenario (spec → code → verify) ran end-to-end in the dogfood pass; the transcript proves it.

### Backward compatibility is real, not theoretical
Single-folder, single-actor users see zero observable difference. Existing v0.7 databases migrate transparently (additive schema; empty-string sentinel for `watch_root`). The deprecation window on any breaking change is documented in the contract.

---

## 9. What we could add if the need arises

These are NOT proposed for v0.8.0. They are the "if a real customer hits this, here's what we'd do" backlog. Listed in priority order if a real demand emerges.

### Near-term (single ticket each)
- **Content blob store** — store the file's bytes alongside the event, keyed on `content_hash`. Unlocks "what did the file look like at the time of event N?" replay queries. Disk-heavy; would need retention policy. ~2 days.
- **Push surface** (`events watch`) — long-poll / SSE / websocket on top of `events list --cursor-name`. Removes the polling tax. Useful only if the 5s active-mode cadence proves insufficient — and even then, agents should prefer wider polls over real-time push (matches the calm-by-design ethos). ~1 day.
- **`messages` table** — dedicated agent ↔ agent messaging decoupled from file changes. Today this is done via `payload_json.addressee` + `ref_event_id`. A dedicated table would give richer query semantics. Probably not needed; agents adapt to the convention. ~1 day if filed.
- **Per-actor read receipts** (`seen` table) — "has X consumed my handoff?" Useful for confirming the receiver got it. Today, "did peer respond?" via `ref_event_id` filter is the inverse. ~0.5 day.

### Medium-term (each is a small project)
- **OS-level inferred attribution** — `fanotify` (Linux) or audit subsystem to map FS event → writing PID → command-line. Closes the "agent forgot to set --actor" gap. Heavy; platform-specific. Probably never. ~3 days.
- **Cross-host event publication** — sharedwatch stays local, but publishes its event stream to NATS / Redis Streams / Kafka so multi-host fleets can consume. The right answer to "we outgrew one host." ~2 days for one of these backends.
- **Web UI** — humans-only surface; thin TUI over JSON envelopes. Agents have no need; humans currently use `digest show`. Build only if a customer asks.
- **Per-root config** (mode, retention, ignore patterns) — moves beyond v0.8.0's "global config, multiple roots" model. Useful only if noisy-neighbor cases appear in practice. ~1 day.

### Long-term (each is an architectural commitment)
- **Real concurrency control** — distributed locks via lease-with-fencing-tokens or a coordination service. Today's leases are advisory. Promoting to hard locks would change the trust model and likely require a second storage layer (Redis, etcd). Out of character for sharedwatch; should be a separate tool.
- **Multi-tenancy / authentication** — sharedwatch today trusts the local filesystem. A multi-tenant SaaS version would need auth, audit, isolation. Different product.
- **Schema evolution beyond v1** — when format_version eventually bumps to 2, the contract requires a deprecation window with side-by-side emission. The machinery is documented but untested. First real bump will be the proof.

---

## 10. Why this branch is shippable

Concrete acceptance criteria, met:

- ✅ All 9 work sections + 4 punchlist items in the tasklist marked done.
- ✅ All 11 Go packages green: `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` passes.
- ✅ 49 new tests across the affected packages; load-bearing invariants explicitly tested.
- ✅ Full 18-scenario dogfood pass executed against the new binary; 17 passed as documented, 1 real bug found (`.git` ignore pattern leak) and fixed in the same pass with a 10-case regression test. Findings catalogued in [`../dogfood/findings-20260523.md`](../dogfood/findings-20260523.md); artefacts in `/tmp/sharedwatch-dogfood/artifacts/`.
- ✅ Canonical multi-agent handoff (spec → code → verify) verified end-to-end.
- ✅ Cross-actor lease violation warning verified in dogfood scenario 17.
- ✅ Backward compatibility preserved: single-root callers see zero observable change.
- ✅ Skills, README, CHANGELOG, OUTPUT_CONTRACT, agent system prompt all accurate as of v0.8.0.
- ✅ CI gitleaks step protects against credential leaks alongside the local hooks.
- ✅ The four sub-problems from the original business intent (Awareness, Attribution, Coordination, Conversation) all have shipped solutions.

What's NOT in scope of "shippable" (and that's fine):
- Renderer polish — pure cosmetic.
- `test emit --root` — debugging tool gap.
- Cross-host / push / content store — non-goals or "when triggered" items.

---

## 11. References

- [`../../tickets/tasklist_20260523_003741.md`](../../tickets/tasklist_20260523_003741.md) — the complete worklog from claim to close-out.
- [`../design/multi-agent-discussion-20260522.md`](../design/multi-agent-discussion-20260522.md) — the gap analysis that motivated this branch.
- [`../design/disclosure-attribution-discussion-20260522.md`](../design/disclosure-attribution-discussion-20260522.md) — the disclosure + attribution design.
- [`../design/multifolder-design-20260522.md`](../design/multifolder-design-20260522.md) — the multi-folder design quality layer.
- [`../agent/agent-system-prompt-20260522.md`](../agent/agent-system-prompt-20260522.md) — the prompt for AI agents using sharedwatch (post-resync).
- `../../src/docs/OUTPUT_CONTRACT.md` — the envelope versioning policy.
- `../../src/CHANGELOG.md` `[Unreleased]` — 10 named blocks covering the full ship.
- `../../Skills/sharedwatch-client/` and `../../Skills/sharedwatch-client-future/` — agent operating skills, both up to date.
- `../../Skills/sharedwatch-contributor/` — repo onboarding skill.
- [`../dogfood/test_dogfood.md`](../dogfood/test_dogfood.md) — the 18-scenario test plan (executed during ship; artefacts at `/tmp/sharedwatch-dogfood/artifacts/`).
- [`../../tickets/ticket-agent-event-access-20260520_035113.md`](../../tickets/ticket-agent-event-access-20260520_035113.md) — SW-AGENT-1 (v0.7 base).
- [`../../tickets/ticket-multi-folder-watching-20260520_101921.md`](../../tickets/ticket-multi-folder-watching-20260520_101921.md) — SW-AGENT-3 (became Section 3).
- [`../dogfood/findings-20260523.md`](../dogfood/findings-20260523.md) — the per-scenario findings from the 18-scenario dogfood pass, including the `.git` bug and the SIGKILL-orphans-lock note.

---

## Bottom line for the reviewer

This branch turns sharedwatch from a calm single-folder activity feed into a multi-agent coordination substrate without breaking any of the v0.7 promises. The design was deliberately layered: every feature has fallback semantics for callers who don't opt in; every contract is explicit; every load-bearing invariant has a test. The dogfood pass is the proof point — the canonical workflow runs end-to-end, and the boring scenarios (renames, ignored patterns, cold start) all behave as the v0.7 binary did.

It's ready.
