# Multi-root operating reference

How to work with multiple watch-roots after SW-AGENT-3 lands.

## Contents
1. The one disambiguator rule
2. Root labels — grammar, defaults, collisions
3. Default behavior (single-root vs multi-root)
4. `--root` filter semantics
5. Cursor patterns across roots
6. Output column visibility
7. Status and roots discovery
8. Cross-root correctness invariants

## 1. The one disambiguator rule

`--root` has two meanings, disambiguated by the presence of `=`:

| Form | Meaning |
|---|---|
| `--root auth=/workspace/projects/auth` | **Definition** — register a root at this path with this label |
| `--root auth` | **Filter** — restrict the query to this root |

- `init`, `run`, `reconcile`: take definitions.
- `events list`, `digest list`, `status`, `events stats`: take filters.

Mixing produces a clear error: `events list does not accept root definitions; use 'init' or 'run' to register roots`.

## 2. Root labels — grammar, defaults, collisions

Rules:
- 1–64 chars from `[a-zA-Z0-9_-]`
- no leading dash
- no `=` or `,` (reserved by the CLI grammar)
- case-sensitive
- empty string `''` is reserved for legacy single-root events (do not use as a label)
- reserved labels: `all`, `none`, `mixed`

Defaults:
- If `--root /tmp/widgets` is passed without an explicit label, the label is auto-derived from `filepath.Base()` → `widgets`.
- Two paths with the same base name (`/a/work` and `/b/work`) **collide on label** — config refuses to load. Fix by giving them explicit labels: `--root a-work=/a/work --root b-work=/b/work`.

## 3. Default behavior (single-root vs multi-root)

The defaults tree, which the entire client experience depends on:

```
Is the system in single-root mode?
├── YES (one root, or only legacy --watch-path)
│   → behave identically to pre-multi-root:
│     - no watch_root column in events list output
│     - no roots[] in status --json
│     - --root filter on read commands is a no-op (warned, not errored)
│
└── NO (watch_roots config list, OR multiple --root <label>=<path>)
    Did the caller filter with --root?
    ├── YES → return matching root(s) only
    └── NO  → return all roots, but watch_root column SHOWN in output
              (so the caller can see why a result spans roots)
```

This is the entire defaults story. Agents written for the single-root world keep working; agents written for the multi-root world see the new column appear when it's relevant.

## 4. `--root` filter semantics

- `--root` is **repeatable** for OR-filter use:
  ```bash
  sharedwatch events list --root auth --root billing
  sharedwatch events list --root auth,billing       # comma-shorthand equivalent
  ```
- No `--not-root` flag — use SQL for exclusion.
- `--root ''` (empty string) returns only legacy single-root events. Escape hatch for audit.

## 5. Cursor patterns across roots

Two natural patterns:

**Unified cursor** — one bookmark across all roots:
```bash
sharedwatch events list --cursor-name <actor>-<task>
```
ASC iteration over `(created_at, id)` across every root. The right default for "what's new everywhere?"

**Per-root cursor** — one bookmark per root:
```bash
sharedwatch events list --cursor-name <actor>-<task>-<root> --root <label>
```
The cursor name encodes the scope. No enforcement; convention only.

**Mistake to avoid:** the same cursor name with and without `--root` advances over different result sets. The client warns when it detects a scope change on a known cursor. Do not ignore the warning — make a new cursor name.

## 6. Output column visibility

The `watch_root` column appears in `events list` output iff:
- The result set contains rows from more than one distinct `watch_root` value, OR
- The caller passed `--include-watch-root` to force it.

Same rule for `digest list`. The point: single-root invocations look identical to today; multi-root invocations reveal the column when it's load-bearing.

Default `--fields` for multi-root scope: `id, type, rel_path, watch_root, producer_id, created_at`. As of v0.8.0 there is no promoted `actor` column — query attribution through `--payload-key actor --payload-value <x>` or `json_extract(payload_json,'$.actor')` in SQL. The timestamp column is exposed as `created_at` by the CLI (see `sharedwatch-client/references/commands.md` for the `created_at` vs `observed_at` quirk).

## 7. Status and roots discovery

There is no standalone `roots` subcommand. Multi-root state is surfaced through `status`:

```bash
# Human-readable: text-mode status includes roots when multi-root is configured.
sharedwatch status
# mode: passive  pending: 12
# roots:
#   auth     /workspace/projects/auth     pending=3   last=2026-05-22T14:32:11Z
#   billing  /workspace/projects/billing  pending=9   last=2026-05-22T14:18:44Z

# Machine-readable: status --json carries a roots[] array.
sharedwatch status --json | jq '.roots'
```

`status --json` adds a `roots[]` array **only when multi-root is configured**:

```json
{
  "mode": "passive",
  "pending": 12,
  "roots": [
    {"label": "auth", "path": "/workspace/projects/auth",
     "pending": 3, "last_event_at": "...", "ok": true},
    {"label": "billing", "path": "/workspace/projects/billing",
     "pending": 9, "last_event_at": "...", "ok": true}
  ]
}
```

For single-root installs, no `roots` key is emitted.

## 8. Cross-root correctness invariants (good to know they exist)

The implementation guarantees:

- **Coalesce is root-scoped.** Two `README.md`s in different roots never merge.
- **Rename detection is root-scoped.** A delete in root A + create in root B with matching size/mtime is never paired as a rename.
- **Snapshots are keyed `(source, watch_root)`.** No bleed across roots.
- **Cold-start is per-root.** Adding a new root emits `file.created` for everything currently in it.

You don't have to think about these as an agent — they Just Work. They are listed here so you can sanity-check unexpected behavior: if cross-root pollution ever appears in your queries, file a ticket immediately. It's a bug, not an expected edge case.

## What's NOT supported (and won't be)

- Per-root mode (`active` for one root, `passive` for another). Mode is a property of the consumer's attention; the consumer reads from one DB.
- Per-root retention or ignore patterns (not in v0.8.0; planned only if/when a noisy-neighbor case emerges).
- Hot reconfig of roots at runtime. Restart with new config.
- Cross-root rename pairing. Two files with matching size/mtime in different roots are two events, not a rename.
- Cross-host or networked queues. Out of scope for v1.

When you hit any of these, the answer is "no" — don't work around it in agent code. File a ticket if you have a real case.
