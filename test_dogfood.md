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
