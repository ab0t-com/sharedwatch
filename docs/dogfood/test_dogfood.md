# sharedwatch — dogfood test scenarios

**Date:** 2026-05-22
**Status:** design doc for a dogfooding pass (not yet executed)
**Purpose:** capture realistic file-activity patterns a human or AI agent would actually produce, run them against sharedwatch, and observe what the journal looks like. Each scenario is a *miniature reality test* — does the tool surface what an agent would need to make sense of this?

---

## Setup

```bash
# Dedicated watched folder for dogfood — isolated from real work.
export DOG=/tmp/sharedwatch-dogfood
export WATCH=$DOG/watch_this
export DBP=$DOG/dogfood.db
export QUARANTINE=$DOG/quarantine     # "soft-delete" destination, OUTSIDE the watched tree
export ARCHIVE=$DOG/archive           # for archived DBs / one-shot artifacts
mkdir -p "$WATCH" "$QUARANTINE" "$ARCHIVE"

# Build & init.
make -C sharedwatch build
SW="./sharedwatch/.bin/sharedwatch --watch-path $WATCH --db $DBP"

$SW init
$SW run &       # background; stop at end with `kill %1`

# For attribution-rich scenarios, agents identify themselves via:
#   $SW --actor claude-1 --session sess-dog-2026-05-22 --task <scenario-N>
# (where the --actor / --session / --task flags are the proposed L1 payload helpers
# from sharedwatch-disclosure-attribution-discussion-20260522.md; today, the same
# effect is achieved by `--producer-id` + custom `payload_json`.)
```

### Safety rules for this dogfood (read once, follow always)

This pass is designed to be **safe to run under a smaller model with limited judgment**. Three hard rules:

1. **No `rm -rf` anywhere.** Never. Not even on `$WATCH`. Smaller models occasionally misexpand variables; the cost of a wrong expansion is total. Every "delete" in this document moves files to `$QUARANTINE` instead.
2. **No folder deletes.** Only file-level operations. Directories are created but never removed by the dogfood scripts. If a scenario needs a clean folder, move its files out (see `safe_clear_files` below); leave the directory in place.
3. **No `sudo`, no writes outside `$DOG`.** All scenario actions stay inside `$DOG/...`. If a scenario *would* need a system-level write to be realistic, the document describes the behavior instead of executing it.

The helper functions below implement the move-instead-of-delete pattern. Use them everywhere; never call `rm`.

```bash
# safe_clear_files — move every regular file under $WATCH into $QUARANTINE,
# preserving relative paths. Leaves all directories in place.
safe_clear_files() {
  find "$WATCH" -type f -print0 | while IFS= read -r -d '' f; do
    rel="${f#$WATCH/}"
    dest="$QUARANTINE/$(date +%s)-$$/${rel}"
    mkdir -p "$(dirname "$dest")"
    mv -- "$f" "$dest"
  done
}

# safe_remove_file <path>  — single-file move-to-quarantine.
safe_remove_file() {
  local f="$1"
  [ -f "$f" ] || return 0
  local rel="${f#$WATCH/}"
  local dest="$QUARANTINE/$(date +%s)-$$/${rel}"
  mkdir -p "$(dirname "$dest")"
  mv -- "$f" "$dest"
}

# safe_archive_db — move the dogfood DB to $ARCHIVE so the next scenario
# starts with a fresh DB. NEVER delete the DB file outright.
safe_archive_db() {
  [ -f "$DBP" ] || return 0
  mv -- "$DBP" "$ARCHIVE/$(date +%s)-dogfood.db"
}
```

After each scenario, two verification calls capture what the journal observed:

```bash
$SW events list --since 5m --fields id,type,rel_path,actor,ts --format jsonl
$SW status --json | jq .
```

A clean tear-down between scenarios (no `rm`):
```bash
safe_clear_files && $SW reconcile now    # baseline before next scenario
```

---

## Scenario index

| # | Title | What it exercises |
|---|---|---|
| 1 | Single new file | Cold-create, basic path capture |
| 2 | In-place text edit | `file.modified`, mtime change |
| 3 | Two edits inside coalesce window | Coalesce burst-suppression |
| 4 | Two edits straddling coalesce window | Coalesce boundary behavior |
| 5 | Rename within the watched root | `file.renamed` heuristic |
| 6 | Move directory subtree | Bulk rename pairing |
| 7 | Editor swap-file pattern (vim) | Atomic-write rename heuristic |
| 8 | Bulk find-and-replace via sed | High-velocity modify cascade |
| 9 | Two-agent handoff (spec → code) | Causality + addressee in payload |
| 10 | Concurrent edits in active mode | Mode interaction + cadence |
| 11 | Restart safety mid-batch | Recovery from `processing` status |
| 12 | Large file above HashMaxSize | Hash skip semantics |
| 13 | Ignored patterns (`.git`, `*.swp`) | Filter correctness |
| 14 | Cold start with 50 pre-existing files | First-scan emission |
| 15 | Reconcile catches missed event | Drift-recovery path |
| 16 | Burst of 200 events / 60s | Queue depth + throttling |
| 17 | Cross-actor churn on same file | Conflict surfacing |
| 18 | Symlink to outside the root | Boundary integrity |
| 19 | (reserved for SW-AGENT-16 content-diff dogfood) | — |
| 20 | CLI polish: `--quiet`, `--dry-run`, JSON errors (v0.0.6) | UX guarantees + structured errors |
| 21 | First-session canonical workflow with env vars (v0.0.7) | The ergonomics promise: export once, never type flags |
| 22 | Stale lock file recovery (v0.0.7) | What `stop` and `run` do when a daemon died hard |
| 23 | Misbehaving agent — text vs JSON error parity (v0.0.7) | Error shape consistency across format-aware commands |
| 24 | Multi-agent lease warning (v0.0.7) | Watcher logs structured warning on cross-actor writes inside a lease |

---

## 1 — Single new file

**Goal:** confirm the simplest possible event flows end-to-end.

**Actions:**
```bash
echo "hello, sharedwatch" > "$WATCH/hello.md"
sleep 12              # > passive interval is 10m; for the test, force a tick
$SW reconcile now     # or rely on the passive scan
```

**Expected events (1):**
```jsonl
{"type":"file.created","rel_path":"hello.md","source":"watcher|reconciler","size":18,"actor":""}
```

**What this exercises:** `BuildSnapshot` against a previously-empty tree; first-time `SaveSnapshot`; basic event insertion.

---

## 2 — In-place text edit

**Goal:** `file.modified` event fires when content + mtime change.

**Actions:**
```bash
echo "edited" >> "$WATCH/hello.md"
$SW reconcile now
```

**Expected events (1):** `file.modified hello.md`, with new `size` and `mtime`.

**What this exercises:** `changed()` in `diff.go` — should detect size+mtime delta; if `HashEnabled`, also content_hash delta.

**Edge case to look for:** if size and mtime are unchanged (e.g., identical content rewritten with a touch), and hashing is off, the event should NOT fire. Document this as a known gap of the polling approach.

---

## 3 — Two edits inside coalesce window

**Goal:** burst-suppression works — two modifies within 5s collapse to one event with a `coalesced_into` pointer.

**Actions:**
```bash
echo "first edit"  >> "$WATCH/hello.md"
sleep 2
echo "second edit" >> "$WATCH/hello.md"
$SW reconcile now
```

**Expected events:** 1 visible `file.modified`; the second one is folded in (`coalesced_into = <first id>`).

**Verify:**
```bash
$SW sql "SELECT id, coalesced_into FROM events WHERE rel_path='hello.md' ORDER BY created_at"
```

**What this exercises:** `InsertOrCoalesceEvent` + `events.ShouldCoalesce` window logic.

---

## 4 — Two edits straddling the coalesce window

**Goal:** if the second edit lands *after* the 5s window, it stays as a separate event.

**Actions:**
```bash
echo "edit 1" >> "$WATCH/hello.md"
sleep 8       # > coalesce_window (5s)
echo "edit 2" >> "$WATCH/hello.md"
$SW reconcile now
```

**Expected events:** 2 separate `file.modified` rows, neither coalesced.

**What this exercises:** the *boundary* of coalesce — easy to off-by-one.

---

## 5 — Rename within the watched root

**Goal:** rename detection pairs a delete+create with identical size+mtime into one `file.renamed`.

**Actions:**
```bash
mv "$WATCH/hello.md" "$WATCH/greetings.md"
$SW reconcile now
```

**Expected events (1):**
```json
{"type":"file.renamed","rel_path":"greetings.md","old_path":"hello.md","size":...}
```

**What this exercises:** `DetectRenames` in `internal/watcher/rename.go`. Note the heuristic relies on identical size+mtime; if the FS updates mtime on rename (some FSes do), this becomes a delete+create.

---

## 6 — Move a directory subtree

**Goal:** moving `auth/` → `services/auth/` produces N rename pairs, NOT N (delete+create) doubles.

**Actions:**
```bash
mkdir -p "$WATCH/auth"
echo "a" > "$WATCH/auth/login.go"
echo "b" > "$WATCH/auth/oauth.go"
echo "c" > "$WATCH/auth/jwt.go"
$SW reconcile now
# (3 file.created events)

mkdir -p "$WATCH/services"
mv "$WATCH/auth" "$WATCH/services/"
$SW reconcile now
```

**Expected events (3):** three `file.renamed` rows where `old_path` has the `auth/` prefix and `rel_path` has the `services/auth/` prefix.

**What this exercises:** rename pairing at scale — and surfaces a known weakness if any of the three files has its mtime updated by the FS on rename, in which case those become delete+create pairs. Document the result.

---

## 7 — Editor swap-file pattern (vim/atomic-write)

**Goal:** modern editors often write a *new* file then rename over the target. Sharedwatch should NOT report a spurious delete + create.

**Actions:**
```bash
echo "v1" > "$WATCH/doc.md"
$SW reconcile now
# emulate vim's atomic write
echo "v2" > "$WATCH/doc.md.swp"
mv "$WATCH/doc.md.swp" "$WATCH/doc.md"   # overwrites
$SW reconcile now
```

**Expected events:**
- `*.swp` files are in the default ignore list, so the swap file itself doesn't appear.
- `doc.md` shows as `file.modified` (not delete+create).

**What this exercises:** ignore-pattern handling + rename heuristic against same-target overwrites.

---

## 8 — Bulk find-and-replace via sed

**Goal:** N files modified in a tight loop. Coalesce should NOT merge across different `rel_path`s (it doesn't, but the test confirms).

**Actions:**
```bash
for i in {1..10}; do echo "hello world" > "$WATCH/file_$i.txt"; done
$SW reconcile now
# (10 file.created)

find "$WATCH" -name 'file_*.txt' -exec sed -i 's/world/sharedwatch/' {} \;
$SW reconcile now
```

**Expected events:** 10 `file.modified` rows, one per file. None coalesced into another (different `rel_path`).

**What this exercises:** per-path coalesce scoping. With multi-folder, this is also the test that two `file_1.txt`s in different roots stay distinct.

---

## 9 — Two-agent handoff (spec → code)

**Goal:** the *intended* multi-agent flow. Agent A writes a spec; agent B sees it via the journal, writes implementation, and references A's event.

**Actions:**

```bash
# Agent A (claude-spec) creates a spec.
$SW --actor claude-spec --session sess-handoff --task new-feature-spec \
    test emit specs/widget.md
echo "Widget should support red and blue modes" > "$WATCH/specs/widget.md"
$SW reconcile now

# Get the event id for the spec write.
SPEC_EVT=$($SW events list --path-glob 'specs/widget.md' --limit 1 --format json | jq -r '.rows[0].id')

# Agent B (claude-code) reacts: writes implementation, references A's event.
$SW --actor claude-code --session sess-impl --task implement-widget \
    --payload-key ref_event_id --payload-value "$SPEC_EVT" \
    --payload-key addressee --payload-value claude-spec \
    test emit code/widget.go
echo "package main // implements widget per $SPEC_EVT" > "$WATCH/code/widget.go"
$SW reconcile now
```

**Expected events (2):** a `file.created` on `specs/widget.md` attributed to `claude-spec`, a `file.created` on `code/widget.go` attributed to `claude-code` with `ref_event_id = <SPEC_EVT>`.

**What this exercises:** the proposed `payload_json` v1 schema + the agent-to-agent causal chain. **This is the scenario that justifies the whole project.**

**Verify:**
```bash
# B's events tagged to A's spec
$SW events list --payload-key ref_event_id --payload-value "$SPEC_EVT" --format json
```

---

## 10 — Concurrent edits in active mode

**Goal:** with active mode (5s cadence), bursty multi-process activity is captured granularly.

**Actions:**
```bash
$SW mode active --ttl 5m
# two background writers
( for i in {1..20}; do echo "A$i" >> "$WATCH/a.txt"; sleep 0.3; done ) &
( for i in {1..20}; do echo "B$i" >> "$WATCH/b.txt"; sleep 0.3; done ) &
wait
# active mode means the consumer is reading on the 5s cadence
sleep 8
$SW status --json | jq .
```

**Expected events:** modifies on both `a.txt` and `b.txt`. The number depends on coalesce window vs interval; document the count and the coalesced-into chains.

**What this exercises:** active mode + concurrent producers + coalesce + consumer cadence interaction. **High value for revealing UX surprises.**

---

## 11 — Restart safety mid-batch

**Goal:** verify the SQLite journal + `RecoverStuckProcessing` keeps an in-flight consume from losing events.

**Actions:**
```bash
# Generate 100 pending events
for i in {1..100}; do touch "$WATCH/r_$i"; done
$SW reconcile now

# Forcibly kill the running daemon during a consume.
kill -9 %1
$SW run &

# Recovery: events stuck in 'processing' older than threshold flip back to pending.
sleep 60
$SW status --json | jq '.pending,.processed'
```

**Expected outcome:** no events lost; eventually all 100 land in `processed`. None are silently stuck in `processing`.

**What this exercises:** `RecoverStuckProcessing` (the watchdog timer for crashed consumers) + WAL durability.

---

## 12 — Large file above HashMaxSize

**Goal:** files larger than `HashMaxSize` (default 1 MB) should be tracked but not hashed.

**Actions:**
```bash
dd if=/dev/urandom of="$WATCH/big.bin" bs=1M count=5
$SW reconcile now
$SW sql "SELECT rel_path, file_size, content_hash FROM events WHERE rel_path='big.bin'"
```

**Expected:** event present, `file_size = 5242880`, `content_hash = NULL` (or empty string).

**What this exercises:** the `HashEnabled && size <= HashMaxSize` gate in `catalog/snapshot.go`. **Important** because a future `--capture-content` blob store would inherit the same gate.

---

## 13 — Ignored patterns

**Goal:** `.git/`, `.DS_Store`, `*.tmp`, `*.swp` are silent.

**Actions:**
```bash
mkdir -p "$WATCH/.git/objects"
echo "git internals" > "$WATCH/.git/objects/abc"
echo "macos" > "$WATCH/.DS_Store"
echo "tmp" > "$WATCH/scratch.tmp"
echo "swap" > "$WATCH/file.swp"
echo "real" > "$WATCH/real.md"
$SW reconcile now
```

**Expected events (1):** only `file.created real.md`.

**What this exercises:** `catalog.Ignored` semantics. Confirms ignore patterns are root-relative and match expected behavior.

---

## 14 — Cold start with 50 pre-existing files

**Goal:** on first run against a populated tree, every file emits a `file.created`.

**Actions:**
```bash
# Fresh DB, but the watch folder already has 50 files. Archive the existing
# DB (move, never rm) so we test cold-start without losing the prior journal.
safe_archive_db
for i in {1..50}; do echo "preexisting $i" > "$WATCH/pre_$i.md"; done
$SW init
$SW reconcile now
$SW status --json | jq '.pending,.last_reconcile_event_count'
```

**Expected events (50):** all `file.created` with `source = reconciler` (cold-start path in `internal/watcher/service.go`).

**What this exercises:** the "no previous snapshot" branch in `ScanAndQueue` (and same in reconcile). **Critical** because a misimplementation here either floods or under-emits.

---

## 15 — Reconcile catches a missed event

**Goal:** if a change happens while the watcher is down, the reconciler discovers it on next run.

**Actions:**
```bash
# Stop the daemon.
kill %1 2>/dev/null; wait %1 2>/dev/null

# Change a file with no watcher running.
echo "stealth edit" >> "$WATCH/real.md"

# Restart and reconcile.
$SW run &
sleep 2
$SW reconcile now
$SW events list --path-glob 'real.md' --since 5m --format json
```

**Expected events:** one `file.modified` on `real.md` with `source = reconciler` (NOT `watcher`).

**What this exercises:** the safety-net path. **Highest-value test for the "durable" promise.**

---

## 16 — Burst of 200 events / 60s

**Goal:** load test. Does the journal and consumer keep up?

**Actions:**
```bash
$SW mode active --ttl 10m
for i in {1..200}; do echo "$i" > "$WATCH/burst_$i.md"; sleep 0.3; done
sleep 30
$SW status --json | jq '.pending,.failed,.processed'
$SW sql "SELECT COUNT(*) FROM events WHERE rel_path LIKE 'burst_%'"
```

**Expected:** all 200 visible in events table; `pending = 0` after a few consumer cycles; no failed events.

**What this exercises:** WAL throughput, consumer batch sizing (default 100), active-mode cadence. Also: do we hit DB lock contention?

---

## 17 — Cross-actor churn on the same file

**Goal:** two agents both editing `auth/login.go` — does the journal make the conflict visible?

**Actions:**
```bash
# Agent A
$SW --actor claude-A --session sA --task auth-fix \
    test emit auth/login.go
echo "// A's change" >> "$WATCH/auth/login.go"
$SW reconcile now

# Agent B (4 seconds later — inside the coalesce window)
$SW --actor claude-B --session sB --task auth-feature \
    test emit auth/login.go
echo "// B's change" >> "$WATCH/auth/login.go"
$SW reconcile now
```

**Expected:** **this exposes a real design question.** Coalesce today is keyed on `rel_path` only; B's modify will coalesce into A's pending modify, losing B's distinct attribution. This is a *finding* the dogfood exposes.

**Verify:**
```bash
$SW sql "SELECT id, json_extract(payload_json,'$.actor'), coalesced_into \
         FROM events WHERE rel_path='auth/login.go' ORDER BY created_at"
```

**What this exercises:** the limit of single-key coalesce when multiple actors are involved. **Recommended follow-up:** coalesce on `(rel_path, actor)` not just `rel_path`. File this as a ticket.

---

## 18 — Symlink to outside the watched root

**Goal:** symlinks pointing outside the root should be handled deterministically.

**Actions:**
```bash
# Create a benign in-$DOG target so we never touch system files.
echo "external content v1" > "$DOG/external_target.txt"
ln -s "$DOG/external_target.txt" "$WATCH/outside-link"
$SW reconcile now

# Modify the *target* (which lives OUTSIDE $WATCH) — we do NOT modify
# anything in /etc or any system path. The dogfood stays inside $DOG.
echo "external content v2" > "$DOG/external_target.txt"
$SW reconcile now
```

**Expected:** *currently unspecified.* The symlink itself appears as a 0-byte entry; its target's modifications are not captured (filepath.WalkDir doesn't follow symlinks by default in Go). Document the observed behavior and decide whether it matches intent.

**What this exercises:** the "symlinks: deferred" caveat in the multi-folder ticket. **Document the answer** before customers hit it.

**Cleanup:** the symlink is a file under `$WATCH`, so the standard `safe_clear_files` will move it to `$QUARANTINE`. The external target stays in `$DOG/external_target.txt` (also safe — inside `$DOG`).

---

## Verification recipes (reusable SQL)

```sql
-- Events in the last hour, projected for an LLM consumer
SELECT id, type, rel_path,
       json_extract(payload_json, '$.actor') AS actor,
       json_extract(payload_json, '$.session') AS session,
       observed_at
FROM events
WHERE created_at > datetime('now', '-1 hour')
ORDER BY created_at DESC
LIMIT 100;

-- Churn-per-path in the last 24h (top 20)
SELECT rel_path, COUNT(*) AS events, MAX(created_at) AS last
FROM events
WHERE created_at > datetime('now', '-1 day')
GROUP BY rel_path
ORDER BY events DESC
LIMIT 20;

-- Actor activity
SELECT json_extract(payload_json, '$.actor') AS actor, COUNT(*) AS events
FROM events
WHERE created_at > datetime('now', '-1 day')
GROUP BY actor
ORDER BY events DESC;

-- Unattributed events (no actor set) — quality metric for cooperative attribution
SELECT COUNT(*) FROM events
WHERE created_at > datetime('now', '-1 day')
  AND (payload_json = '{}' OR json_extract(payload_json, '$.actor') IS NULL);

-- Coalesce chains: how often does coalesce merge?
SELECT COUNT(*) AS coalesced_count FROM events WHERE coalesced_into IS NOT NULL;

-- Reconcile vs watcher attribution mix
SELECT source, COUNT(*) FROM events GROUP BY source;

-- Renames detected
SELECT rel_path, old_path, observed_at FROM events
WHERE type = 'file.renamed' ORDER BY created_at DESC LIMIT 20;
```

---

## What the dogfood should *teach* us

For each scenario, capture three artifacts:

1. **The raw `events list --format jsonl` output** — the evidence.
2. **One token estimate** — how many tokens would an LLM need to ingest this scenario's events? (drives the progressive-disclosure design)
3. **One paragraph** — what felt right, what felt wrong, what was missing.

The aggregate teaches us:
- Where coalesce helps vs hurts (#3, #8, #17).
- Where the rename heuristic is brittle (#5, #6, #7).
- Whether ignore patterns are correctly scoped (#13).
- Whether cold-start UX is OK or floods (#14).
- Whether reconcile actually catches drift (#15).
- Whether attribution is sufficient to disambiguate co-editing (#17 is the load-bearing one).
- Whether throughput hits limits (#16).
- Whether the symlink decision matches expectation (#18).

**The scenarios are designed so a stranger could re-run the dogfood pass next quarter and detect regressions.** That makes this document a living test plan, not a one-shot exercise.

---

## What's deliberately *not* in this dogfood

- **Cross-host scenarios.** Out of scope for v1; the design rules them out.
- **Permissions / setuid corner cases.** Different threat model.
- **Network filesystem behavior (NFS, SMB, FUSE).** These FSes have weird mtime semantics; a separate hardening pass.
- **Subsecond chrono ordering.** SQLite created_at is RFC3339Nano; events from the same `reconcile now` may share a timestamp. Document but don't test.
- **Very long paths (>4096 chars), unicode, control characters.** Worth a separate fuzz pass.

---

## Running the whole pass

```bash
# Pseudo-script — execute each scenario, dump artifacts, tear down between.
# IMPORTANT: no `rm`, no `sudo`, no folder deletes. Tear-down uses the
# safe_clear_files helper defined in the Setup section.
for SCEN in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18; do
  echo "=== Scenario $SCEN ==="
  bash scenarios/scenario_${SCEN}.sh \
    > artifacts/scenario_${SCEN}_stdout.log 2>&1
  $SW events list --since 10m --format jsonl \
    > artifacts/scenario_${SCEN}_events.jsonl
  $SW status --json > artifacts/scenario_${SCEN}_status.json
  safe_clear_files && $SW reconcile now
done
```

### Recovery — if something accidentally lands in `$QUARANTINE`

Everything moved by `safe_clear_files` / `safe_remove_file` lives under `$QUARANTINE` with a `<timestamp>-<pid>/` prefix and the original relative path preserved. To restore a single file:

```bash
# Find what's there
find "$QUARANTINE" -type f | sort

# Move it back (replace <stamp> with the actual prefix)
mv "$QUARANTINE/<stamp>/<relpath>" "$WATCH/<relpath>"
```

To inspect what the dogfood "deleted" without restoring:
```bash
find "$QUARANTINE" -type f -printf '%T@ %p\n' | sort -n
```

`$QUARANTINE` and `$ARCHIVE` are append-only by convention — never `rm` them, just let them grow during the pass. Clean up by hand after you've reviewed the artifacts.

After a pass, the `artifacts/` directory becomes the canonical "this is what real activity looks like" reference that we can attach to release notes, point new contributors at, and re-run for regression checks.

---

## 20 — CLI polish: `--quiet`, `--dry-run`, JSON errors (v0.0.6)

**Goal:** verify the three additions from SW-AGENT-19 each behave as specified — `--quiet` suppresses Next blocks and friendly info, `--dry-run` previews destructive commands without writing, and `--format json` emits errors as a JSON envelope on stdout.

**Setup:**
```bash
export DOG=/tmp/sw-scenario-20
rm -rf "$DOG" && mkdir -p "$DOG"
$SW --data-dir "$DOG" init >/dev/null
$SW --data-dir "$DOG" --actor sc20 test emit a.md >/dev/null
# Mark one event failed so --dry-run has something to preview:
$SW --data-dir "$DOG" sql --write "UPDATE events SET status='failed' WHERE id=(SELECT id FROM events LIMIT 1)" >/dev/null
```

**Actions + expected:**

```bash
# (a) --quiet suppresses Next: hint block on status
$SW --data-dir "$DOG" --quiet status
# Expected: single status line; NO blank line; NO "Next:" header.

# (b) --quiet silences init / stop friendly messages
rm -rf /tmp/sw-quiet-init && $SW --data-dir /tmp/sw-quiet-init --quiet init
# Expected: zero stdout output, exit 0.
$SW --data-dir "$DOG" --quiet stop
# Expected: zero stdout output, exit 0 (no daemon was running).

# (c) --dry-run on events retry previews without writing
$SW --data-dir "$DOG" events retry --dry-run
# Expected: "dry-run: would requeue 1 failed event(s):" + the event id.
$SW --data-dir "$DOG" sql "SELECT status FROM events WHERE id=(SELECT id FROM events LIMIT 1)"
# Expected: status = 'failed' (unchanged by dry-run).

# (d) --dry-run on events recover-stuck (no stuck events expected here)
$SW --data-dir "$DOG" events recover-stuck --dry-run
# Expected: "dry-run: no stuck processing events would be flipped"

# (e) JSON-structured error on bad --since when --format jsonl
$SW --data-dir "$DOG" events list --format jsonl --since not-a-time
# Expected on STDOUT (exit 1):
#   {
#     "format_version": 1,
#     "error": {
#       "code": "bad_flag",
#       "message": "--since: not a valid RFC3339 timestamp or duration: \"not-a-time\""
#     }
#   }

# (f) Same error WITHOUT --format json: plain text on stderr (unchanged behaviour)
$SW --data-dir "$DOG" events list --since not-a-time
# Expected on STDERR (exit 1): error: --since: not a valid RFC3339 timestamp or duration: "not-a-time"
```

**What this exercises:**
- `--quiet` resolution path through `resolveHintsProfile` (forced ProfileOff).
- `--quiet` gating of friendly info in `handleInit` and `handleStop`.
- `events retry --dry-run` and `events recover-stuck --dry-run` mirror the live UPDATE's WHERE clause via SELECT for accurate preview without modification.
- `fatalJSON` envelope shape on stdout when `--format json|jsonl` is set; unchanged stderr text on the default text format.

**Pass criteria:** all six commands behave exactly as the expected blocks describe. Any divergence is a regression in SW-AGENT-19's contract.

**Recorded run (v0.0.6, 2026-05-24):** see worklog in [`../../tickets/tasklist_20260524_051945.md`](../../tickets/tasklist_20260524_051945.md) for the actual captured output against the deployed binary.

---

## 21 — First-session canonical workflow with env vars (v0.0.7)

**Goal:** confirm the v0.0.5 ergonomics promise — an agent exports `SHAREDWATCH_*` once at session start and never types those flags again. Walks the full init → emit → cursor read → introspect → stop lifecycle.

**Setup:**
```bash
export DOG=/tmp/sw-scenario-21
rm -rf "$DOG" && mkdir -p "$DOG"
export SHAREDWATCH_ACTOR=demo-agent-21
export SHAREDWATCH_ACTOR_KIND=ai_agent
export SHAREDWATCH_FORMAT=jsonl
export SHAREDWATCH_HINTS=agent
export SHAREDWATCH_CURSOR_NAME=demo-agent-21
```

**Actions + expected:**

```bash
# (a) init — no flags. Expected: prints paths, Next: hint suggests run.
$SW --data-dir "$DOG" init

# (b) Emit attributed event — no --actor / --task / --format flags.
$SW --data-dir "$DOG" --task demo test emit hello.md
# Expected stdout: "emitted evt_xxx hello.md"
# Expected journal entry: payload_json.actor = "demo-agent-21"

# (c) Cursor read — no --cursor-name / --format flags.
#     Should be in cursor mode (env-derived) AND JSONL (env-derived).
$SW --data-dir "$DOG" events list
# Expected: 1 jsonl row for hello.md with actor=demo-agent-21
# Expected: cursor "demo-agent-21" advances to that event

# (d) Cursor read again — should be empty (cursor advanced past).
$SW --data-dir "$DOG" events list
# Expected: zero rows

# (e) Introspect — verify env vars are being read.
$SW --data-dir "$DOG" config show | head -20

# (f) Status (JSON, agent profile — auto from env).
$SW --data-dir "$DOG" status --json | jq '.next | length'
# Expected: integer > 0 (next[] populated under agent profile)

# (g) Stop (no daemon running). Friendly + suggests run.
$SW --data-dir "$DOG" stop
```

**What this exercises:** the env-var resolution chain (SW-AGENT-18 §3) end-to-end; `--cursor-name` default from env activating cursor mode without an explicit flag; `SHAREDWATCH_FORMAT=jsonl` flowing into `events list`; `SHAREDWATCH_HINTS=agent` producing rich `next[]` arrays.

**Pass criteria:** none of (a)–(g) require an explicit `--actor` / `--format` / `--cursor-name` / `--hints` flag; every command's output matches expectations as if those flags had been typed.

**Recorded run:** see worklog in [`../../tickets/tasklist_20260524_053824.md`](../../tickets/tasklist_20260524_053824.md).

---

## 22 — Stale lock file recovery (v0.0.7)

**Goal:** when a daemon dies hard (SIGKILL or kernel OOM), it can't run its `defer fileLock.Release` — the lock file is left on disk with a stale PID. Verify `sharedwatch stop` detects the stale PID via ESRCH, and the next `sharedwatch run` recovers cleanly because `flock(2)` releases on process exit even without explicit unlock.

**Setup:**
```bash
export DOG=/tmp/sw-scenario-22
rm -rf "$DOG" && mkdir -p "$DOG"
$SW --data-dir "$DOG" init >/dev/null
```

**Actions + expected:**

```bash
# (a) Start a daemon in background.
$SW --data-dir "$DOG" run >"$DOG/run.log" 2>&1 &
RUNPID=$!
sleep 1

# (b) Confirm lock file has our PID.
cat "$DOG/sharedwatch.lock"
# Expected: $RUNPID

# (c) SIGKILL the daemon (no chance to release the lock).
kill -9 "$RUNPID"
sleep 0.5

# (d) Lock file should STILL exist after SIGKILL (no defer ran).
ls "$DOG/sharedwatch.lock"
# Expected: file present, PID matches the dead one.

# (e) `stop` should detect ESRCH and report friendly "already gone" message.
$SW --data-dir "$DOG" stop
# Expected: "daemon already gone (pid $RUNPID not found; lock file is stale...)"
# Expected exit: 0

# (f) Lock file IS still on disk after the friendly stop (we don't auto-remove).
ls "$DOG/sharedwatch.lock" && echo "still present"
# Expected: still present.

# (g) New `run` should acquire the lock cleanly — flock releases on process
#     death even without unlock, so the new fcntl-style lock succeeds and
#     overwrites the PID.
$SW --data-dir "$DOG" run >"$DOG/run2.log" 2>&1 &
NEWPID=$!
sleep 1
cat "$DOG/sharedwatch.lock"
# Expected: $NEWPID (overwritten by the new process)

# (h) Clean shutdown of the recovered daemon.
$SW --data-dir "$DOG" stop
```

**What this exercises:** `internal/app/lock.go` semantics; ESRCH path in `cmd/sharedwatch/stop.go`; the POSIX `flock(2)` release-on-exit guarantee.

**Pass criteria:** (e) reports friendly stale-PID line and exits 0; (g) successfully acquires the lock; (h) cleans up.

**Recorded run:** see worklog.

---

## 23 — Misbehaving agent — text vs JSON error parity (v0.0.7)

**Goal:** agents pass bad input to various format-aware commands. Errors should be **plain text on stderr** in default text mode (exit 1) and **JSON envelope on stdout** when `--format json|jsonl` is set (exit 1). Same logical error → same code in JSON; same human message in text.

**Setup:**
```bash
export DOG=/tmp/sw-scenario-23
rm -rf "$DOG" && mkdir -p "$DOG"
$SW --data-dir "$DOG" init >/dev/null
```

**Actions + expected (text mode):**

```bash
# (a-text) bad --since on events list
$SW --data-dir "$DOG" events list --since not-a-time 2>&1; echo "exit=$?"
# Expected on stderr: error: --since: not a valid RFC3339 timestamp or duration: "not-a-time"
# Expected exit: 1

# (b-text) garbage SQL
$SW --data-dir "$DOG" sql "GARBAGE QUERY" 2>&1; echo "exit=$?"
# Expected on stderr: error: non-SELECT statements require --write...
# Expected exit: 2 (sql special-cases this for the hint)

# (c-text) unknown schema table
$SW --data-dir "$DOG" schema unknown_table 2>&1; echo "exit=$?"
# Expected on stderr: error: no such table: unknown_table
# Expected exit: 1

# (d-text) events stats with no --root
$SW --data-dir "$DOG" events stats 2>&1; echo "exit=$?"
# Expected on stderr: error: events stats requires --root <label>...
# Expected exit: 1
```

**Actions + expected (JSON mode — flags before positional args):**

```bash
# (a-json)
$SW --data-dir "$DOG" events list --format jsonl --since not-a-time
# Expected on stdout: {"format_version":1,"error":{"code":"bad_flag","message":"--since: not a valid RFC3339 ..."}}

# (b-json)
$SW --data-dir "$DOG" sql --format jsonl "GARBAGE QUERY"
# Expected on stdout: {"format_version":1,"error":{"code":"bad_flag","message":"non-SELECT statements require --write ..."}}

# (c-json)
$SW --data-dir "$DOG" schema --format jsonl unknown_table
# Expected on stdout: {"format_version":1,"error":{"code":"not_found","message":"no such table: unknown_table"}}

# (d-json)
$SW --data-dir "$DOG" events stats --format jsonl
# Expected on stdout: {"format_version":1,"error":{"code":"bad_flag","message":"events stats requires --root <label>..."}}
```

**What this exercises:** `fatalJSON` wiring across all 5 format-aware handlers (`events list`, `events stats`, `sql`, `schema`, `overview`); the canonical error-code vocabulary; the "JSON to stdout, text to stderr" contract.

**Pass criteria:** every text-mode case has the same human message; every JSON-mode case is a parseable envelope with the right `code` value.

**Known gotcha (for the system prompt):** `--format` must appear **before** any positional argument (e.g. before the SQL string) for Go's stdlib `flag` parser to see it. `sharedwatch sql "X" --format jsonl` is parsed as `sql "X"` ignoring the trailing flag.

**Recorded run:** see worklog.

---

## 24 — Multi-agent lease warning (v0.0.7)

**Goal:** when actor B writes inside the path-glob of an active lease held by actor A, the watcher (and reconciler) log a **structured `slog.Warn` line** with `event_id`, `rel_path`, `watch_root`, `event_actor`, `lease_id`, `lease_actor`, `lease_path_glob`, `lease_expires_at`. Verify a cooperative B can see the warning and decide whether to back off.

**Setup:**
```bash
export DOG=/tmp/sw-scenario-24
rm -rf "$DOG" && mkdir -p "$DOG"
$SW --data-dir "$DOG" init >/dev/null

# Start daemon with JSON logging so we can grep structured fields.
$SW --data-dir "$DOG" --log-format json run >"$DOG/run.log" 2>&1 &
RUNPID=$!
sleep 1
```

**Actions + expected:**

```bash
# (a) Actor A heartbeats + acquires a lease on auth/**
$SW --data-dir "$DOG" actor heartbeat claude-a --kind ai_agent --focus 'auth/**'
$SW --data-dir "$DOG" lease acquire 'auth/**' --actor claude-a --ttl 5m

# (b) Actor B writes inside the leased glob. Watcher should detect.
$SW --data-dir "$DOG" --actor claude-b test emit auth/login.go

# (c) Give the watcher a beat, then check the log for the structured warning.
sleep 2
grep -E '"msg":"lease_violation"|"lease_actor":"claude-a"' "$DOG/run.log"
# Expected: at least one log line with both lease_actor=claude-a AND
# event_actor=claude-b (or similar — exact field names per the SW-AGENT-12
# implementation in internal/watcher/service.go).

# (d) Clean shutdown.
$SW --data-dir "$DOG" stop
```

**What this exercises:** `db.LeaseGlobMatchesPath` from SW-AGENT-12; the watcher's slog.Warn emission on lease violations; the multi-actor cooperation pattern.

**Pass criteria:** the structured warning fires for the cross-actor write; the relevant fields are present in the log line.

**Known gotcha (for the system prompt):** lease warnings go to **logs** (slog.Warn), not the journal. An agent that wants programmatic notification of lease violations must tail the log file OR file-watch for the warning OR ask the orchestrator to scrape stderr. Building a journal-side notification is filed as a future-consideration in [`../design/hooks-discussion-20260524.md`](../design/hooks-discussion-20260524.md).

**Recorded run:** see worklog.
