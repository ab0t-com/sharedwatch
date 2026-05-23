# TICKET: Content diffs + actor registration — LOCKED DESIGN + implementation spec

**ID:** SW-AGENT-16
**Filed:** 2026-05-23 09:35 UTC
**Filed by:** claude (this session)
**Status:** open, not yet claimed — **design locked, ready to implement**
**Target:** sharedwatch v0.9.0
**Estimated:** ~4 focused engineer-days (3 for diffs incl. lib integration, schema + tests; 1 for registration + session-token plumbing; tightly bounded)
**Supersedes:** [SW-AGENT-15](ticket-content-diff-and-actor-registration-20260523_090258.md) — same scope, but every open question is now a decision; schema is locked; configurability + ownership semantics are spelled out for future-proofing. Implement against this file, not SW-AGENT-15.

**⚠ STORAGE DESIGN UNDER REVIEW:** The user has raised the question of whether to use git-format content-addressed blob storage (hidden in `<data_dir>/blobs/`, isolated from any user-facing git repo) instead of the gzipped-diff-text approach in §4.1 below. The evaluation is in [`../docs/design/content-storage-evaluation-20260523.md`](../sharedwatch-content-storage-evaluation-20260523.md). **Do not start §4–§8 implementation until the evaluation reaches a decision.** Most of this ticket (registration, configurability, ownership semantics, library choices, test coverage) is unaffected by that decision; only the content-storage shape (table name `event_diffs` → `event_content`, BLOB column → hash + sidecar blob store, precomputed diff → on-demand diff) would change. The recommendation in the evaluation is to switch to blob storage (Option B / Path γ).

---

## 1. Why a second ticket

SW-AGENT-15 framed the feature and named eight open questions. The user feedback after review:

> *"the design and schema need need to get right; ensure it's configurable in the future; doesn't leak ownership; we store data correctly; use best practice engineering like using a lib where possible; cover everything you said + your recommendations for all the questions."*

This ticket bakes in those decisions so implementation has no ambiguity. It also adds three sections that SW-AGENT-15 didn't have:

- **§4 Schema decisions (locked)** — every column, every default, every index, every FK behaviour decided up-front.
- **§7 Ownership / attribution semantics** — explicit rules for how diff rows relate to event rows, who "owns" what, how registration + session_token interact with the journal.
- **§9 Forward-compatibility considerations** — how v0.9.1+ extensions can land without breaking v0.9.0 consumers.

SW-AGENT-15 stays as the historical framing doc; this ticket is what the implementer follows.

---

## 2. Context — what we are doing and why

(condensed from SW-AGENT-15 §1 and `../docs/design/design-questions-20260523.md` Q2)

sharedwatch v0.8.0 captures the *that* of file activity (path, time, hash, size, attribution-if-volunteered). It does not capture the *what* — the bytes that changed. For the swarm-coordination use case ("multiple AI agents working on the same folder system and not knowing what each other is doing"), the missing content layer makes the journal useful for triggers and alarms but thin for *understanding*.

Two changes close that gap:

- **Part A — content diffs**, captured by the watcher regardless of writer cooperation. Standard unified diff per `file.modified` event, computed against the prior known content, gzip-compressed, stored in a separate `event_diffs` table.
- **Part B — first-class actor registration**, an explicit startup handshake stricter than the existing heartbeat. Reliable identity, conflict detection, optional session_token validation, clean release path.

Together: when a consumer asks "what has claude-X been doing in `auth/`?", the answer is "here are 12 events, each with a diff showing exactly what changed, all confirmed-attributable to claude-X via their registered session." Not "12 files were touched, probably by claude-X."

---

## 3. Design principles (locked)

These are the constraints implementation must respect:

1. **Backward compatibility is absolute.** A v0.8.0 callsite (no `--diff`, no `register`) must behave byte-identically to today. No new columns appear in output unless requested; no new mandatory flags; no breaking schema changes.
2. **Opt-in cost.** Diff capture is OFF by default. Operators enable it explicitly via `--diff on` or `diff_enabled: true`. Same for `require_actor_registration` (default off) and `enforce_session_token` (default off). Three config knobs, each independent.
3. **Library reuse over reinvention.** Use stdlib where possible (`compress/gzip`, `database/sql`, `crypto/rand`, `os`/`syscall` for PID liveness). Single new external dep: `github.com/pmezard/go-difflib` for unified-diff formatting (small, zero transitive deps, MIT, ~500 LOC). No new test framework — `testing` only. The "modernc.org/sqlite is the only dep" rule was already broken in spirit (gzip/json/etc. are stdlib but still "deps"); we add one purpose-built lib and justify it.
4. **Storage correctness via transactions.** The `(events row, event_diffs row)` pair MUST be written in a single transaction. Either both land or neither does. WAL mode already guarantees crash consistency for that transaction.
5. **Ownership flows from the event.** `event_diffs` rows have NO actor / session field of their own. Attribution is read via the FK to `events`, which carries `payload_json.actor`. Diffs are owner-blind on write; consumers do `events JOIN event_diffs` and read actor from the event side.
6. **Schema is forward-extensible.** Every new table has a `format_version` column (where the row format itself can evolve), reserved nullable columns for future use, and a `metadata_json` blob for opaque per-row extension without migrations.
7. **Diffs never block writes.** If diff computation fails (lib error, OOM, corrupt cache), the event still inserts; `event_diffs` carries `status='unavailable'` with `error_reason`. Lossy diff > lost event.
8. **Test the invariants explicitly.** Each ownership/storage/configurability rule above has at least one dedicated test (see §11).

---

## 4. Schema decisions (locked)

### 4.1 New table `event_diffs`

```sql
CREATE TABLE IF NOT EXISTS event_diffs (
  event_id TEXT PRIMARY KEY,
  format_version INTEGER NOT NULL DEFAULT 1,          -- row schema version; bump on any breaking change
  diff_format TEXT NOT NULL,                          -- 'unified-v1' | 'binary-v1' | 'truncated-unified-v1' | 'none'
  diff_status TEXT NOT NULL,                          -- 'available' | 'binary' | 'too_large' | 'no_prior_content' | 'truncated' | 'unavailable'
  error_reason TEXT NOT NULL DEFAULT '',              -- populated when status='unavailable'
  lines_added INTEGER NOT NULL DEFAULT 0,
  lines_removed INTEGER NOT NULL DEFAULT 0,
  bytes_added INTEGER NOT NULL DEFAULT 0,             -- for binary: size delta sign-preserved; for text: bytes in added lines
  bytes_removed INTEGER NOT NULL DEFAULT 0,
  diff_gz BLOB,                                       -- gzipped unified diff text; NULL when status != 'available' and != 'truncated'
  content_hash_before TEXT NOT NULL DEFAULT '',
  content_hash_after TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  metadata_json TEXT NOT NULL DEFAULT '{}',           -- opaque future extension; no parser today
  FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_event_diffs_status ON event_diffs(diff_status);
CREATE INDEX IF NOT EXISTS idx_event_diffs_format ON event_diffs(diff_format);
CREATE INDEX IF NOT EXISTS idx_event_diffs_created_at ON event_diffs(created_at);
```

Rationale for each column:
- `event_id PRIMARY KEY` — one diff per event; the PK constraint makes the 1:1 explicit. `ON DELETE CASCADE` means retention pruning of `events` cleanly removes diffs.
- `format_version` — row-shape evolution. Today everything is `1`; if a future change adds non-null-defaulted columns, bump to `2`. Consumers check `format_version` before reading new fields.
- `diff_format` — the *encoding* of the diff. `unified-v1` is the standard. `binary-v1` is a placeholder for binary files (no diff text). `truncated-unified-v1` is a unified diff with some hunks replaced by truncation placeholders. `none` for status='unavailable'/'no_prior_content'.
- `diff_status` — the *outcome*. Enum.
- `error_reason` — string for `'unavailable'`. Empty otherwise. Lowercase-snake-case ("oom", "lib_panic", "cache_miss_on_anchor_skipped").
- `lines_added` / `lines_removed` — counted by walking the diff. For binary: 0 / 0.
- `bytes_added` / `bytes_removed` — for text: sum of bytes in added/removed lines (excluding diff metadata). For binary: signed size delta in `bytes_added` (positive = grew), `bytes_removed` = 0.
- `diff_gz BLOB` — gzipped UTF-8 unified-diff text. NULL when no diff content is available.
- `content_hash_before` / `content_hash_after` — SHA-256 (matching events.content_hash format). Empty when not computed.
- `created_at` — when this diff row was inserted (mirrors events.created_at format). Used for ad-hoc time-bounded queries against `event_diffs` directly (e.g., "all diffs in the last hour").
- `metadata_json` — opaque escape hatch. v0.9.0 emits `'{}'`. Future tickets can add structured fields here without a migration.

### 4.2 Extensions to existing `actors` table

```sql
ALTER TABLE actors ADD COLUMN registered_at TEXT NOT NULL DEFAULT '';  -- empty when heartbeat-only
ALTER TABLE actors ADD COLUMN host TEXT NOT NULL DEFAULT '';           -- hostname of the registering process
ALTER TABLE actors ADD COLUMN pid INTEGER NOT NULL DEFAULT 0;          -- registering process's pid; 0 = unknown
ALTER TABLE actors ADD COLUMN session_token TEXT NOT NULL DEFAULT '';  -- 'stk_<16-hex>'; rotated on each register
```

Why columns vs a new `actor_registrations` table:
- One actor has at most one active registration at a time. Multiple-registrations would require collision handling already covered by `--reclaim`. Single row keeps the read path simple.
- The four new columns are small, NULLable-equivalent (empty string / 0), and answer the common queries with one row.
- A future "registration history" feature (audit log of all reclaim events) would justify a separate table. Until then, this is enough.

### 4.3 Migration ordering and idempotency

All three migrations are additive, idempotent, and applied via the existing `addColumnIfMissing` + `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS` helpers. Order in `migrate()`:

1. `event_diffs` table + 3 indexes (new; cannot break existing schema)
2. `actors.registered_at` + `actors.host` + `actors.pid` + `actors.session_token` (additive columns; old rows get the defaults)

A pre-v0.9 DB opened by the v0.9 binary applies both transparently. No DROP COLUMN, no rename. Test: `TestMigrateV08ToV09` opens a checked-in v0.8 fixture DB and asserts both migrations land cleanly with old rows preserved.

### 4.4 Schema-contracts doc update

`sharedwatch/docs/SCHEMA_CONTRACTS.md` gains two new subsections documenting `event_diffs` table fully (every column, every enum, examples) and the new `actors` columns. The schema discovery endpoint (`sharedwatch schema --format json`) reflects both automatically via PRAGMA table_info.

---

## 5. Configuration decisions (locked)

All new knobs are config-yaml keys AND CLI flags, with the flag winning over the config (matches existing precedence). All default to OFF / safe values so v0.8.0 callsites are byte-identical.

| Knob | Default | CLI flag | YAML key | Notes |
|---|---|---|---|---|
| Enable diff capture | off | `--diff on\|off` | `diff_enabled: true\|false` | Master switch. When off, `event_diffs` table is migrated but never written. |
| Max file size for diffing | `262144` (256 KB) | `--diff-max-size <bytes>` | `diff_max_size: 262144` | Files above this size get `diff_status='too_large'`. |
| In-memory content cache size (file count) | `200` | `--diff-cache-size <N>` | `diff_cache_size: 200` | LRU eviction. Larger = more diffs available; more memory. |
| Per-hunk byte cap (truncation) | `65536` (64 KB) | `--diff-truncate-hunk-size <bytes>` | `diff_truncate_hunk_size: 65536` | Hunks larger than this are replaced with a placeholder; row marked `diff_status='truncated'`. |
| Require actor registration | off | `--require-actor-registration` | `require_actor_registration: true` | When on, emits without a valid registered actor are rejected (exit 1). v0.9.0 ships the flag but the default is off — strict mode is opt-in. |
| Enforce session_token | off | `--enforce-session-token` | `enforce_session_token: true` | When on, emits with `--actor X --session-token Y` are validated against the registration. Mismatch → rejected. |
| Binary detection threshold | `0.30` | `--diff-binary-threshold <0..1>` | `diff_binary_threshold: 0.30` | Fraction of non-printable bytes in first 8KB above which a file is treated as binary. NUL byte forces binary regardless. |

**Why these specific defaults:**
- `256 KB` for diff-max-size — typical source files are <50 KB; 256 KB covers large config files, generated headers, etc., without inviting log-file-sized diffs.
- `200` for cache — at avg 100 KB content per file = ~20 MB resident. Bounded. Tunable.
- `64 KB` for hunk truncation — same magnitude as max-size; a single huge hunk in a 256 KB file file is rare but possible (e.g., generated minified code). Truncating to 64 KB keeps event_diffs rows bounded.
- `0.30` for binary threshold — matches `git diff --binary` heuristic empirically. Conservative; rare false positives on UTF-8 source.

### 5.1 Configurability for future per-root settings

`v0.9.0` ships GLOBAL settings only (one `diff_enabled` for the whole daemon). Per-root settings (e.g., "diff on for `src/`, off for `node_modules/`") are deliberately deferred to v0.9.x.

To make that future addition non-breaking, the schema-level design choice is:
- Diff settings are NOT serialized into `event_diffs` rows. They affect what gets written but aren't attribute-of-the-row. So adding per-root settings later just routes the right config to the right root in code; no data migration.
- Config knobs use flat keys today (`diff_enabled`). Future per-root would use nested form (`watch_roots: [{label: auth, diff_enabled: true}]`), parsed by an additive code path. Flat keys remain valid as the default for roots that don't override.

### 5.2 Config file format (v0.9.0 additions)

```yaml
# v0.9.0 — additive to existing config.yaml
diff_enabled: true
diff_max_size: 262144
diff_cache_size: 200
diff_truncate_hunk_size: 65536
diff_binary_threshold: 0.30
require_actor_registration: false
enforce_session_token: false
```

All optional. Defaults match the table above. Parsing extends `internal/config/file.go` (one switch case per new key; trivial).

---

## 6. CLI surface (locked)

### 6.1 New global flags (root flagset)

```
--diff on|off                          enable per-event content diff capture (default off)
--diff-max-size <bytes>                cap above which files get status='too_large' (default 262144)
--diff-cache-size <N>                  in-memory content cache file count (default 200)
--diff-truncate-hunk-size <bytes>      per-hunk byte cap (default 65536)
--diff-binary-threshold <0..1>         non-printable fraction for binary classification (default 0.30)
--require-actor-registration           reject emits from unregistered actors (default off)
--enforce-session-token                reject emits where --session-token doesn't match registration (default off)
```

All seven are additive; absent flag = default = byte-identical to v0.8.0.

### 6.2 New `actor` subcommands

```
sharedwatch actor register <actor-id> [--kind ai_agent|human|automation]
                                       [--label "..."]
                                       [--focus <path-glob>]
                                       [--metadata '<json>']
                                       [--reclaim]
                                       [--format json|text]
# Strict: fails (exit 1) if actor_id is registered to a live PID.
# --reclaim overrides (warns if prior PID still alive).
# Returns JSON envelope:
{
  "format_version": 1,
  "actor_id": "claude-X",
  "registered_at": "2026-05-23T10:00:00.000Z",
  "session_token": "stk_a1b2c3d4e5f6g7h8",
  "host": "claude-host-7",
  "pid": 12345
}

sharedwatch actor release <actor-id> [--session-token <stk_...>]
# Clears registered_at + host + pid + session_token in actors row.
# Row itself stays (last_heartbeat etc. preserved).
# --session-token if provided is verified before release; mismatch → exit 1.

# Existing `actor heartbeat` unchanged (tolerant, secondary path).
```

### 6.3 New `diff` subcommand

```
sharedwatch diff <event-id> [--format text|json]
# text (default): prints the unified diff (ungzipped). Header line shows path,
#                 actor (from event), lines_added/removed, diff_status.
# json: returns the EventDiff envelope:
{
  "format_version": 1,
  "event_id": "evt_...",
  "diff_format": "unified-v1",
  "diff_status": "available",
  "lines_added": 12,
  "lines_removed": 3,
  "bytes_added": 287,
  "bytes_removed": 64,
  "content_hash_before": "abc123...",
  "content_hash_after": "def456...",
  "diff_text": "<ungzipped unified diff>",
  "metadata_json": "{}"
}
# Errors clearly when:
#  - event_id not found → exit 1, "event not found"
#  - event has no diff row (diff was off when emitted) → exit 0, prints "no diff for this event (capture was disabled)"
#  - diff_status != 'available' → prints the status + reason; exit 0
```

### 6.4 New `--session-token` flag on emit paths

```
sharedwatch --actor claude-X --session-token stk_xyz run
sharedwatch test emit foo.md --actor claude-X --session-token stk_xyz
```

When `enforce_session_token` is OFF (default), the token is recorded in the emit but never validated. When ON, the daemon verifies token matches the actor's current registration; mismatch → emit rejected with exit 1.

### 6.5 `events list` projection of diff fields

```
sharedwatch events list --fields rel_path,type,lines_added,lines_removed,actor,created_at
# When --fields requests lines_added/lines_removed/bytes_added/bytes_removed/diff_status,
# the query LEFT JOIN's event_diffs and projects from there.
# Events without a diff row show 0/0/'' for those fields.
```

The default `--fields` is unchanged — no new columns appear unless explicitly requested. Backward compatibility absolute.

### 6.6 Help text + usage updates

`cmd/sharedwatch/main.go`'s `usage()` block gains:
- `diff <event-id>` under COMMANDS
- `actor register <actor-id>` and `actor release <actor-id>` under COMMANDS (alongside existing `actor heartbeat`)
- The new global flags in the flag dump (automatic via `root.PrintDefaults()`)

---

## 7. Ownership / attribution semantics

These are the rules consumers (and reviewers) can rely on. Each has at least one test.

### 7.1 Diff rows are owner-blind on write

`event_diffs` has NO `actor`, `session`, `producer_id`, or `watch_root` columns. The diff is a fact about *bytes that changed between two snapshots*, not about *who caused the change*. Attribution lives on the event row; the FK provides the link.

**Why:** prevents attribution drift. If the event's payload is later updated (e.g., a `coalesced_into` event has its actor changed by coalescing), the diff doesn't need to follow. The diff is immutable per-event; the event carries the canonical attribution.

**Test:** `TestEventDiffHasNoActorColumns` — schema introspection asserts no `actor*` / `producer_id` / `watch_root` columns in `event_diffs`.

### 7.2 Consumer-side attribution via JOIN

```sql
-- Canonical "what did claude-X change?" query
SELECT e.rel_path, e.observed_at, d.lines_added, d.lines_removed, d.diff_status
FROM events e
LEFT JOIN event_diffs d ON d.event_id = e.id
WHERE json_extract(e.payload_json, '$.actor') = 'claude-X'
  AND e.created_at > datetime('now', '-1 hour')
ORDER BY e.created_at DESC;
```

The skill + recipe docs show this pattern explicitly.

### 7.3 Session token is verified, never embedded in events

When `--session-token` is set on an emit, the daemon validates it against the actor's current registration in `actors.session_token` AT EMIT TIME. The token itself is NOT written into the event row, NOT into the payload_json, NOT into event_diffs. After validation, it's discarded.

**Why:** tokens are an authentication signal, not a record signal. Embedding them in the journal would leak them to anyone who can read the journal — exactly what they're meant to authenticate.

**Test:** `TestSessionTokenNotPersistedInEvents` — emit with a token; query the resulting event row + diff row; assert neither contains the token string.

### 7.4 Registration affects FUTURE emits only

When an actor registers, no prior journal rows are re-attributed. When an actor releases or is reclaimed, prior rows keep whatever attribution they had at emit time. The journal is append-only with respect to attribution.

**Why:** retroactive re-attribution would silently change history and break consumer cursors that have already passed those rows.

**Test:** `TestRegisterDoesNotRetrofitPriorEvents` — heartbeat-only actor emits 3 events; then registers; assert the 3 prior events still have empty `registered_at`-via-join (semantically: still heartbeat-attributed).

### 7.5 The `reclaim` path is explicit + auditable

`actor register --reclaim` writes a new row state. The PRIOR registration's `session_token` is invalidated — any in-flight emit with the old token will be rejected (when enforcement is on) or quietly succeed under the new identity (when enforcement is off).

**Why operators need to know:** reclaim is a takeover. It should not be quiet.

The implementation logs a `slog.Warn` line at reclaim time with: prior PID, prior host, prior registered_at, new PID, new host, new registered_at. (No data is destroyed; the prior row state isn't audited beyond the log line in v0.9 — a future "registration audit log" table would track this if/when needed.)

**Test:** `TestReclaimLogsWarn` — capture slog output; reclaim; assert the warn line includes both prior and new state.

### 7.6 Diff computation never reads payload_json

The diff path operates on bytes (FS content) and writes to `event_diffs`. It never inspects `payload_json` (no actor lookup, no intent parsing). This guarantees the diff is *what happened in the bytes*, decoupled from *what was claimed about it*.

**Why:** keeps the diff trustworthy. An agent that lies in `payload.intent` ("formatting only") can't lie via the diff — the diff is computed from FS reality.

**Test:** `TestDiffComputationIgnoresPayload` — emit a synthetic event with a misleading `intent`; verify the diff doesn't reference / depend on the payload field.

---

## 8. Storage correctness invariants

### 8.1 Diff insert is in the same transaction as the event insert

`Store.InsertEventWithDiff(ctx, event, diff)` opens a tx, inserts the event row, inserts the diff row, commits. If either fails, both roll back. WAL provides crash consistency for the pair.

For coalesce path (`InsertOrCoalesceEvent`), the diff is associated with the SURVIVING event (the one that absorbs the merged events). If coalesce updates an existing event, the existing `event_diffs` row is UPDATED to reflect the new content (old `content_hash_before` preserved, new `content_hash_after` updated, new diff_gz overwrites).

**Test:** `TestEventDiffTransactional` — inject a sql.ErrTxDone into the diff insert; assert event also rolls back (no orphaned event without a diff when one was expected).

### 8.2 No orphaned diffs after retention pruning

`event_diffs.event_id REFERENCES events(id) ON DELETE CASCADE` ensures that `PruneOldProcessedEvents` (and any future event-pruning path) cleans diffs automatically. SQLite needs `PRAGMA foreign_keys=ON` for CASCADE to fire — verify this is set in `Open()` after the WAL pragma.

**Test:** `TestPruneOldEventsCascadesDiffs` — insert N old processed events with diffs; prune; assert both events and event_diffs rows are gone.

### 8.3 Bounded memory under diff cache pressure

The in-memory LRU cache holds at most `--diff-cache-size` file contents (default 200) with a hard upper bound of `--diff-max-size` per entry. Worst case: 200 × 256 KB = 50 MB resident.

Cache implementation: `container/list` from stdlib for LRU; map for O(1) lookup. No external lib.

**Test:** `TestDiffCacheRespectsCapacity` — fill cache beyond capacity; assert LRU evicts to stay at capacity.

### 8.4 Diff failure modes never corrupt the journal

If `pmezard/go-difflib` panics on an edge-case input, the diff path catches the panic (defer + recover), logs at error level, and inserts the event_diffs row with `diff_status='unavailable'`, `error_reason='lib_panic'`. The event itself still inserts.

**Test:** `TestDiffLibPanicCaught` — feed a constructed pathological input that's known to trip the lib (e.g., extremely long lines); assert the event lands with diff_status='unavailable'.

### 8.5 Anchor capture is always-on for new files (when diff enabled)

First-seen of any file (size ≤ max, non-binary) caches its content. The corresponding `file.created` event has `diff_status='no_prior_content'` (correct — there's no prior to diff against). The NEXT modify against the same path will have a real diff because the anchor is cached.

**Test:** `TestFirstScanCreatesAnchorThenDiff` — diff-enabled scan with one new file: assert created event has status='no_prior_content' AND that the content is in the cache; modify the file; second scan: assert modify event has a real diff.

### 8.6 SHA-256 hashes in event_diffs match events.content_hash when both present

When the watcher captures content (diff enabled) AND hashing is enabled (`--hash on`), the hash computed for `events.content_hash` and the hash stored in `event_diffs.content_hash_after` are the SAME bytes. Specifically: hashing happens once per content read, with the result fanned to both consumers.

**Test:** `TestHashConsistencyEventVsDiff` — emit with hash + diff on; assert event.content_hash == event_diffs.content_hash_after for the same event.

---

## 9. Forward-compatibility considerations

### 9.1 Schema extension without migration

Three escape hatches for future tickets:
- `event_diffs.metadata_json` blob — opaque, parsed by no one in v0.9.0. Future fields can land here without ALTER TABLE.
- `event_diffs.format_version` — bumps on row-shape breaking changes. Consumers gate new field reads on `format_version >= N`.
- `actors.metadata_json` (already exists) — same pattern for actor-row extensions.

### 9.2 New `diff_format` values

`unified-v1` is the v0.9.0 format. Future formats — `unified-v2` (different hunk encoding), `semantic-go-v1` (Go AST diff), `semantic-markdown-v1` (markdown tree diff) — append to the enum without breaking. Consumers that don't recognize a format treat it as opaque: render the `diff_gz` as bytes, show metadata fields (lines_added/removed/bytes_*/) which remain semantically valid.

### 9.3 Per-root config in v0.9.x

The day-1 design uses GLOBAL diff settings. The roadmap for per-root settings:
- `internal/config/config.go` already has `Config.WatchRoots []WatchRoot`. v0.9.x adds an optional `DiffSettings *DiffSettings` field on `WatchRoot`. Nil = inherit global.
- The watcher's `effectiveRoots()` helper threads per-root settings into the per-root scan loop.
- Default and existing callsites unaffected (Nil DiffSettings = current global behaviour).
- No schema change required — settings are runtime config, not row attributes.

### 9.4 Future `--require-actor-registration` enforcement is already a knob

v0.9.0 ships the flag in OFF state. When a deployment wants strictness, flip the flag — no migration, no code change. Same for `--enforce-session-token`.

### 9.5 Future "registration audit log" table

If a deployment needs reclaim history (for compliance / forensics), a future ticket can add an `actor_registrations_audit` table that records every register/release/reclaim event. Today's slog.Warn is the audit trail; a future structured table can mirror it without affecting current behaviour.

### 9.6 OUTPUT_CONTRACT.md updates

The new `diff` envelope is added to the OUTPUT_CONTRACT documented shapes; format_version=1; the contract follows the same additive/breaking rules as the rest. Versioning policy applies.

---

## 10. Library + dependency decisions (locked)

| Need | Library | Why this one |
|---|---|---|
| Unified diff formatting + line counting | `github.com/pmezard/go-difflib` | Small (~500 LOC); zero transitive deps; MIT; port of Python difflib (known-correct semantics); produces unified output directly; widely vendored in Go ecosystem. |
| Compression of diff text | `compress/gzip` (stdlib) | Universal, no add-on dep, RFC 1952 (portable). `klauspost/compress` is faster but adds a dep; gzip stdlib is fast enough for 256 KB inputs. |
| LRU cache for content | `container/list` (stdlib) + map | ~30 LOC implementation; no external cache lib (`hashicorp/golang-lru` would add a dep for what's a trivial pattern). |
| Session token generation | `crypto/rand` (stdlib) — 8 random bytes, hex-encoded, `stk_` prefix | Cryptographically strong; no external uuid lib. |
| PID liveness check | `os.FindProcess(pid).Signal(syscall.Signal(0))` (stdlib) | Standard POSIX trick; portable; no external dep. |
| JSON encoding | `encoding/json` (stdlib) | Existing pattern. |
| Database access | `database/sql` (stdlib) + existing `modernc.org/sqlite` | Existing pattern. |
| Test framework | `testing` (stdlib) | Existing pattern. |

**Net new external dep: `github.com/pmezard/go-difflib`.** Sole addition. Justification logged in CONTRIBUTING.md ("the one-dep rule was already implicit-broken; this is the second deliberate addition, justified by diff lib not being something we want to maintain in-tree").

**`go.sum` impact:** vendor sum added; CI's existing `go mod verify` covers it.

---

## 11. Required tests

Each test below targets a specific invariant from §7 or §8 or §3. Following the existing test style (`t.TempDir()` databases, no mocks, table-driven where appropriate).

### internal/db/event_diffs_test.go
- `TestEventDiffSchemaShape` — open a fresh DB, query PRAGMA table_info(event_diffs), assert exact column list + types + NULL/PK/DEFAULT properties match §4.1.
- `TestEventDiffHasNoActorColumns` — assert no `actor*`/`producer_id`/`watch_root` columns (rule §7.1).
- `TestEventDiffStatusEnum` — insert one row per status; query by status; assert filter correctness.
- `TestEventDiffCascadeDelete` — insert event + diff; delete event; assert diff row gone (rule §8.2). Requires `PRAGMA foreign_keys=ON`.
- `TestEventDiffForeignKeyConstraint` — attempt to insert a diff with a non-existent event_id; assert FK violation.
- `TestEventDiffTransactional` — wrap the paired insert in a tx that fails on the second insert; assert the first also rolls back (rule §8.1).
- `TestPruneOldEventsCascadesDiffs` — old processed events with diffs; prune; assert both removed (rule §8.2).

### internal/db/actors_register_test.go
- `TestActorRegisterCreates` — first register populates `registered_at`/`host`/`pid`/`session_token`.
- `TestActorRegisterConflictRejected` — second register without `--reclaim` fails when prior PID is alive (`kill(pid,0)==nil`).
- `TestActorRegisterReclaimSucceedsWithWarn` — second register with `--reclaim`; succeeds; new session_token; slog.Warn captured.
- `TestActorRegisterDeadPIDSilentReclaim` — when prior PID is dead, register succeeds without `--reclaim`; no warn (rule §7.5).
- `TestActorHeartbeatStillWorks` — legacy heartbeat-only flow unchanged.
- `TestActorReleaseClears` — release blanks `registered_at`/`host`/`pid`/`session_token`; row preserved with `last_heartbeat` intact.
- `TestActorReleaseWithTokenMismatchRejected` — release with wrong `--session-token`; exit 1; state unchanged.
- `TestRegisterDoesNotRetrofitPriorEvents` — emit 3 events heartbeat-only; register; query: prior events still have no registration evidence (rule §7.4).
- `TestSessionTokenNotPersistedInEvents` — emit with `--session-token`; query event + diff rows; assert token string absent (rule §7.3).
- `TestReclaimLogsWarn` — capture slog output; reclaim; assert structured warn line includes prior+new state (rule §7.5).

### internal/diff/diff_test.go (new package — diff/ is the focused unit)
- `TestComputeDiffTextChange` — two byte slices with a line modification; assert unified output + lines_added/removed correct.
- `TestComputeDiffEmptyToContent` — empty before + populated after; assert all-additions diff.
- `TestComputeDiffContentToEmpty` — populated before + empty after (e.g., file truncated); assert all-removals diff.
- `TestComputeDiffIdentical` — before == after; assert zero hunks, zero lines.
- `TestComputeDiffUTF8` — multi-byte chars; assert hunks count by code points / lines correctly.
- `TestComputeDiffHugeLine` — single 1 MB line that changes; assert hunk truncation kicks in (rule from §5 / §8.4 — truncate at `diff_truncate_hunk_size`).
- `TestIsBinaryContent` — table of: NUL byte; >30% non-printable; pure ASCII; UTF-8; UTF-16-BOM (currently classified as binary — documented limitation).
- `TestComputeDiffPanicRecovered` — feed input known to trip the lib (constructed); defer+recover catches; returns DiffResult{Status: unavailable, ErrorReason: "lib_panic"} (rule §8.4).

### internal/catalog/snapshot_diff_test.go
- `TestSnapshotCapturesContentWhenDiffEnabled` — diff-enabled BuildSnapshot reads + retains bytes for files under max-size; binary files have bytes=nil; over-cap files have bytes=nil.
- `TestSnapshotDoesNotCaptureContentWhenDiffOff` — diff disabled = no bytes retained (no memory cost).
- `TestSnapshotContentIsNotPersisted` — saved snapshot rows in DB don't contain the bytes (they're in-memory only per §4.1 design note).

### internal/watcher/diff_integration_test.go
- `TestWatcherEmitsDiffOnModify` — diff-enabled run; create file (anchor); modify file; assert event + event_diffs row with correct diff_text + lines_added/removed.
- `TestWatcherFirstScanCreatesAnchorThenDiff` — first scan stamps no_prior_content but caches; next scan with a modify produces an available diff (rule §8.5).
- `TestWatcherBinaryFileHasBinaryStatus` — binary file modification → diff_status=binary, bytes_added=sign-preserved size delta, lines_*=0 / diff_gz=NULL.
- `TestWatcherTooLargeFileHasTooLargeStatus` — file > diff_max_size → diff_status=too_large, no diff text.
- `TestWatcherDiffOffNoDiffRows` — diff disabled → events still flow; event_diffs table empty.
- `TestHashConsistencyEventVsDiff` — hash + diff both on → events.content_hash == event_diffs.content_hash_after (rule §8.6).
- `TestDiffComputationIgnoresPayload` — emit with a deliberately misleading payload.intent; verify the diff path doesn't read payload (instrumented or asserted via "diff result identical with and without payload" — same bytes in = same diff out).

### internal/db/migrate_v08_to_v09_test.go
- `TestMigrateV08ToV09` — checked-in v0.8 fixture DB (binary file in `internal/db/testdata/`); open with v0.9 binary; assert `event_diffs` table exists with the expected schema; assert `actors` table has the 4 new columns with empty defaults; assert all existing rows preserved.

### Acceptance smoke at end-to-end
Copy-paste from §12 below — single bash script that exercises every CLI surface and asserts via `jq` / `python3` that the right rows / status codes appear.

Total new test count: ~32. Existing test count: ~62. v0.9 target: ~94 tests covering the union.

---

## 12. Acceptance criteria

(Refined from SW-AGENT-15 §6 with locked decisions baked in.)

```bash
export PATH=$HOME/.local/go/bin:$PATH
SW=./sharedwatch/.bin/sharedwatch
TMP=$(mktemp -d); WATCH=$TMP/w; DB=$TMP/q.db
mkdir -p "$WATCH"

# A1: build with v0.9 features compiled
go build -ldflags "-X main.Version=v0.9.0-rc" -o "$SW" ./sharedwatch/cmd/sharedwatch

# A2: backward compat — diff off by default
$SW --watch-path "$WATCH" --db "$DB" init
echo "x" > "$WATCH/f.md"
$SW --watch-path "$WATCH" --db "$DB" reconcile now
$SW --watch-path "$WATCH" --db "$DB" sql "SELECT COUNT(*) FROM event_diffs"
# Expects: 0 (no diffs written when diff off)

# A3: diff capture with --diff on
DB2=$TMP/q2.db; WATCH2=$TMP/w2; mkdir -p "$WATCH2"
$SW --watch-path "$WATCH2" --db "$DB2" --diff on init
echo -e "line1\nline2" > "$WATCH2/file.txt"
$SW --watch-path "$WATCH2" --db "$DB2" --diff on reconcile now
echo -e "line1\nline2 modified\nline3" > "$WATCH2/file.txt"
$SW --watch-path "$WATCH2" --db "$DB2" --diff on reconcile now
EVT=$($SW --db "$DB2" events list --type file.modified --path-glob 'file.txt' \
        --limit 1 --format json | jq -r '.rows[0].id')
$SW --db "$DB2" diff "$EVT"
# Expects: unified diff showing "-line2\n+line2 modified\n+line3"

# A4: lines_added projection
$SW --db "$DB2" events list --type file.modified --path-glob 'file.txt' \
    --fields rel_path,lines_added,lines_removed --format json
# Expects: rows with lines_added=2 (the new line2 + line3), lines_removed=1 (old line2)

# A5: binary detection
dd if=/dev/urandom of="$WATCH2/blob.bin" bs=1K count=4 2>/dev/null
$SW --watch-path "$WATCH2" --db "$DB2" --diff on reconcile now
dd if=/dev/urandom of="$WATCH2/blob.bin" bs=1K count=8 2>/dev/null
$SW --watch-path "$WATCH2" --db "$DB2" --diff on reconcile now
$SW --db "$DB2" sql "SELECT diff_status, bytes_added FROM event_diffs WHERE event_id IN (SELECT id FROM events WHERE rel_path='blob.bin' ORDER BY created_at DESC LIMIT 1)"
# Expects: status='binary', bytes_added=4096

# A6: too-large file
dd if=/dev/zero of="$WATCH2/big.txt" bs=1M count=1 2>/dev/null
$SW --watch-path "$WATCH2" --db "$DB2" --diff on --diff-max-size 65536 reconcile now
echo "edit" >> "$WATCH2/big.txt"
$SW --watch-path "$WATCH2" --db "$DB2" --diff on --diff-max-size 65536 reconcile now
$SW --db "$DB2" sql "SELECT diff_status FROM event_diffs WHERE event_id IN (SELECT id FROM events WHERE rel_path='big.txt' AND type='file.modified')"
# Expects: status='too_large'

# A7: actor register (strict)
$SW --watch-path "$WATCH2" --db "$DB2" actor register claude-1 --kind ai_agent --focus 'auth/**' --format json
# Expects: JSON with actor_id, registered_at, session_token, host, pid

# A8: actor register conflict rejected
$SW --watch-path "$WATCH2" --db "$DB2" actor register claude-1 --kind ai_agent
# Expects: exit 1, "actor claude-1 is registered by host=... pid=..."

# A9: --reclaim succeeds + warn
$SW --watch-path "$WATCH2" --db "$DB2" actor register claude-1 --kind ai_agent --reclaim --format json 2>&1
# Expects: success; new session_token; stderr contains a WARN line about reclaim

# A10: release
$SW --watch-path "$WATCH2" --db "$DB2" actor release claude-1
$SW --watch-path "$WATCH2" --db "$DB2" status --actors --json | \
  python3 -c "import json,sys; d=json.load(sys.stdin); a=[x for x in d['actors'] if x['actor_id']=='claude-1'][0]; print('cleared:', a.get('registered_at','')=='' and a.get('session_token','')=='')"
# Expects: cleared: True

# A11: legacy heartbeat-only flow unchanged
$SW --watch-path "$WATCH2" --db "$DB2" actor heartbeat claude-legacy --kind ai_agent
$SW --watch-path "$WATCH2" --db "$DB2" status --actors --json | python3 -c "import json,sys; d=json.load(sys.stdin); a=[x for x in d['actors'] if x['actor_id']=='claude-legacy'][0]; print('heartbeat-only:', a.get('registered_at','')=='')"
# Expects: heartbeat-only: True

# A12: --require-actor-registration off → unregistered emits accepted
$SW --watch-path "$WATCH2" --db "$DB2" --actor noregister test emit foo.md
# Expects: emits

# A13: --require-actor-registration on → unregistered emits rejected
$SW --watch-path "$WATCH2" --db "$DB2" --require-actor-registration --actor unknown test emit bar.md
# Expects: exit 1, "actor unknown is not registered; use `actor register` first or omit --require-actor-registration"

# A14: --enforce-session-token off → token mismatches ignored
TOK=$($SW --watch-path "$WATCH2" --db "$DB2" actor register claude-tok --kind ai_agent --reclaim --format json | jq -r .session_token)
$SW --watch-path "$WATCH2" --db "$DB2" --actor claude-tok --session-token wrong test emit foo.md
# Expects: emits (token not enforced)

# A15: --enforce-session-token on → mismatch rejected; match accepted
$SW --watch-path "$WATCH2" --db "$DB2" --enforce-session-token --actor claude-tok --session-token wrong test emit bar.md
# Expects: exit 1
$SW --watch-path "$WATCH2" --db "$DB2" --enforce-session-token --actor claude-tok --session-token "$TOK" test emit baz.md
# Expects: emits

# A16: schema migration on v0.8 DB
# (uses checked-in fixture; verifies event_diffs + actors new columns present)
cp internal/db/testdata/queue-v08.db $TMP/v08.db
$SW --watch-path "$WATCH2" --db $TMP/v08.db sql "SELECT name FROM sqlite_master WHERE type='table' AND name='event_diffs'"
$SW --watch-path "$WATCH2" --db $TMP/v08.db sql "PRAGMA table_info(actors)" | grep -E 'registered_at|host|pid|session_token'
# Expects: event_diffs table present; all 4 new actor columns shown

# A17: session_token absent from events + diffs
$SW --watch-path "$WATCH2" --db "$DB2" --actor claude-tok --session-token "$TOK" test emit secret.md
$SW --watch-path "$WATCH2" --db "$DB2" sql "SELECT id, payload_json FROM events WHERE rel_path='secret.md'" | grep -q "$TOK" && echo "LEAK" || echo "no leak"
# Expects: no leak

# A18: diff cascade-delete on retention prune
# (synthesize: insert old processed event with a diff, run retention, assert both gone)
# Covered by the unit test TestPruneOldEventsCascadesDiffs; this command is a smoke:
$SW --watch-path "$WATCH2" --db "$DB2" --diff on sql --write "DELETE FROM events WHERE id='nonexistent-but-test-cascade'"
# (more rigor in unit test)
```

Plus: `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` clean.

---

## 13. Out of scope (sharper than SW-AGENT-15)

Explicitly NOT shipping in v0.9.0:
- Full content blob store (point-in-time file reconstruction). Diffs answer the "what changed" question; full content reconstruction is a separate ticket if/when needed.
- Semantic / format-aware diff (Go AST, markdown tree). Unified line diff only.
- Patch application / inverse diff rewind.
- Per-root diff configuration. Hooks for it are designed-in (§9.3); the actual code lands in v0.9.1+ if demand arrives.
- Agent-emitted change records (payload v2 with `operation`/`summary`/`files_affected`). Parallel feature; could land alongside if scope permits but not required.
- Cross-actor merge / interleave diff views.
- Registration audit log table.
- OS-level inferred attribution (`fanotify`).
- Compression algorithms beyond gzip.
- Cross-event content reconstruction via chained diffs.
- Streaming diff API (a future `diff watch` push surface).

---

## 14. Risk / concerns

### Risk: Storage growth from diff_gz
Mitigation: opt-in (`--diff on`); per-file size cap (default 256 KB); per-hunk truncation (64 KB); ON DELETE CASCADE with retention; documented growth profile (~250 KB/day at 100 modifications/day average). Operators can `du -sh queue.db` to monitor.

### Risk: pmezard/go-difflib bugs
Mitigation: defer+recover catches panics → status='unavailable'; lib is widely vendored + stable; we cap input size at 256 KB before calling. Replacement path: the diff package interface is one function (`ComputeDiff(before, after []byte) DiffResult`); swapping libs is a contained change.

### Risk: Cache memory pressure on cold-start
Mitigation: cache is bounded by file count AND per-entry size; worst case 50 MB resident; documented; tunable via `--diff-cache-size`.

### Risk: Session token leak via SQL escape hatch
Mitigation: §7.3 invariant — tokens never written to events. The `actors.session_token` column IS readable via `sql`, which is the (already documented) trust boundary — the DB file is on disk with normal POSIX perms; agents that have shell access have FS access. Future tightening: hash the token before storage (verify by hashing the submitted token and comparing), so even DB readers can't impersonate. Filed as a v0.9.1 follow-up if demand.

### Risk: Reclaim race
Two operators racing `register --reclaim` on the same actor could both succeed if neither sees the other's commit. Mitigation: single-process invariant (we're a single-host tool; the file lock prevents two `run` daemons; the SQLite WAL serializes the writes). Race window is at most one tx, and both reclaim → last-writer-wins (deterministic).

### Risk: Diff format evolution
Mitigation: `format_version` + named formats (`unified-v1`, etc.) + consumer expected to gate on these.

### Risk: Backward compat regression
Mitigation: §3 principle #1 + tests A2, A11, A12, A14 explicitly verify the v0.8-compatible paths still work. CI gates on these.

---

## 15. Deliverables checklist (for the eventual tasklist)

### New files
- [ ] `internal/diff/diff.go` — `ComputeDiff(before, after []byte, opts DiffOptions) DiffResult`; `IsBinaryContent([]byte) bool`; truncation logic; pmezard/go-difflib integration.
- [ ] `internal/diff/diff_test.go` — 8+ tests per §11.
- [ ] `internal/db/event_diffs.go` — `EventDiffRecord` struct; `InsertEventDiff`/`GetEventDiff`/`ListEventDiffs(filter)`/`DeleteEventDiff` (cascade handled by FK).
- [ ] `internal/db/event_diffs_test.go` — 7 tests per §11.
- [ ] `internal/db/actors_register_test.go` — 10 tests per §11.
- [ ] `internal/db/migrate_v08_to_v09_test.go` + `internal/db/testdata/queue-v08.db` (fixture).
- [ ] `internal/catalog/snapshot_diff_test.go` — 3 tests per §11.
- [ ] `internal/watcher/diff_integration_test.go` — 7 tests per §11.

### Modified files
- [ ] `internal/db/db.go` `migrate()` — adds `event_diffs` table + indexes + 4 `actors` columns; verifies `PRAGMA foreign_keys=ON`.
- [ ] `internal/db/adapter.go` — interface gains: `InsertEventDiff`, `GetEventDiff`, `ListEventDiffs`, plus `InsertActorRegistration`, `ReleaseActorRegistration`.
- [ ] `internal/db/actors.go` — `RegisterActor(ctx, ActorRecord, opts{Reclaim bool}) (token, error)`; `ReleaseActor(ctx, actorID, token string) error`; PID-liveness helper.
- [ ] `internal/catalog/snapshot.go` — `Options.DiffEnabled` + `Options.DiffMaxSize`; in-memory content retention.
- [ ] `internal/catalog/ignore.go` — no change.
- [ ] `internal/watcher/service.go` — diff computation path in `scanRoot`; LRU cache wiring; paired transactional insert via new Store method `InsertEventWithDiff`.
- [ ] `internal/reconcile/reconcile.go` — same as watcher's `scanRoot`.
- [ ] `internal/config/config.go` — 7 new fields with defaults per §5.
- [ ] `internal/config/file.go` — 7 new switch cases for the new config keys.
- [ ] `cmd/sharedwatch/main.go` — 7 new flags; `diff` subcommand; `actor register|release` subcommands; `--session-token` flag on emit paths; require/enforce checks at emit time.
- [ ] `sharedwatch/CHANGELOG.md` — 2 named blocks: "Added — content diffs (SW-AGENT-16-A)" + "Added — actor registration handshake (SW-AGENT-16-B)".
- [ ] `sharedwatch/README.md` — diff example in the "For agents" section.
- [ ] `sharedwatch/config.yaml` — example with 7 new commented lines.
- [ ] `sharedwatch/docs/SCHEMA_CONTRACTS.md` — `event_diffs` table fully documented; actors new columns.
- [ ] `sharedwatch/docs/OUTPUT_CONTRACT.md` — diff envelope shape; format_version policy reaffirmed.
- [ ] `sharedwatch/CONTRIBUTING.md` — note about the new external dep (pmezard/go-difflib) and the relaxed "one dep" rule.

### Skills + design-question doc updates
- [ ] `Skills/sharedwatch-client-future/SKILL.md` — register/release in identity section; `diff` in command list.
- [ ] `Skills/sharedwatch-client-future/references/attribution-v1.md` — session_token field doc; registration handshake described.
- [ ] `Skills/sharedwatch-client/references/commands.md` — new commands + flags.
- [ ] `Skills/sharedwatch-client/references/patterns.md` — "see what teammates did" pattern uses `sharedwatch diff`.
- [ ] `Skills/sharedwatch-client/references/recovery.md` — note about stale registrations + how to release.
- [ ] `../docs/design/design-questions-20260523.md` Q2 — append "v0.9.0 — addressed via SW-AGENT-16 (diffs + register)."

### Verification
- [ ] All §11 tests pass.
- [ ] All §12 acceptance commands pass on a clean Linux host.
- [ ] gofmt/vet/test clean.
- [ ] Dogfood pass re-run: confirm no regression in the 18 scenarios; new scenario "S19 — diff capture across a refactor" added to `../docs/dogfood/test_dogfood.md`.

### Go module
- [ ] `go get github.com/pmezard/go-difflib@latest` → `go.mod` + `go.sum` updated; vendor sum verified.

---

## 16. References

- `tickets/ticket-content-diff-and-actor-registration-20260523_090258.md` — SW-AGENT-15, the initial framing with open questions
- `../docs/design/design-questions-20260523.md` — Q1 (git replacement) + Q2 (touch vs understanding) — the conceptual basis
- `../docs/design/future-features-20260523.md` features #4 + #9 — overlapping pre-thinking
- `../docs/design/multi-agent-discussion-20260522.md` §6 — original gap analysis
- `internal/catalog/snapshot.go` — where content capture hooks in
- `internal/db/db.go` `migrate()` — additive migration block
- `internal/db/actors.go` — current actors registry (extended here)
- `internal/watcher/service.go` `scanRoot` — diff computation site
- `tickets/ticket-multi-folder-watching-20260520_101921.md` — reference for ticket structure
- `Skills/sharedwatch-client-future/references/leases.md` — current actor section to be extended
- `docs/OUTPUT_CONTRACT.md` — versioning policy applied to the new `diff` envelope
- `https://github.com/pmezard/go-difflib` — the new dep; MIT licensed; pinned to specific tag at vendor time

---

## 17. Definition of done

All of:

1. Every §12 acceptance command works on a clean Linux host with the v0.9-built binary.
2. Every §11 test passes; `gofmt -l .` empty; `go vet ./...` clean; `go test ./... -count=1 -race` clean (CI's -race gate).
3. Schema migration verified against the v0.8 fixture DB (`internal/db/testdata/queue-v08.db`); old rows preserved; new tables/columns appear with the documented defaults.
4. Backward compatibility verified: every test case in v0.8.0 still passes; the `--diff off` and no-register code paths are byte-identical to v0.8 behaviour (single-root status JSON, single-root events list output, default ignore patterns, etc.).
5. `CHANGELOG.md`, `README.md`, `config.yaml`, `SCHEMA_CONTRACTS.md`, `OUTPUT_CONTRACT.md`, `CONTRIBUTING.md` all updated per §15.
6. All Skills updated per §15. Loading either `sharedwatch-client` or `sharedwatch-client-future` against a v0.9-deployed binary produces accurate guidance for an AI consumer.
7. Dogfood pass re-run; new scenario S19 (diff capture across a refactor) lands in `../docs/dogfood/test_dogfood.md`; all 19 scenarios green.
8. The `../docs/design/design-questions-20260523.md` Q2 carries a "v0.9.0 — addressed via SW-AGENT-16" footnote.
9. The branch can credibly carry the release note: *"sharedwatch v0.9.0 — answers not just what files changed and who touched them, but what changed inside the file, with a stricter identity handshake for cooperative writers and a versioned, owner-blind, transactionally-consistent diff store designed for forward-compatible extension."*
10. A worklog entry in the next session's `tasklist_<bashdate>.md` records what landed, what was deferred, library-dep notes, and any surprises from the diff cache / binary detection / migration paths.
