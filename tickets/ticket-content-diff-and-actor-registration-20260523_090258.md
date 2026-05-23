# TICKET: Content diffs + first-class actor registration — close the "what is actually happening?" gap

**ID:** SW-AGENT-15
**Filed:** 2026-05-23 09:02 UTC
**Filed by:** claude (this session)
**Status:** open, not yet claimed
**Target:** sharedwatch v0.9.x
**Estimated:** ~3.5 focused engineer-days (2 for diffs, 1 for actor registration, 0.5 for skills + docs)

---

## 1. Context — why this exists

Two related design questions surfaced during the v0.8.0 post-ship review and are captured in `../docs/design/design-questions-20260523.md`:

- **Q1 (git replacement):** sharedwatch records *that* files changed, not *what* changed. Without bytes (or at least a diff), the journal cannot tell a consumer "what was different between event N and event N+1." For the swarm-coordination use case, this is the load-bearing gap.
- **Q2 (touch events vs. understanding):** Touch + actor + free-form `payload.intent` is enough for *triggers* (alarm, wake-up, coordination signal) but thin for *understanding what teammates have been doing*. The watcher needs to either capture the diff itself OR have writers reliably annotate. The current model leans on optional cooperation; it needs a stronger floor.

The user framing: *"without that users might as well use something else; remember the main intent is multi-swarm coordination; how can they understand what agents have been doing?"*

Companion design constraint: actor identification today is heartbeat-only and silently tolerant (the `actor_kind` change between heartbeats just logs a warn). For attribution to be reliable, agents (and other cooperative writers) should explicitly *register* at startup so the journal has a strong handshake, not a guess.

Back-refs:
- `../docs/design/design-questions-20260523.md` Q1, Q2
- `../docs/design/future-features-20260523.md` features #4 (read receipts) + #9 (content blob store) — this ticket subsumes #9 with a narrower "diffs only, not full content" approach
- `../docs/design/multi-agent-discussion-20260522.md` §6 ("Are we collecting enough information?")
- `internal/db/actors.go` — existing actors registry (added in SW-AGENT-8) — extended here
- `internal/catalog/snapshot.go` — `BuildSnapshot` already reads each file to compute hashes; the same pass can capture content for diffing

---

## 2. The product problem in concrete terms

A swarm operator sets up sharedwatch on a shared workspace. Several writers operate in the watched tree:

- `claude-spec` (an AI agent) writes spec docs
- `claude-code` (an AI agent) writes implementation
- `human-mike` (a human in vim) reviews and edits
- `npm run build` (a build tool) regenerates `dist/`

A reviewer agent subscribes to the journal and wants to know:
- *"What did claude-spec change in the spec doc in the last hour?"*
- *"What did claude-code add to widget.go (lines added vs removed)?"*
- *"Did human-mike's edits override claude-code's refactor?"*
- *"What did the build system regenerate?"*

Today the journal can answer "WHICH files were touched, WHEN, by which process (`producer_id`), by which actor if cooperative (`payload.actor`)." It cannot answer "WHAT changed inside the file." The reviewer agent has to open each file at its current state and guess — which only works for the latest change, not historical ones.

The fix is two-part:

**Part A — Content diffs (watcher-side, no cooperation required).** The watcher captures enough state to compute a unified diff between consecutive `file.modified` events on the same path. Stored compressed alongside the event. Available via `sharedwatch diff <event-id>` and `sharedwatch content show <event-id>` (for the latter, we store reconstructable state, not necessarily full content for every event). Binary files are detected and recorded as `+N bytes binary` without diff text.

**Part B — Actor registration handshake (cooperative writers).** Writers (agents, wrappers, hooks) call `sharedwatch actor register <id> --kind ... --label ... --focus ...` once at startup. This is stricter than heartbeat — it fails on conflict (unless `--reclaim`), records `registered_at` + `host` + `pid`, and gives the writer a stable identity for subsequent emits. Heartbeats then refresh `last_heartbeat`. Release on shutdown (or TTL expiry).

The two parts close different halves of the same gap. Part A closes "what did any writer do" regardless of cooperation. Part B closes "who is this writer, reliably" for the cooperative subset.

---

## 3. In scope — Part A: Content diffs

### A.1 — Snapshot extension to capture content

`catalog.BuildSnapshotWithOptions` already opens each file to compute SHA-256. Extend the snapshot to **optionally retain content bytes** alongside the FileState (in-memory only, not persisted to the snapshots table):

```go
type FileState struct {
    RelPath string    `json:"rel_path"`
    Path    string    `json:"path"`
    Size    int64     `json:"size"`
    MTime   time.Time `json:"mtime"`
    Hash    string    `json:"hash,omitempty"`
    // NEW: only populated when DiffEnabled and the file is under DiffMaxSize
    // and detected as text. Bytes themselves are NOT serialized into the
    // snapshots table — they're held in-memory just long enough to compute
    // the diff against the next snapshot.
    content []byte    `json:"-"`
}

type Options struct {
    Includes      []string
    HashEnabled   bool
    HashMaxSize   int64
    // NEW
    DiffEnabled   bool
    DiffMaxSize   int64 // bytes; default 256 KB
}
```

### A.2 — Diff computation during DiffSnapshots

When the watcher diffs two consecutive snapshots and detects `file.modified` on a path that exists in both:

- If both old and new content are available in memory: compute a unified diff (use a small Go diff library — `github.com/sergi/go-diff` or `github.com/pmezard/go-difflib` — pick one with no transitive deps if possible)
- Compress the diff text with gzip
- Emit the event with diff metadata; insert a paired row in the new `event_diffs` table

If content is unavailable for either side (size cap exceeded, binary detected, snapshot was loaded from DB without bytes), the event is still emitted but `event_diffs` carries `diff_status='unavailable'` with a reason code.

### A.3 — New table `event_diffs`

```sql
CREATE TABLE IF NOT EXISTS event_diffs (
  event_id TEXT PRIMARY KEY,
  diff_status TEXT NOT NULL,           -- 'available' | 'binary' | 'too_large' | 'no_prior_content' | 'unavailable'
  diff_format TEXT NOT NULL DEFAULT '', -- 'unified-v1' for text diffs; '' for non-available
  lines_added INTEGER NOT NULL DEFAULT 0,
  lines_removed INTEGER NOT NULL DEFAULT 0,
  bytes_added INTEGER NOT NULL DEFAULT 0,    -- for binary: + size delta; for text: byte length of added text
  bytes_removed INTEGER NOT NULL DEFAULT 0,
  diff_gz BLOB,                        -- gzipped unified diff text; NULL when status != 'available'
  content_hash_before TEXT,
  content_hash_after TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_event_diffs_status ON event_diffs(diff_status);
```

Separate table (not column on events) because:
- Diffs are large-ish (avg 1-5 KB compressed); keeping them off the hot event row preserves `events list` performance.
- Not all events have diffs (deletes, creates with no prior, binaries).
- Future: same table can hold richer derived data (semantic summaries from agent emit, format-aware deltas).

### A.4 — Reading diffs

```bash
# Show the diff for one event (rendering: ungzip + unified-diff print)
sharedwatch diff <event-id> [--format text|json]

# Show the latest content reconstructable from anchors + chained diffs
# (deferred to A.7; if not implemented, fall back to "read the file at the
#  current FS state and back-apply the inverse diffs to walk back in time")
sharedwatch content show <event-id>

# Stats overlay on events list — when diff is available, include lines_added/removed
sharedwatch events list --fields rel_path,type,lines_added,lines_removed,actor,created_at
```

### A.5 — CLI flags

```
--diff on|off                 default off (opt-in for storage cost). Equivalent to
                              setting DiffEnabled=true and DiffMaxSize=default.
--diff-max-size <bytes>       default 256 KB. Files above this size get
                              diff_status='too_large'.
--diff-cache-size <count>     default 200. Number of file contents held in memory
                              between scans for diff computation.
```

Config keys: `diff_enabled`, `diff_max_size`, `diff_cache_size`.

### A.6 — Detection of binary files

Use the first 8 KB of the file: if it contains a NUL byte or `>30%` non-printable characters (excluding standard whitespace), classify as binary. For binary files, `event_diffs` carries `diff_status='binary'` with `bytes_added` = size delta only.

### A.7 — Content reconstruction (DEFERRED to A follow-up — out of scope for this ticket)

Walking arbitrary historical content (i.e., "what did the file look like at event N-5") requires either full anchors + diff chains OR a content blob store. This ticket ships diff-per-modification; reconstruction is a v0.9.x add-on that depends on what's been built here. The decision to defer is deliberate: the diff alone answers most "what did teammates do" questions; full reconstruction is heavier and can land after we see usage signal.

---

## 4. In scope — Part B: Actor registration handshake

### B.1 — Schema extension to `actors`

Add columns (idempotent migration):

```sql
ALTER TABLE actors ADD COLUMN registered_at TEXT NOT NULL DEFAULT '';
ALTER TABLE actors ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE actors ADD COLUMN pid INTEGER NOT NULL DEFAULT 0;
ALTER TABLE actors ADD COLUMN session_token TEXT NOT NULL DEFAULT '';
```

`registered_at`: timestamp of the explicit `register` call (NULL/empty when the actor exists from a heartbeat-only path).
`host` + `pid`: identifies the live process holding the registration — used for conflict detection.
`session_token`: opaque ~16-char random; agents include it in subsequent emits (via a new `--session-token` flag or env) to verify identity. Optional in v0.9; agents that don't include it work as today.

### B.2 — `sharedwatch actor register <actor-id>` subcommand

```bash
sharedwatch actor register <actor-id> \
  [--kind human|ai_agent|automation] \
  [--label "<human readable>"] \
  [--focus <path-glob>] \
  [--reclaim] \
  [--metadata '<json>']

# Returns JSON on success:
{
  "actor_id": "claude-coordinator-1",
  "registered_at": "2026-05-23T10:00:00.123Z",
  "session_token": "stk_a1b2c3d4e5f6g7h8",
  "host": "claude-host-7",
  "pid": 12345
}
# Exit 1 on conflict (different live PID already holds the actor_id),
# unless --reclaim is passed (then the prior registration is invalidated).
```

Strictness: if the actor_id is currently held by a live process (host+pid combination, and that PID is alive via kill(pid, 0)), `register` fails with `exit 1` and a clear error message including the holder's host+pid. `--reclaim` overrides this for legitimate handoff cases (e.g., an agent crashed and restarted; the operator wants to take over).

### B.3 — Relationship to existing `actor heartbeat`

| Subcommand | Purpose | Strictness |
|---|---|---|
| `actor register` | Explicit startup handshake; claim the actor_id | strict: conflicts → exit 1 unless --reclaim |
| `actor heartbeat` | Refresh `last_heartbeat`; update light metadata | tolerant: just upserts; warn on kind change |
| `actor release` | Explicit deregister at shutdown | NEW; clears session_token, marks registered_at NULL |

`heartbeat` continues to work for callers that don't want the handshake ceremony. New best-practice: `register` once at startup; `heartbeat` every N min; `release` at shutdown.

### B.4 — Reclaim semantics

When `--reclaim` is passed and the prior PID is still alive: warn loudly and proceed. Don't refuse — that would leave the system stuck if a misbehaving agent never releases.

When the prior PID is dead: silent reclaim (no warning needed; the prior registration is effectively orphaned).

### B.5 — Session token usage

Optional in v0.9; gives a future stricter mode:

```bash
sharedwatch run --actor claude-X --session-token stk_xyz
sharedwatch test emit foo.md --actor claude-X --session-token stk_xyz
```

When set, the daemon validates that the supplied `session_token` matches the current registration's. Mismatch → reject the emit with a clear error (vs. today's silent tolerance).

If `--session-token` is not set, behavior unchanged — emits succeed regardless. Backwards compatible.

### B.6 — Skill update

`Skills/sharedwatch-client-future/references/leases.md` already describes heartbeat. Add a sibling section describing `register` as the recommended startup ceremony; mark heartbeat as still-supported but secondary. Skills/sharedwatch-client/SKILL.md identification section should describe the register/heartbeat/release lifecycle.

---

## 5. Out of scope (for this ticket)

- **Full content blob store / history reconstruction.** A.7 is intentionally deferred; this ticket gives diffs per-modification, not arbitrary historical state. A follow-up ticket can add a content store if usage shows it's needed.
- **Format-aware semantic diff** (e.g., Go AST diff, markdown-tree diff). Unified line diff is the baseline; semantic diffs are a future ticket.
- **Agent-emitted semantic summary alongside the diff** (`--summary "moved jwt_validate to auth/jwt.go"`). The payload v2 keys (operation/summary/lines_added/etc.) from the Q2 design discussion are a parallel feature that COULD land alongside this; if they do, the renderer should prefer the agent's summary when available and fall back to the watcher's lines_added/removed.
- **Diff replay / patch application.** The diff is readable for understanding; we don't ship a "rewind the file" feature.
- **Cross-actor diff merging** (showing diffs that interleave from multiple actors). Each event's diff is computed against the prior modification's content regardless of actor.
- **Watcher-side actor inference** (`fanotify`-based PID-to-actor mapping). This ticket leaves that as the long-term Layer 3 from the design-questions Q2; register + reclaim is the Layer 1 answer.

---

## 6. Acceptance criteria

Copy-paste against a fresh checkout after merge.

```bash
# A1: diff capture with --diff on
sharedwatch --watch-path $WATCH --db $DB --diff on init
echo "v1" > $WATCH/login.go
sharedwatch --watch-path $WATCH --db $DB reconcile now
echo -e "v1\nv2 line" > $WATCH/login.go
sharedwatch --watch-path $WATCH --db $DB reconcile now

# Should show 2 events for login.go, second one with a diff:
sharedwatch --watch-path $WATCH --db $DB events list --path-glob 'login.go' \
  --fields rel_path,type,lines_added,lines_removed --format jsonl

# Expects: a row with lines_added=1, lines_removed=0 for the modify event.

# A2: diff retrieval
EVT=$(sharedwatch --watch-path $WATCH --db $DB events list --path-glob 'login.go' \
        --type file.modified --limit 1 --format json | jq -r '.rows[0].id')
sharedwatch --watch-path $WATCH --db $DB diff $EVT

# Expects: unified diff showing "+v2 line".

# A3: binary file detection
dd if=/dev/urandom of=$WATCH/blob.bin bs=1K count=4
sharedwatch --watch-path $WATCH --db $DB reconcile now
dd if=/dev/urandom of=$WATCH/blob.bin bs=1K count=8
sharedwatch --watch-path $WATCH --db $DB reconcile now
sharedwatch --watch-path $WATCH --db $DB events list --path-glob 'blob.bin' \
  --fields type,lines_added,lines_removed,bytes_added --format jsonl

# Expects: modify event with diff_status='binary', bytes_added=4096
# (or appropriate size delta), lines_added=0, lines_removed=0.

# A4: too-large file
dd if=/dev/urandom of=$WATCH/big.txt bs=1M count=1
sharedwatch --watch-path $WATCH --db $DB --diff-max-size 65536 reconcile now
echo "edit" >> $WATCH/big.txt
sharedwatch --watch-path $WATCH --db $DB --diff-max-size 65536 reconcile now
sharedwatch --watch-path $WATCH --db $DB diff <event-id>

# Expects: diff_status='too_large'; no diff text returned; clean error message.

# A5: actor register (strict)
sharedwatch --watch-path $WATCH --db $DB actor register claude-1 --kind ai_agent --focus 'auth/**'
# → JSON with actor_id, registered_at, session_token

sharedwatch --watch-path $WATCH --db $DB actor register claude-1 --kind ai_agent
# → exit 1, "actor claude-1 already registered by host=X pid=Y"

# A6: actor register with --reclaim
sharedwatch --watch-path $WATCH --db $DB actor register claude-1 --kind ai_agent --reclaim
# → succeeds; new session_token; prior registration invalidated.

# A7: actor release
sharedwatch --watch-path $WATCH --db $DB actor release claude-1
sharedwatch --watch-path $WATCH --db $DB status --actors --json | jq '.actors[] | select(.actor_id=="claude-1")'
# → registered_at empty; row still present (kept for last_heartbeat)

# A8: legacy heartbeat-only flow still works
sharedwatch --watch-path $WATCH --db $DB actor heartbeat claude-legacy --kind ai_agent
# → succeeds as today; registered_at empty in the row.

# A9: backwards compat — diff off by default
sharedwatch --watch-path $WATCH2 --db $DB2 init    # no --diff flag
echo "x" > $WATCH2/f.md
sharedwatch --watch-path $WATCH2 --db $DB2 reconcile now
# events list works exactly as v0.8.0; no event_diffs rows produced.

# A10: schema migration on a v0.8 DB
# (open a pre-existing v0.8 DB with the new binary; assert that event_diffs
#  table exists, actors gained registered_at/host/pid/session_token columns,
#  old rows have empty values, old queries still work.)
```

Plus: `gofmt -l .` clean, `go vet ./...` clean, `go test ./...` clean.

---

## 7. Required tests

- `internal/db/event_diffs_test.go`:
  - `TestEventDiffInsert`: insert a diff row, read it back, assert fields preserved
  - `TestEventDiffStatusEnum`: insert with each status value; assert filter queries by status work
  - `TestEventDiffCascadeDelete`: delete an event; assert event_diffs row also removed
- `internal/catalog/diff_test.go`:
  - `TestDiffTextChange`: two snapshots with a text-file modification produce a unified diff with correct lines_added/removed
  - `TestDiffBinaryDetection`: file with NUL byte classified as binary; size delta recorded; diff_text empty
  - `TestDiffTooLarge`: file above DiffMaxSize gets status='too_large'; no diff text
  - `TestDiffNoPriorContent`: first scan after diff-enabled has no prior cached content; status='no_prior_content'
  - `TestDiffCacheLRU`: with cache-size=2, third file evicts the LRU; that file's next modify has status='no_prior_content'
- `internal/db/actors_register_test.go`:
  - `TestActorRegisterCreates`: first register populates registered_at/host/pid/session_token
  - `TestActorRegisterConflictRejected`: second register without --reclaim fails when prior PID is alive
  - `TestActorRegisterReclaim`: with --reclaim, second register succeeds; new session_token issued
  - `TestActorRegisterDeadPID`: when prior PID is dead, register silently reclaims (no error, no warning)
  - `TestActorHeartbeatStillWorks`: legacy heartbeat-only flow continues to work; no registered_at populated
  - `TestActorReleaseClears`: release blanks registered_at + session_token; row remains
- `internal/app/diff_integration_test.go`:
  - end-to-end: configure with diff on; create file; reconcile; modify file; reconcile; verify event_diffs row with correct content
- Schema-migration regression:
  - `TestMigrateAddDiffsAndRegister`: open a v0.8 DB fixture; assert all new columns/tables present with default values; old rows preserved.

---

## 8. Open questions

These should be resolved before claim or as the first task of implementation.

1. **Which diff library?** Candidates: `github.com/sergi/go-diff/diffmatchpatch` (mature, well-tested, MIT, no deps); `github.com/pmezard/go-difflib` (port of Python's difflib, BSD, no deps, simple); roll our own line-by-line LCS. **Recommended:** `pmezard/go-difflib` — small, transitively-clean, produces unified output directly. The project's existing one-dep rule (modernc.org/sqlite only) is broken either way; pick the smallest add.

2. **In-memory cache size default — 200 files reasonable?** Could be too small for very wide repos (a 5000-file `src/` tree would have stale cache for most files). Could be too large for memory-constrained hosts. **Recommended:** start at 200, document tuning, expose `--diff-cache-size` for override. Future: per-root caches sized proportionally.

3. **Should `diff_status='no_prior_content'` be a one-time-only condition, or should the watcher try to anchor every new file?** Anchoring (caching content the first time a file is seen) means subsequent modifies have diffs. Not anchoring means the very first modify after restart shows no diff. **Recommended:** always anchor on first-seen of each file (subject to size/binary cap), so the steady state has diffs for nearly all events. Cost: one extra read per cold-start file (already done for hashing if `--hash on`, so often free).

4. **Should `event_diffs.diff_gz` compression be optional?** Diff text is human-readable; storing uncompressed is debuggable but ~5× larger. **Recommended:** always gzip in the table; `sharedwatch diff <id>` ungzips for display. Add a debug flag `--no-compress` to write uncompressed for diagnostic builds only.

5. **Should registration be required, or just recommended?** If required, all emits without a registered session fail — strong identity guarantee but breaks existing single-tenant flows. If recommended, attribution stays best-effort. **Recommended:** recommended in v0.9; introduce a future `--require-registration` config knob if a deployment wants strictness.

6. **Should `session_token` be enforced even when registration is recommended-not-required?** I.e., if an actor IS registered, must subsequent emits include the matching token? **Recommended:** No for v0.9 — token is opt-in for stricter environments. Bumping to required-when-registered breaks too many cooperative-but-token-unaware emit paths.

7. **What happens to in-flight events when an actor releases or is reclaimed?** Events already in the journal are not retroactively re-attributed or removed. Releasing only affects future emits. Document this explicitly.

8. **Diff truncation for very long lines.** A single 10 MB log file line that changes would produce a diff with a 10 MB hunk. **Recommended:** cap individual hunk size (e.g., 64 KB); for over-cap hunks, store `<truncated, original Δ N bytes>` placeholder + the unaffected hunks.

---

## 9. Risk / concerns

- **Storage cost.** Diffs are small (1–5 KB avg) but accumulate. A repo with 100 modifications/day = ~250 KB/day = ~90 MB/year. Acceptable for most cases; problematic for very high-churn or large files. Mitigation: `--diff-max-size`, file-type filter (skip lockfiles, generated code), retention-aware pruning (delete `event_diffs` for events past `retention_days`).
- **In-memory cache pressure.** Holding content for ~200 files at 100 KB avg = ~20 MB resident. Bounded but worth monitoring under cold-start. Mitigation: LRU eviction; documented `--diff-cache-size` knob.
- **Binary detection false positives.** UTF-16 text files (BOM + nulls) would be classified as binary. Mitigation: explicit BOM check; document the heuristic in skill references.
- **Diff library dependency.** Adds one external dep; we've held to `modernc.org/sqlite` only. Mitigation: pick smallest possible (pmezard/go-difflib is ~500 LOC, no transitive deps); document the addition in CONTRIBUTING.md.
- **Actor registration leaks on agent crash.** If an agent dies without calling `release`, its row stays "registered" until the next reclaim. The `kill(pid, 0)` check in `register --reclaim` handles this; a future reaper could auto-release dead-PID registrations (probably v0.9.x follow-up).
- **Session-token theft.** If a malicious process can read `runtime_state` / `actors` table, it can present a valid token. Mitigation: this is not a security boundary (already documented for the `sql --write` flag — the DB is on disk with normal POSIX perms). Document the trust model explicitly.
- **Backward compatibility on `event_diffs` consumers.** New table — no existing consumer reads it, so additive. The `events list` output gains `lines_added`/`lines_removed` only when `--fields` requests them; default output unchanged.

---

## 10. Deliverables checklist (for the eventual tasklist)

- [ ] `catalog.FileState.content` field (unexported) + `Options.DiffEnabled` + `Options.DiffMaxSize`
- [ ] `catalog.BuildSnapshotWithOptions` reads bytes when diff-enabled + file under cap
- [ ] `catalog.IsBinaryContent(bytes []byte) bool` helper
- [ ] `internal/catalog/diff.go` — `ComputeDiff(oldBytes, newBytes []byte) (DiffResult, error)` returning unified diff text + lines_added/removed
- [ ] In-memory LRU cache for prior content (bounded by `--diff-cache-size`)
- [ ] `watcher.DiffSnapshots` / `reconcile.reconcileRoot` produce events PLUS optional `db.EventDiffRecord` per event
- [ ] `internal/db/event_diffs.go` — `EventDiffRecord` struct + `InsertEventDiff` / `GetEventDiff` / `ListEventDiffs(EventDiffFilter)`
- [ ] Schema migration: new table + indexes; `actors` gains 4 new columns
- [ ] Adapter interface gains 3 methods (InsertEventDiff, GetEventDiff, ListEventDiffs)
- [ ] CLI: `sharedwatch diff <event-id> [--format text|json]`
- [ ] CLI: `events list --fields lines_added,lines_removed,bytes_added,bytes_removed` projects from `event_diffs` via LEFT JOIN
- [ ] CLI: `sharedwatch actor register <id> [...]` + `sharedwatch actor release <id>`
- [ ] CLI: global flags `--diff on|off`, `--diff-max-size`, `--diff-cache-size`
- [ ] Config: `diff_enabled`, `diff_max_size`, `diff_cache_size` parseable in config.yaml
- [ ] All tests per §7
- [ ] CHANGELOG `[Unreleased]` entries — one block for diffs, one for register
- [ ] README — add diff example to "for agents" section
- [ ] `docs/SCHEMA_CONTRACTS.md` — document `event_diffs` table + actors registration columns
- [ ] `docs/OUTPUT_CONTRACT.md` — document `sharedwatch diff` envelope
- [ ] Skills updates:
  - `Skills/sharedwatch-client-future/SKILL.md` — register/release in the identity section
  - `Skills/sharedwatch-client-future/references/attribution-v1.md` — session_token field
  - `Skills/sharedwatch-client/references/commands.md` — `diff` + `actor register|release` documented
  - `Skills/sharedwatch-client/references/patterns.md` — "see what teammates did" pattern updated to use diffs
- [ ] Dogfood pass §6 acceptance commands run end-to-end on a clean host
- [ ] `../docs/design/design-questions-20260523.md` Q2 — update with "v0.9.x landed this" note when this ships

---

## 11. References

- `../docs/design/design-questions-20260523.md` — Q1 + Q2 are the framing
- `../docs/design/future-features-20260523.md` features #4 (read receipts) + #9 (content blob store) — this ticket scopes down #9 to diffs-only
- `internal/catalog/snapshot.go` — `BuildSnapshotWithOptions` is where content capture hooks in
- `internal/db/actors.go` — existing actors registry, gains registration columns + helpers
- `internal/db/db.go` `migrate()` — additive schema migration block
- `internal/watcher/service.go` `scanRoot` — diff computation happens here, alongside event emission
- `Skills/sharedwatch-client-future/references/leases.md` — current actor section, updated to add register
- `tickets/ticket-multi-folder-watching-20260520_101921.md` — reference for ticket structure + acceptance-criteria style

---

## 12. Definition of done

- All §6 acceptance commands work against a real DB on a clean Linux host.
- All §7 tests pass; `gofmt -l .` clean; `go vet ./...` clean; `go test ./...` clean.
- Schema migration tested against a v0.8 fixture DB; old rows preserved; new columns/tables present.
- Backward compatibility verified: a v0.8.0 single-root call path with no `--diff` flag and no `--register` ceremony works exactly as before.
- Skills updates merged; commands + envelopes documented.
- CHANGELOG carries two named additions (Added — content diffs (SW-AGENT-15A) + Added — actor registration handshake (SW-AGENT-15B)).
- `../docs/design/design-questions-20260523.md` updated with a "v0.9.x — landed via SW-AGENT-15" footnote on Q2.
- A worklog entry in the next `tasklist_<bashdate>.md` records what shipped, what was deferred, and any surprises (especially around diff library dependency, cache tuning, or registration-conflict edge cases).
- The branch report or follow-up release notes can credibly claim: "sharedwatch can now answer not just *what files changed and who touched them*, but *what changed inside the file*, with a stricter identity handshake for cooperative writers."
