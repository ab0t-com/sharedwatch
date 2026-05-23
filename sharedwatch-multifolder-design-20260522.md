# sharedwatch — multi-folder design (storage, access, control surface)

**Date:** 2026-05-22
**Status:** design doc — read-only, no code yet
**Builds on:** `tickets/ticket-multi-folder-watching-20260520_101921.md` (SW-AGENT-3)
**Companions:** `sharedwatch-multi-agent-discussion-20260522.md`, `sharedwatch-disclosure-attribution-discussion-20260522.md`

The SW-AGENT-3 ticket already documents *what* to build. This document is about *how to design it so it doesn't confuse anyone* — agents or humans. It's the design-quality layer over the implementation plan.

The single most important rule it tries to enforce:

> **The single-folder case must remain the default and must look exactly the same as it does today. Multi-folder is strictly additive; nobody who doesn't ask for it should ever encounter its complexity.**

---

## 1. Principles (these are load-bearing)

1. **Single-folder is the default.** Existing configs, existing CLI invocations, existing test scripts — nothing changes for users who don't opt in.
2. **Multi-folder is additive, not replacement.** No deprecation of `--watch-path`. No "use the new way." Both work.
3. **One process, one DB, one lock.** Multi-folder unifies *within* one sharedwatch instance. Multi-host or multi-DB topologies are deliberately not v1.
4. **Watch-roots are labels, not paths.** Agents reference `--root auth`, not `--root /workspace/projects/auth`. Labels are stable; paths drift.
5. **Cross-root correctness is non-negotiable.** Two `README.md`s in different roots never coalesce. A delete in root A + create in root B with matching size/mtime is never paired as a rename. Tests enforce this.
6. **Defaults reveal, don't hide.** Output should not silently add columns the user didn't ask for. With one root, the `watch_root` column shouldn't appear; with two, it should. Same for the `roots[]` array in `status`.
7. **Surprise minimization > feature maximization.** Every flag that could mean two things must be disambiguated by syntax, not by inference.

---

## 2. Storage pattern

### 2.1 Schema delta — columns, not tables

```sql
ALTER TABLE events     ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';
ALTER TABLE digests    ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';
ALTER TABLE snapshots  ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_events_root_created
  ON events(watch_root, created_at);
CREATE INDEX IF NOT EXISTS idx_events_root_relpath
  ON events(watch_root, rel_path, status, created_at);
```

The column holds the **label** (a short stable handle), not the path. Labels:
- 1–64 chars
- character class `[a-zA-Z0-9_-]`
- no leading dash
- no `=` or `,` (those are reserved by the CLI grammar)
- empty string means "legacy single-root" (existing rows; existing setups)

### 2.2 Why no `roots` registry table now?

A `roots` table mapping label → path → policy → quotas would be tempting:
```sql
CREATE TABLE roots (label TEXT PK, path TEXT, retention_days INT, quota_bytes INT, ...);
```

Don't build it yet. Three reasons:

- **Config, not state.** The label → path mapping is supplied at startup. It doesn't need to survive a restart of a *different* config.
- **No policy diversity yet.** Until we have per-root retention or per-root quotas, the table has no second column to justify it.
- **Adds a sync-with-config bug surface.** What happens when the config lists `auth` and `billing` but the DB has `auth`, `billing`, and `legacy-foo`? You now own the join semantics.

When per-root policy lands (probably with quotas), revisit. Until then, the column is enough.

### 2.3 Index strategy — what queries we expect

| Query pattern | Index used |
|---|---|
| "events for root X in last hour" | `idx_events_root_created` |
| "events on path P in root X" | `idx_events_root_relpath` |
| "all events in last hour" (global) | existing `idx_events_status_created` |
| "events for path P across roots" (rare) | seq-scan; acceptable |
| "events by actor" (after L1 attribution) | seq-scan + JSON; acceptable until promotion |

`(watch_root, created_at)` is the workhorse — agents will query per-root by time slice constantly. Make it covering if cost allows: ASC sort fits both cursor iteration and "latest first."

### 2.4 Legacy event handling

Pre-migration events have `watch_root = ''`. Two rules to follow:
- A query with no `--root` filter MUST return them (otherwise an upgrade silently changes what existing CLI calls return).
- A query with `--root <label>` MUST NOT match them (otherwise legacy events get re-attributed to the first new root added).
- A query with explicit `--root ''` returns only legacy events. (Escape hatch for audit.)

---

## 3. Access pattern

### 3.1 The defaults tree

The access pattern flows from this decision tree:

```
Is the system in single-root mode?
├── YES (one root, or only legacy --watch-path)
│   → behave identically to today:
│     - no watch_root column in output
│     - no roots[] in status
│     - no --root filter prompts shown
│
└── NO (multi-root: watch_roots list, OR multiple --root flags)
    Did the caller filter with --root?
    ├── YES → return matching root(s) only
    └── NO  → return all roots, BUT show watch_root column in output
              (so the user can see *why* a result spans roots)
```

This is the entire defaults story. Memorize it.

### 3.2 Filter semantics — `--root`

`--root` is **repeatable** and **comma-aware** for OR-filter use:
```bash
# Single root
sharedwatch events list --root auth

# Multiple roots (OR)
sharedwatch events list --root auth --root billing
sharedwatch events list --root auth,billing       # equivalent shorthand

# All roots (no filter)
sharedwatch events list
```

There is **no `--not-root`** flag — keep the surface small. If someone wants exclusion, use SQL.

### 3.3 Cursor semantics — unified vs per-root

Two cursor patterns naturally fall out:

**A. Unified cursor (default for "what's new everywhere?")**
```bash
sharedwatch events list --cursor-name my-agent --limit 100
```
Iterates ASC over `(created_at, id)` across all roots. One bookmark.

**B. Per-root cursor (for "what's new in *this* root?")**
```bash
sharedwatch events list --cursor-name my-agent-auth --root auth
```
The cursor name encodes the scope. The system does not enforce naming, but a clear convention beats no convention.

**Mistake to avoid:** the same `cursor-name` with and without `--root` advances over different result sets, which means it skips events. Make `events list` warn (not error) if a cursor's previous filter set differs from the current one. Document this loudly.

### 3.4 Output column visibility

The `watch_root` column appears in `events list` output iff:
- the result set contains rows from more than one distinct `watch_root` value, OR
- the caller passed `--include-watch-root` to force it.

Why this rule? Because piping single-root output into existing tooling shouldn't suddenly grow a column. But once an agent is genuinely operating in multi-root mode, hiding the column is misleading.

Same rule for `digests`: `digest list` adds a `roots` (or `watch_root` if one) column only when relevant.

### 3.5 Status surface

`status --json` adds a `roots[]` array **only when multi-root is configured**:

```json
{
  "mode": "passive",
  "pending": 12,
  "last_run": "...",
  "roots": [
    {"label": "auth", "path": "/workspace/projects/auth",
     "pending": 3, "last_event_at": "...", "ok": true},
    {"label": "billing", "path": "/workspace/projects/billing",
     "pending": 9, "last_event_at": "...", "ok": true}
  ]
}
```

For a single-root install, no `roots` key is emitted. This keeps `status --json` output identical to today for anyone not opting in.

---

## 4. Control surface (smart defaults)

### 4.1 Argument grammar — the one rule

`--root` has two meanings, disambiguated by the presence of `=`:

| Form | Meaning |
|---|---|
| `--root auth=/workspace/projects/auth` | **Definition** — register a root at this path with this label |
| `--root auth` | **Filter** — restrict the query to this root |

The `=` is the disambiguator. No mode flags, no positional dance. The CLI parser inspects each `--root` value: if it contains `=`, it's a definition; otherwise it's a filter.

This is the kind of overload that *would* be a footgun in a different domain, but here the asymmetry is benign:
- `init`, `run`, `reconcile` only take definitions.
- `events list`, `digest list`, `status` only take filters.
- A misuse fails with a clear error: `"events list does not accept root definitions; use 'init' or 'run' to register roots"`.

### 4.2 Label rules (recap, normative)

| Rule | Why |
|---|---|
| 1–64 chars `[a-zA-Z0-9_-]`, no leading dash | URL-safe-ish, shell-safe, agent-prompt-safe |
| Auto-derived from basename if not specified | `--root /tmp/widgets` → label `widgets` |
| Collisions rejected at config load | Two paths with the same auto-label → error pointing to `--root <label>=<path>` |
| Reserved labels: `all`, `none`, `mixed`, empty string | Reserved for filter sugar / mixed-root digests |
| Case-sensitive | Predictable; SQL matching is byte-exact |

### 4.3 Backward-compat behavior of `--watch-path`

```bash
# Today (single root, unchanged)
sharedwatch --watch-path /foo run

# Becomes (multi-root, additive)
sharedwatch --watch-path /foo --watch-path /bar run
# → two roots, labels auto-derived: 'foo' and 'bar'

# Mixed (allowed)
sharedwatch --watch-path /legacy --root tagged=/new run
# → two roots: 'legacy' and 'tagged'
```

If the user only passes one `--watch-path` and no `--root`, the system is in single-root mode (the default-default).

### 4.4 Path normalization

- All paths resolved to absolute at config load.
- `~` expansion applied.
- Symlinks: resolved once at start (real path stored), but not re-resolved every tick. This avoids loops.
- Trailing slashes stripped.
- Overlapping paths (`/a` and `/a/b`) are allowed but **warn** at startup: events in `/a/b` will appear under both roots if both are configured. Don't deduplicate magically; that hides bugs.

### 4.5 Collision and conflict handling

| Situation | Behavior |
|---|---|
| Two `--root` with same label | Error at startup. |
| Two `--root` with same path | Warn; keep the first occurrence. |
| One root inside another (`/a` and `/a/b`) | Warn; events fire under both. |
| Root path doesn't exist | Auto-mkdir (matches single-root behavior). |
| Root path disappears at runtime | Watcher tick logs error, continues other roots; reconcile catches up when path returns. |
| Root path is not a directory (file/socket) | Error at startup, refuse to run. |

### 4.6 Mode is global, not per-root (explicit non-feature)

It will be tempting. Resist:
- `mode active` flips the consumer cadence to fast. The consumer reads from one DB.
- "Active for root X, passive for root Y" doesn't make sense for a single consumer.
- If an agent wants per-root cadence, it polls per-root with `events list --root X`.

This is settled in SW-AGENT-3; restating here because every future contributor will re-propose it.

---

## 5. Agent ergonomics (drill flows)

The multi-folder design opens up the L1 → L2 → L3 drill flow from the progressive-disclosure doc:

```bash
# L1 — across all roots
sharedwatch overview --format json
# → roots: [{label, pending, last_event_at, churn_24h}, ...]

# L2 — into one root
sharedwatch events stats --root auth --format json
# → per-root: top_paths, top_actors, type_histogram

# L3+ — into the events themselves
sharedwatch events list --root auth --since 1h --format jsonl
```

For this drill flow to work cleanly:
- `overview` always returns one row per root.
- Each row includes a `drill` hint pointing to the L2 endpoint for that root.
- `events stats` always operates on exactly one root (`--root` required). Forces the agent to pick.

This makes the multi-folder feature *and* the progressive-disclosure design mutually reinforcing — neither shines on its own; together they make sharedwatch feel like a coordinator.

---

## 6. Discovering roots

Two endpoints:

```bash
# Compact list of configured roots
sharedwatch roots
# auth     /workspace/projects/auth     pending=3   last=2026-05-22T14:32:11Z
# billing  /workspace/projects/billing  pending=9   last=2026-05-22T14:18:44Z

# JSON for agents
sharedwatch roots --format json
# [{"label":"auth","path":"/workspace/projects/auth","pending":3,...}, ...]
```

`status --json | jq .roots` returns the same info — `sharedwatch roots` is a friendlier alias. (Or skip the alias; one fewer command surface.)

---

## 7. Failure modes — by intent

Designed-for failures (must work):
- DB locked briefly → busy_timeout waits; retry succeeds (current behavior).
- One root's path disappears → other roots keep working; that root's events stop arriving; reconcile flags it.
- Hostname change → `producer_id` default rotates; old events keep their old producer.
- Disk fills → inserts fail; watcher logs and exits cleanly; manual recovery.

Undesigned-for failures (won't fix in v1):
- Cross-host filesystem races on NFS/SMB.
- Symlink loops between roots.
- Adding/removing a root without restart.
- Two sharedwatch processes against the same DB (file lock prevents).
- Encrypted-at-rest filesystem with mtime granularity > 1s (events may be lost between same-second writes).

---

## 8. Smart defaults — one-page reference

| Surface | Single-root default | Multi-root default |
|---|---|---|
| `--watch-path` | one value | repeatable, label auto-derived |
| `--root` (definition) | not used | `--root label=path` |
| `--root` (filter) | not applicable | unset = all roots |
| `events list` `watch_root` column | hidden | shown when result spans roots |
| `status --json` `roots[]` | absent | present |
| `digest list` `watch_root` column | hidden | shown when relevant |
| Cursor with no `--root` | unified | unified (across all roots) |
| Cursor with `--root X` | unified | scoped to X |
| Reconcile cadence | global | global (no per-root) |
| Coalesce window | global | global |
| Retention days | global | global (until per-root quotas land) |
| Ignore patterns | global | global |
| Active/passive mode | global | global |
| Producer ID default | `<host>:<pid>` | `<host>:<pid>` (same) |

---

## 9. Future extensions (named, not built)

| Extension | Trigger to build |
|---|---|
| `roots` registry table | When we have per-root retention/quotas/policy |
| Per-root retention | When one noisy root proves it pushes out useful events from quiet roots |
| Per-root mode (DON'T) | Never |
| Per-root ignore patterns | When evidence shows global patterns conflict |
| Hot reconfig (add root without restart) | When restart cost is measurable customer pain |
| Cross-root rename pairing (DON'T) | Never — it breaks the cross-root correctness invariant |
| Per-root payload-schema enforcement | When we have content-aware tooling on the journal |
| Cross-DB join across hosts | Never in v1 (publish to a real queue instead) |

---

## 10. Open questions

1. **Should `events stats --root <X>` require `--root`, or default to all roots?** Recommendation: require. Forces the agent to pick scope, which is the natural progressive-disclosure motion. (Mirrors how `git log --oneline` accepts a default but `git diff` doesn't.)
2. **Should `digest`s be per-root or mixed?** Default: mixed digests, with `watch_root = "mixed"` when the batch spans roots. Per-root opt-in via a future `--per-root` flag on `consume`. (Matches the SW-AGENT-3 recommendation.)
3. **Should the `roots` command actually exist, or is `status --json` enough?** Skip it unless a real user asks. Fewer commands = fewer surface bugs.
4. **What happens if two roots have the same auto-derived label?** Error at config load (already covered in §4.2). Open: do we *also* check at runtime if a config file is edited while sharedwatch is running? Answer: no — config is read at startup; hot reconfig isn't supported.
5. **Should we add `--include-watch-root` and `--exclude-watch-root` for output control?** Recommend only `--include-watch-root` — exclude is rare and SQL covers it.
6. **Should the auto-derived label respect Unicode?** Probably ASCII-only for safety. Reject `--watch-path /foo/Δ` from auto-deriving `Δ`; require explicit `--root delta=/foo/Δ`.

---

## 11. Implementation checklist (echoes SW-AGENT-3 — for cross-reference)

This document is design-level; the implementation tasklist is in SW-AGENT-3 §9. Read this for *why* the choices are what they are; read SW-AGENT-3 for *what* to type.

The acceptance criteria in SW-AGENT-3 §5 should be re-run against this document's defaults table (§8). Any divergence is a bug in one of the two documents — flag and reconcile.

---

## Bottom line

If you do exactly one thing from this document: **commit to the rule that single-folder users see no change.** Every flag, every output column, every status field that could surprise them is a tax on a feature most users won't opt into. The multi-folder mode rewards the agent use case without punishing the human use case. That asymmetry is the whole product design.
