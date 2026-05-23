# sharedwatch — content storage design evaluation

**Date:** 2026-05-23
**Status:** UNDER EVALUATION — decision needed before SW-AGENT-16 implementation locks in
**Author:** claude (this session)
**Audience:** the team — humans deciding the shape of v0.9.0 storage
**Estimated decision effort:** ~half-day workshop / one focused review session

This document evaluates the choice between three storage approaches for the "what changed inside the file" feature targeted by SW-AGENT-16. It captures what LLM consumers actually want from the journal, applies the user's "hidden + isolated git, not in the watched folder" constraint, and recommends a path. The goal is **shared understanding before we commit code**, because storage decisions made in v0.9.0 are expensive to migrate in v0.9.1.

---

## 1. The question

SW-AGENT-16 currently proposes storing per-event content changes as **gzipped unified diff text in a `event_diffs.diff_gz` BLOB column**. That's the simplest workable design — minimal storage, one external library, no runtime dependencies.

A user-raised alternative: **use git-style content-addressed blob storage** as the underlying content store, with sharedwatch's events referencing blob hashes. Two key constraints from the user on this:

1. Any git-style storage must be **HIDDEN out of the watched folder** so it never interferes with the user's actual git repo.
2. It must NOT use the user's git index, working tree, refs, or any commit machinery — just the blob storage layer.

A third option lurks: hybrid (store both diff for fast access AND blob hash for full reconstruction).

The decision matters because storage approach drives:
- What questions consumers (LLMs, humans, other tools) can ask the journal
- Storage growth profile (small/medium/large)
- Operational complexity (single small file vs. content-addressed object directory)
- Forward-extensibility (can a future feature read the data?)

---

## 2. The framing — what LLM consumers actually want

The user's framing, verbatim: *"almost like a time series of diff changes."*

That's the right framing. An LLM consuming the journal isn't asking "did this file change?" (a trigger question). It's asking:

- *"How has `auth/login.go` evolved over the last hour? Show me the chain."*
- *"What did claude-X actually do to this file? Walk me through each modification."*
- *"What did this file look like when claude-Y referenced it via `ref_event_id`?"*
- *"Diff the file's state at event A against its state at event E (three events later)."*
- *"Show me only the diffs from non-self actors on paths under `auth/**` since my last cursor."*

These are **time-series queries** over file content. They require either:

(a) A diff at every modification (you walk forward through diffs to understand evolution), OR
(b) Full content at every modification (you can pick any two points and diff them, or just show the bytes), OR
(c) Both — diffs for fast "what changed" answers, full content for "show me the state."

The single touch-event view from v0.8.0 cannot answer any of these well. Even SW-AGENT-16's diff-per-event answers some but not others:

| LLM query | diff_gz only (Option A) | content blob store (Option B) | Hybrid (Option C) |
|---|---|---|---|
| "What changed at event N?" | ✅ direct | requires diff blob_N vs blob_N-1 | ✅ direct |
| "What did the file look like at event N?" | ❌ no full content | ✅ retrieve blob_N | ✅ retrieve blob_N |
| "Diff between events A and E?" | ⚠ chain A→B→C→D→E forward; doable but each step materialized | ✅ diff blob_A vs blob_E | ✅ diff blob_A vs blob_E |
| "Show me the file as claude-X first saw it before editing" | ❌ | ✅ retrieve blob at the read-event (if we capture reads — separate feature) | ✅ |
| "Time series of diffs on path P for actor X in last 1h" | ✅ filter + display | ✅ filter + compute diffs from blobs | ✅ filter + display |
| "Reconstruct the file's state 3 days ago" | ❌ | ✅ retrieve blob from that day's event | ✅ |
| Storage cost | very low (small deltas) | medium-high (full bytes, with dedup) | high (both) |

Option A nails the most common single-event "what changed" question but fails on "point-in-time reconstruction" and most cross-event queries.

Option B can answer everything but at higher storage cost.

Option C is the most capable but most expensive.

**The LLM-consumer answer:** the time-series framing genuinely demands Option B or C. Option A is *almost enough* — agents who only ever want to see "what just changed?" get a great answer — but the moment they want to reason historically ("show me the file as it was at T-2 hours") they're blocked.

---

## 3. The "hidden + isolated" constraint

The user's stipulation: if we adopt git-style blob storage, it must NOT touch the user's watched folder. Concretely, this means:

- **No `.git/` inside the watched folder is touched by sharedwatch.** If the user's watched folder happens to be a git working tree, sharedwatch ignores it entirely.
- **No git refs, no commits, no working-tree state.** Sharedwatch never runs `git commit`, `git add`, `git checkout`, or anything that mutates state visible to the user's git tooling.
- **Blob storage lives in sharedwatch's own data directory**, e.g. `<data_dir>/blobs/`, far from the watched folder. The data directory is already where the SQLite DB lives.
- **No git binary required at runtime** if we can avoid it — to keep sharedwatch's "single binary, one dep" ethos as close as possible.

What this means in practice: **we're not using git's commit/history layer at all.** We might use git's blob FORMAT (content-addressed, zlib-compressed) for portability, but we are NOT a git repo. The storage is sharedwatch-owned.

Three implementation paths under this constraint:

### Path α — sharedwatch-owned blob store, git-format-compatible
- Storage layout: `<data_dir>/blobs/<first-2-hash-chars>/<rest-of-hash>` (git's loose-object layout)
- Each blob: zlib-compressed bytes, prefixed with `blob <size>\0` header (git's blob format)
- Sharedwatch implements ~200 LOC of focused code to write/read these
- Bonus: any git tool (`git cat-file -p <hash>`) can read the blobs if pointed at the directory
- Cost: small new code; no new external dependency
- Hash function: SHA-1 (to be git-compatible) OR SHA-256 (more modern, but breaks git tool compat)

### Path β — pure go-git library
- Vendor `github.com/go-git/go-git/v5` — Go-native git implementation
- Use only its blob-write/blob-read API; ignore everything else (refs, packfiles, working tree)
- Cost: large external dep (~30k LOC + transitives); we are paying for features we don't use
- Benefit: battle-tested implementation; if we ever do want more git features they're available

### Path γ — sharedwatch-owned blob store, NOT git-compatible
- Storage layout: `<data_dir>/blobs/<sha256[:2]>/<sha256>`
- Each blob: just zlib-compressed bytes (no header, no git format)
- Pure SHA-256 content addressing
- Cost: ~150 LOC; no new external dependency
- Loss: git tools can't read the blobs; we own the format end-to-end

The user constraint ("hidden, doesn't interfere with any other git") rules out using the user's git repo's object store but doesn't actually require git format. Path α or Path γ both satisfy it. Path α has a portability bonus (any git tool can debug-read our blobs); Path γ is the simplest.

**Recommendation: Path γ.** Reasons:
- Simplest implementation; smallest surface
- No SHA-1 (deprecated) inheritance
- No external dependency
- Sharedwatch fully owns the format
- The "git tool can read it" benefit of Path α is marginal — operators almost never need to read blobs directly; they'll use `sharedwatch content show <event-id>`

If the team values git-format portability for debugging, Path α adds maybe 30 LOC (the blob header). Either is fine.

---

## 4. Storage growth analysis

For a busy shared workspace (call it 10 active writers + 1000 modifications/day, average file size 20 KB, average diff size 2 KB):

**Option A (diff_gz only):**
- Per event: ~500 bytes compressed diff
- Per day: 1000 events × 500 B = ~500 KB/day
- Per year: ~180 MB/year
- Acceptable indefinitely; retention pruning can keep it bounded

**Option B (content blob store, with dedup):**
- Per event: ~6 KB compressed blob (assuming zlib gets ~3× compression on text)
- But dedup: many events touch the same file repeatedly, so the same blob hash reappears
- Realistic per-day net: 1000 events × 6 KB × 0.4 dedup factor = ~2.4 MB/day
- Per year: ~870 MB/year
- Larger than Option A but still bounded; opt-in per-root would let operators scope

**Option C (hybrid):**
- A + B both stored
- ~3 MB/day, ~1 GB/year
- Maximum flexibility, maximum cost

**Outlier scenarios that change the calculus:**
- Watched folder contains generated code, lockfiles, or build artifacts: file sizes balloon to MB, blob store explodes. Mitigation: per-path opt-out (already designed in via ignore patterns); `--diff-max-size` cap (already in SW-AGENT-16).
- Watched folder is mostly binary (images, PDFs): blobs are uncompressed; storage cost spikes; diffs are useless. Mitigation: binary detection already in SW-AGENT-16; binary files get `diff_status='binary'` and (in Option B) the blob hash but no diff.
- Very low churn folder (docs, specs): storage cost is negligible regardless of option.

**Net:** Option B's storage cost is ~5× Option A's but still in the "tens of GB per year for a busy workspace" range — comfortable on any modern disk. The cost gap isn't the deciding factor.

---

## 5. The deciding factor — what queries become possible

Going back to the LLM-consumer framing. The questions an LLM most wants to ask:

```
"What did claude-X change in auth/login.go over the last hour?"
"Show me the file as it was when this PR was raised."
"Diff between two specific points in the timeline."
"Reconstruct historical state to ground a follow-up question."
"Walk the time-series of changes with diffs at each step."
```

Option A answers: ✅, ❌, ⚠ (chain forward only), ❌, ✅
Option B answers: ⚠ (compute diff from adjacent blobs), ✅, ✅, ✅, ✅
Option C answers: all ✅

The "✅, ❌, ⚠, ❌, ✅" pattern for Option A is *acceptable for triggers and recent change reasoning* but *insufficient for the broader "understand teammate work" goal* this whole branch is meant to enable.

Option B with on-demand diff computation (small CPU cost, no storage cost beyond blobs) covers 5/5.

Option C is overkill: it precomputes the diff that Option B can compute on-demand from blobs. The CPU cost of one diff computation is sub-millisecond for files under 256 KB. We do not need to precompute.

**Conclusion:** Option B is the right design. Storing diffs precomputed (Option A) trades off the wrong axis — it saves CPU at the cost of capability. Option B preserves all the capabilities and the CPU cost of on-demand diff is negligible.

---

## 6. Revised schema (locked design, if we adopt Option B)

Replace the `event_diffs` table proposed in SW-AGENT-16 §4.1 with:

```sql
CREATE TABLE IF NOT EXISTS event_content (
  event_id TEXT PRIMARY KEY,
  format_version INTEGER NOT NULL DEFAULT 1,
  content_status TEXT NOT NULL,                    -- 'captured' | 'binary' | 'too_large' | 'no_change' | 'unavailable'
  content_hash TEXT NOT NULL DEFAULT '',           -- SHA-256 of the file's bytes at this event; '' when status != captured
  bytes_size INTEGER NOT NULL DEFAULT 0,           -- file size at this event
  binary_detected INTEGER NOT NULL DEFAULT 0,      -- 1 = binary; 0 = text
  error_reason TEXT NOT NULL DEFAULT '',           -- populated when status = 'unavailable'
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_event_content_hash ON event_content(content_hash);
CREATE INDEX IF NOT EXISTS idx_event_content_status ON event_content(content_status);
```

Plus a blob store at `<data_dir>/blobs/<sha256[:2]>/<sha256>` (zlib-compressed bytes, content-addressed).

Per-event flow on `file.modified`:
1. Watcher reads file (already does this for hashing).
2. Compute SHA-256 of bytes.
3. Check if blob exists at `<data_dir>/blobs/<hash[:2]>/<hash>`. If yes, free dedup; skip write.
4. If no, zlib-compress and write the blob.
5. Insert `event_content` row referencing the hash.

Per-event flow on `file.deleted`:
- Insert `event_content` with `content_status='no_change'`, no hash. (The "delete" is its own kind of change; there's no new content to capture.)

### CLI surface (replaces SW-AGENT-16 §6.3)

```bash
# Retrieve the file's content AT a specific event
sharedwatch content show <event-id> [--format raw|text|json]

# Compute diff between any two events on the same path
sharedwatch diff <event-A> <event-B> [--format text|json]
# (special case: --base allows --base=before to mean "the event immediately prior on this path")
sharedwatch diff <event-id> --base before
# (special case: --base allows --base=current to mean "current FS state")
sharedwatch diff <event-id> --base current

# Time-series view for a path
sharedwatch path history <rel-path> [--root <X>] [--since 24h] [--with-diffs] [--format text|json]
# Output (--with-diffs):
#   evt_001  2026-05-22T14:32:11Z  claude-spec   created    (+47/-0)
#   evt_002  2026-05-22T14:35:00Z  claude-code   modified   (+12/-3)   <inline diff>
#   evt_003  2026-05-22T15:01:22Z  claude-spec   modified   (+5/-8)    <inline diff>

# Replay file state at each event in a range
sharedwatch path replay <rel-path> --from <evt> --to <evt>
# (returns content at each intermediate event)
```

The on-demand diff is computed by retrieving both blobs and running the unified-diff library (`pmezard/go-difflib` — same one SW-AGENT-16 already plans to vendor) over them.

### Storage location (LOCKED per the user constraint)

```
<data_dir>/
├── queue.db                  # the SQLite journal
├── queue.db-wal              # WAL log
├── queue.db-shm              # shared memory
├── sharedwatch.lock          # process lock
└── blobs/                    # ← NEW: hidden, sharedwatch-owned content store
    ├── ab/
    │   └── ab12cdef...       # content-addressed; zlib-compressed
    ├── ef/
    │   └── ef34abcd...
    └── ...
```

NEVER inside the watched folder. NEVER a git repo. NEVER touched by any user-facing git tool. Sharedwatch owns the directory; the user's git operations are unaffected.

### Forward extension

- `event_content.metadata_json` opaque blob for future per-event content metadata (encoding, language detected, etc.)
- `format_version=1` lets us bump when blob format changes (e.g., switch from zlib to zstd in v0.10)
- If we ever want a git-format export (e.g., "publish my journal's blob store as a git repository for archival"), the bytes are there — we'd just need to add the `blob <size>\0` header on export

---

## 7. Decision points needed before implementation

Three concrete decisions before SW-AGENT-16 (or its successor) starts coding:

### Decision 1: Option A, B, or C?

Recommendation: **Option B (blob store, on-demand diff)**.

Reasoning:
- Closes the LLM time-series queries that motivated the branch
- ~5× the storage of Option A but well within "comfortable on any disk"
- On-demand diff CPU cost is negligible
- Doesn't preclude future hybrid (we can always precompute diffs into a `event_diffs` cache table later if a hot consumer needs it)

### Decision 2: Implementation path (α/β/γ)?

Recommendation: **Path γ — sharedwatch-owned blob store, NOT git-compatible**.

Reasoning:
- Smallest implementation (~150 LOC)
- No new external dependency
- SHA-256 content addressing (not the deprecated SHA-1)
- Honors the user's "hidden + isolated" constraint exactly
- Forward-compatible: if we ever want git-format export, that's an additive change

### Decision 3: Opt-in by default vs always-on?

SW-AGENT-16 had `--diff on` default off. With Option B, the analogous question is `--capture-content on|off`.

Recommendation: **default off, opt-in via flag or config**.

Reasoning:
- Backward compatibility (v0.8 users see zero observable change unless they enable)
- Storage cost is real (~870 MB/year for a busy workspace) — operators should consent
- Per-root opt-in is a future v0.9.1 add (already designed-in via per-root config hooks)
- Same flag treatment as SW-AGENT-16 plans for `--diff`; just renames to `--capture-content`

---

## 8. What changes in SW-AGENT-16

If we adopt this evaluation's recommendations:

| Aspect | SW-AGENT-16 current | SW-AGENT-16 revised |
|---|---|---|
| Table name | `event_diffs` | `event_content` |
| Primary content storage | gzipped diff text in BLOB column | content-addressed blob store at `<data_dir>/blobs/` |
| Hash algorithm | SHA-256 (already) | SHA-256 (no change) |
| Diff library | `pmezard/go-difflib` (vendor) | `pmezard/go-difflib` (vendor — same) |
| Diff computation | At event-write time | On-demand at query time |
| External dependency | `pmezard/go-difflib` only | `pmezard/go-difflib` only |
| `sharedwatch diff <event-id>` | reads gzipped diff from DB | retrieves 2 blobs + runs diff lib |
| `sharedwatch content show <event-id>` | not possible (no content stored) | retrieves blob and returns bytes |
| `sharedwatch path history` | requires walking events + their diffs | requires walking events + retrieving blobs as needed |
| `sharedwatch path replay` | not possible | retrieves blob at each event in range |
| Storage growth (busy workspace) | ~180 MB/year | ~870 MB/year |
| Future hybrid | can add blob store later | can add precomputed diff cache later if needed |

The implementation effort is roughly the same. **Most of the SW-AGENT-16 spec stays intact** — the actor registration handshake, the configuration model, the ownership semantics, the test coverage, the migration plan. Only the *storage shape* changes.

---

## 9. Risks of going with Option B

Honesty check — risks worth weighing:

- **Blob store filesystem operations are not transactional with the SQLite write.** If SQLite commits the `event_content` row but the blob write fails (disk full mid-write), we have an orphan reference. Mitigation: write the blob BEFORE the SQLite row; if blob write fails, don't insert the row at all (event still gets emitted with `content_status='unavailable'`). The reverse orphan (blob exists, no row) is harmless garbage cleaned up by a future GC.
- **Blob directory grows without bound** unless we GC. Mitigation: a `sharedwatch content gc` subcommand that walks `event_content.content_hash` and removes blob files with no references. Run on demand or as part of the reconcile post-pass.
- **Filesystem inode count** can grow large at high-churn (each blob = one file). On ext4 default settings this is fine up to ~10 million inodes; mitigation if ever needed is to use packfiles (git's optimization for the same problem). Not a v0.9 concern.
- **Backup/restore complexity** — operators must back up both `queue.db` AND `blobs/` for a consistent restore. Document explicitly.
- **Loss of "what changed" in raw form for archival**. With Option A, the diff text was self-describing in the DB; with Option B you need the blobs to reconstruct. If an operator only backs up `queue.db` they lose the content. Mitigation: documented; backup tooling should warn.

None of these are blockers; all are addressable. They're the cost of Option B over Option A.

---

## 10. What this doc is NOT deciding

To stay focused, this evaluation does NOT take a position on:

- **Whether SW-AGENT-16's actor registration design changes.** That's orthogonal — keep it as-is.
- **Whether to use per-root content config knobs in v0.9.0.** Defer to v0.9.1+.
- **Whether to add agent-emitted change records alongside watcher-computed content.** Parallel feature; can land independently.
- **Whether to use git-format export.** A future option; not in v0.9.0.

---

## 11. Suggested next steps

To reach the decision:

1. **Review this doc** with the team. The decision is which of A/B/C to adopt.
2. **If B is chosen** (the recommendation): update SW-AGENT-16 to reflect the revised schema (§6 above). Most of the ticket stays intact.
3. **If A is kept** (the simpler path): document in SW-AGENT-16 the deliberate trade-off — "we accept that LLM consumers can't reconstruct historical state; that's a v0.9.1+ add via the content blob store."
4. **If C is chosen** (hybrid): largest cost, most flexibility — file as a separate optimization ticket after B ships if a real consumer needs the precomputed-diff hot path.
5. **Either way**: add a note to `sharedwatch-design-questions-20260523.md` as Q3 capturing this evaluation and its outcome.

---

## 12. Bottom line — recommendation

**Adopt Option B (content blob store, Path γ implementation, opt-in default off).**

It costs more storage than Option A but answers the queries that justify the v0.9 work in the first place. It satisfies the user's "hidden + isolated git store" constraint without using git at all (we use git's content-addressed *idea*, not git itself). It's forward-extensible to all the queries an LLM consumer wants to ask. It doesn't preclude later optimizations.

If we ship Option A as the v0.9.0 primary store, we will be back here in three months adding Option B's blob store anyway — because the LLM-consumer queries the user has been pressing on (time-series, point-in-time reconstruction, cross-event diffs) genuinely need it.

The user's "git on every change" question landed in exactly the right place: not the commits, but the content-addressed storage layer git pioneered. Sharedwatch can have that capability cleanly, in its own hidden directory, without ever touching the user's git repo.
