# Output contract

Authoritative reference for the JSON / JSONL / CSV shapes sharedwatch emits.
This is a contract: published fields are stable; only additive changes happen
inside a `format_version`. Breaking changes bump the version.

## Versioning rule

Every structured envelope carries `format_version` as the first key:

```json
{"format_version": 1, ... }
```

The version-bump policy:

- **Bump `format_version`** when a published field is renamed, removed, has its
  type changed, or its semantics change in a way that would silently break a
  consumer who didn't know.
- **Do NOT bump** for:
  - new fields added (consumers must tolerate unknown keys)
  - new optional keys appearing in existing maps
  - new entries in lists, new keys in maps
  - changes to text-format output (text is NOT a contract — see below)

A deprecation window of at least one release accompanies any version bump.
Both versions are emitted side-by-side during the window so consumers can
migrate.

## Surfaces

| Command | Envelope | `format_version` | Notes |
|---|---|---|---|
| `events list --format json` | `{format_version, columns, rows, next_cursor?}` | yes | cursor only when relevant |
| `events list --format jsonl` | one row per line + final `{format_version, next_cursor}` sentinel | yes (in sentinel) | recommended for agents |
| `events list --format csv` | header + rows + `# next_cursor,<tok>` trailer | n/a | text-format-adjacent |
| `events list --format text` | tab-aligned, `# next_cursor=<tok>` trailer | n/a | **NOT a contract** |
| `sql --format json/jsonl/csv` | same shape as `events list` | yes | uses shared output package |
| `schema --format json` | `[{name, sql, columns: [...]}, ...]` | **NO (special case)** | bare array — see exception below |
| `schema --format text` | DDL concatenated with `;` | n/a | text format |
| `status --json` | `{format_version, mode, pending, ..., roots?, actors?}` | yes | roots/actors only when relevant |
| `overview --format json` | `{format_version, generated_at, since, mode, ..., drill}` | yes | L1 envelope |
| `events stats --format json` | `{format_version, generated_at, root, window, by_type, ..., drill}` | yes | L2 envelope |
| `actor heartbeat` | `heartbeat <actor-id>` line | n/a | side-effect command, no JSON |
| `intent list --json` / `lease list --json` | bare JSON array of records | no (kept simple) | each record carries its own field names |
| `lease grant` JSON response | `{lease_id, granted, expires_at, conflict_with?}` | no (kept simple) | small fixed shape |
| `digest list` text + `--status`/`--root` filters | line-per-digest | n/a | text format |

## The `schema --format json` exception

`schema --format json` returns a bare JSON array of table descriptors, NOT a
`{format_version, ...}` envelope. Rationale: the response IS the schema —
self-describing. A future schema-shape change would necessarily break
consumers regardless of whether format_version were present. If a change
becomes necessary, the migration would emit an envelope at that point and
publish a deprecation notice.

## Text-format outputs

Anything emitted by `--format text` (or the default text format for `status`,
`digest list`, etc.) is NOT a contract. The renderer can change at any time.
Agents that parse text output are wrong; point them at `--format json` or
`--format jsonl`.

## JSONL specifics

JSONL streams emit one row object per line. When a cursor applies, the final
line is a sentinel object: `{"format_version": 1, "next_cursor": "..."}`. The
sentinel does NOT carry row fields, so a consumer parsing each line as a
discriminated union (row vs sentinel) can tell them apart.

When no cursor applies, NO sentinel line is emitted. Consumers should not
assume one.

## Field-naming conventions

- snake_case for JSON field names.
- RFC3339Nano for timestamps (UTC).
- Empty optional fields use `omitempty` — they are absent rather than null.
- Path-style fields are POSIX-forward-slash regardless of host OS.

## Adding a new field — the checklist

Before publishing a new JSON field:

1. Confirm consumers can ignore unknown keys. (All current agent code paths
   in the Skills and the SDK do.)
2. Add the field with `,omitempty` if it can be absent.
3. Document it in this file under the relevant surface.
4. Add a test that asserts presence / absence on a representative call.

## Breaking-change checklist

Before bumping `format_version`:

1. Open a ticket with the rationale and the deprecation window.
2. Implement both versions; pick the one to emit via a config flag (defaulting
   to the current version).
3. Announce in CHANGELOG with explicit migration notes.
4. Ship the dual-emission release; collect at least one cycle of feedback.
5. Flip the default to the new version in the next release; remove the old
   emission path one release later.

This process is intentionally slow. The contract matters more than the
convenience of any single change.
