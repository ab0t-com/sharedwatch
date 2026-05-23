# Migrating agents from the pre-v0.8 skill to v0.8.0

What to change in an agent prompt / orchestration that targets the legacy `sharedwatch-client` skill. Everything in `sharedwatch-client-future` shipped in v0.8.0; this doc is the migration recipe for orchestrations written before that release.

## Contents
1. Detect the available feature set first
2. Mechanical replacements
3. Cursor name migration
4. payload_json → flag migration
5. Status output additions to consume
6. Patterns that change in spirit
7. Backward-compatible writing strategy

## 1. Detect the available feature set first

Don't assume. Detect:

```bash
HAS_OVERVIEW=$(sharedwatch overview --format json >/dev/null 2>&1 && echo yes || echo no)
HAS_ACTORS=$(sharedwatch actor heartbeat probe --dry-run >/dev/null 2>&1 && echo yes || echo no)
HAS_LEASES=$(sharedwatch lease list >/dev/null 2>&1 && echo yes || echo no)
HAS_MULTI_ROOT=$(sharedwatch status --json | jq -e '.roots' >/dev/null 2>&1 && echo yes || echo no)
HAS_ACTOR_FLAGS=$(sharedwatch test emit --help 2>&1 | grep -q -- '--actor' && echo yes || echo no)
```

Use these as feature gates. Falling back gracefully is part of being a good citizen across deployment versions.

## 2. Mechanical replacements

| Old (pre-v0.8) | New (v0.8.0+) | Notes |
|---|---|---|
| `sharedwatch sql "SELECT type, COUNT(*) FROM events WHERE ... GROUP BY type"` | `sharedwatch overview --format json` | overview returns the same data plus more, in one call |
| `sharedwatch sql "SELECT rel_path, COUNT(*) ... GROUP BY rel_path"` | `sharedwatch events stats --root <X> --format json` then read `top_paths` | scope-required forces the agent to pick |
| `--payload '{"actor":"X","session":"Y"}'` | `--actor X --session Y` | flags are validated, JSON isn't |
| `events list --since 1h` (single-root world) | `events list --root <X> --since 1h` (multi-root world) | only when multi-root is configured; single-root keeps working |
| Hand-rolled "is peer alive?" by scanning recent events | `status --actors --format json` | direct, doesn't burn query budget |
| Manual locking via "check then write" with no signal | `lease grant '<path>' --ttl 5m` | advisory but visible to peers |

## 3. Cursor name migration

In the single-root world, `<actor>-<task>` was enough. In multi-root, scope matters:

| Use | Cursor name template |
|---|---|
| Unified stream across all roots | `<actor>-<task>` |
| Per-root scoped stream | `<actor>-<task>-<root>` |

If you have agents using cursor names without the root suffix and you start scoping their queries with `--root`, **the cursor will silently skip events**. Reset or rename:

```bash
sharedwatch events cursor reset <old-name>
# then call events list with the new name and the new scope
```

The CLI warns when it detects a scope change on a known cursor — do not ignore the warning.

## 4. payload_json → flag migration

Old:
```bash
sharedwatch test emit auth/login.go --payload '{
  "schema_version":1,
  "actor":"claude-me-1",
  "session":"sess-abc",
  "task":"refactor-auth",
  "intent":"split JWT",
  "tags":["refactor","auth"]
}'
```

New:
```bash
sharedwatch test emit auth/login.go \
  --actor claude-me-1 \
  --session sess-abc \
  --task refactor-auth \
  --intent "split JWT" \
  --tag refactor --tag auth
```

The flag form is preferred because:
- Validation is real (typos in key names rejected at parse time vs silently ignored in JSON).
- The `actor` value flows into the promoted `events.actor` column for query speed.
- Easier to read in logs and shell history.

`--payload '<json>'` still works for keys not covered by flags. Use sparingly.

## 5. Status output additions to consume

In the single-root world, agents could safely assume `status --json` returned:
```json
{"mode":"...","pending":N,"failed":N,"processed":N, ...}
```

In multi-root, two new top-level keys may appear:
```json
{
  "mode":"...","pending":N,...,
  "roots": [...],     // present only if multi-root configured
  "actors": [...]     // present only if actors registry has entries
}
```

Code that destructures `status --json` with strict schemas will need to add tolerance for these. The keys are **additive** and the system promises no other shape changes — agents reading them can rely on the field names.

## 6. Patterns that change in spirit

**PATTERN: initial-orientation** — was three SQL aggregations. Now: one `overview` call. Token cost drops ~70%.

**PATTERN: peer-handoff-check** — was "events list + read recent actors." Now: also consult `intent list` and `lease list` to see *declared* upcoming activity, not just historical activity.

**PATTERN: publish-and-attribute** — was paired synthetic event (watcher emits, you emit a tagged copy). Now: a single attributed event is possible because `sharedwatch run --actor ...` tags every event the watcher emits during the run. No more paired duplicates.

**PATTERN: did-peer-respond** — unchanged in mechanism, but the `addressee` flag makes the original handoff cleaner: peer can filter on `--payload-key addressee --payload-value <them>` to find handoffs *intended for them*, instead of scanning all events.

**PATTERN: drill-from-overview (NEW)** — first call is `overview`, follow `drill` hints into `events stats`, then into `events list`. Replaces ad-hoc SQL exploration.

**PATTERN: lease-before-edit (NEW)** — wrap risky edits in a `lease grant`/`lease release` pair.

## 7. Backward-compatible writing strategy

If you maintain an agent that must work against multiple sharedwatch versions, write the agent like this:

1. **Detect features at startup** (§1).
2. **Use the v0.8.0 primitives when available**, falling back to pre-v0.8 forms otherwise.
3. **Always set `--actor`** (via flag if supported, via `--payload` JSON otherwise). It's the one universally-meaningful field.
4. **Always use cursors with the `<actor>-<task>[-<root>]` naming convention** even if you're in single-root today. Future-proofs.
5. **Tolerate unknown keys in JSON output.** Never destructure with strict schemas.
6. **Avoid `sql --write`** in any version. Operationally risky.

A polite agent works correctly on v0.7, works *better* on v1.0, and never breaks when the binary updates underneath it.

## Removal & renaming policies

The maintainers will:
- Never rename a published column.
- Never remove a published CLI flag without a release-cycle deprecation warning.
- Never change a `--format json` envelope shape (only add fields).

The maintainers may:
- Add new commands, new flags, new payload keys at any time.
- Reorder optional flags in `--help` output.
- Improve default-output rendering for `text` format (text is *not* a contract).

When in doubt, use `--format json` or `--format jsonl` — those are the stable contracts.
