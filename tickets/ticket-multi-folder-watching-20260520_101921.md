# TICKET: Multi-folder watching — one sharedwatch, one DB, many roots

**ID:** SW-AGENT-3
**Filed:** 2026-05-20 10:19 UTC
**Filed by:** claude
**Status:** open, not yet claimed
**Target:** sharedwatch v0.8.x
**Estimated:** ~1.5 focused engineer-days (real schema work + cross-root correctness paths)

---

## 1. Context

Repo state at filing: v0.7.3-20260520, 11 packages green. SW-AGENT-1, 2, 4, 5, 6 closed; this is the last open Gap from the agent-fit analysis.

Back-refs:
- agent-fit doc `sharedwatch-agent-fit-20260520_034245.md` §4 Gap 5 (multi-folder), §6 "yes, comfortably" verdict — currently blocked on this feature for the many-threads case.
- prior ticket `ticket-agent-event-access-20260520_035113.md` (SW-AGENT-1) — explicitly defers multi-folder watching to this ticket (§4).
- prior tasklists: `tasklist_20260520_022339.md`, `tasklist_20260520_040435.md`, `tasklist_20260520_073211.md`.
- design constraint baseline: `shared-drive-watcher-spec.md` (the original John spec) — describes a SINGLE folder. This ticket is an additive extension; the single-folder shape stays the default.

---

## 2. Why this matters — the LLM-agent use case in concrete detail

The "user" we're designing for is not a human dropping into a folder. It's an LLM agent shepherding many concurrent threads of work where each thread lives in its own filesystem subtree, but the agent itself wants a single, unified view of "what's happened across everything I care about."

This is the use case the current single-`WatchPath` shape actively obstructs. Walk through what an agent actually does:

### Scenario A — workspace-spanning coordinator
An agent is managing three workstreams in parallel:
- `/workspace/projects/auth/` (refactor)
- `/workspace/projects/billing/` (bug fix)
- `/workspace/projects/api/` (design doc)

Every 5 minutes the agent asks itself: "Across all my open threads, which one had recent activity I should attend to first?" Today the agent has to:
- run three `sharedwatch` instances against three DBs,
- issue three `events list` calls,
- merge three streams in its own head,
- maintain three cursors,
- and reconcile three locking concerns.

What it actually wants: `sharedwatch events list --since 5m --order desc --limit 50`, with a `watch_root` column it can group by. One process, one DB, one query, one cursor.

### Scenario B — multi-inbox agent
An agent receives instructions from multiple channels, each channel materialized as a folder:
- `/inbox/from-human/` — direct user prompts
- `/inbox/from-agent-x/` — peer agents' handoffs
- `/inbox/from-ci/` — automated pipeline outputs

The agent's prioritization logic is: "Read newest-first across all three inboxes; humans always preempt agents; CI never preempts." That priority logic needs to *see* events from all three sources in one ordered stream, with a `--root` filter available to scope when needed. Today, the agent can't sort by chronology across roots — it has to query each separately and merge.

### Scenario C — coordinator monitoring sub-agent outputs
A parent agent has dispatched N sub-tasks. Each sub-task writes to its own output folder. The parent's question is: "Which sub-agents have produced output since the last time I checked, and which haven't?"

This needs a per-root query: `events list --root /tasks/task-abc --since-cursor <parent-cursor>` returns activity for that sub-task. The agent runs N such queries with the same cursor pattern, with shared infrastructure (one DB, one process, one set of pruning policies) underneath.

### Scenario D — cross-cutting concerns
An agent watches several disjoint directories that the human has organized differently:
- `~/notes/` (markdown ideas)
- `~/code/scratch/` (throwaway experiments)
- `/tmp/agent-out/` (this agent's own scratch)

These don't fit under a common parent, and even if they did, a recursive watch on `~` would be wrong. The agent wants three explicit roots in one process.

### Scenario E — initial-context priming
On startup, an agent often constructs its initial situation awareness by asking: "What did I touch across all my work directories in the last hour?" Today this is impossible in one call. Multi-folder support makes it one query, which the agent can stuff into its context window as a starting frame.

### Scenario F — short-lived task folders
Some agents spawn ephemeral work directories per task: `/work/task-abc/`, `/work/task-def/`, each living for the duration of one task and then being archived. The agent wants the *parent* `/work/` to be the watched root, AND wants to be able to filter by `--path-glob 'task-abc/**'` to get just that subtree's activity. This is already possible with the current single-root setup — but only if the agent can guarantee everything lives under one parent. The moment the agent's thread topology spreads across `/work/...` and `/home/...`, single-root breaks down.

### What the agent really wants from "multi-folder"
Boiled out of A–F, the requirements are:

1. **One process, one DB, many physical paths.** Operational simplicity. One file lock. One reconcile heartbeat. One cursor name space.

2. **A `watch_root` dimension on every event.** So the agent can group, filter, and decide priority by root.

3. **Stable handles for roots.** `--root auth` is friendlier than `--root /full/long/path/to/workspace/projects/auth/` when the agent is constructing queries from memory or LLM-generated tool calls. Labels matter.

4. **Cross-root cursor that advances over the unified stream.** When the agent's question is "what's new everywhere since last check," one cursor over all events is the right primitive. Per-root cursors are achievable through `--root` + named cursors, but a default unified cursor is the common case.

5. **Per-root cold-start emission.** When a new root is added to the config, the agent expects to see `file.created` events for everything currently in that root — the same one-time bootstrap behavior we shipped for the single-root case.

6. **Cross-root correctness invariants.** Two files named `README.md` in different roots must NOT coalesce. A delete in root A plus a create in root B with the same size/mtime must NOT be paired as a rename. Anything less than this is a footgun that will fire during real usage and silently corrupt the journal.

7. **Backward compatibility.** Existing single-root configs and DBs must keep working. The single-root case stays the README's default. Multi-root is a strict extension.

### What the agent does NOT need (and we should not build now)
- **Per-root mode (active/passive) independence.** Tempting, but mode is a global property of the consumer's attention. If the agent wants fast updates for a specific root, it can filter at read time. Mode-per-root is scope creep.
- **Per-root ignore patterns / retention / coalesce windows.** All operational knobs stay global. Heterogeneity per root inflates the config surface for marginal value.
- **Per-root file lock or per-root DB.** The whole point is unification.
- **Hot reconfiguration (add/remove root at runtime).** Re-launch on config change. Standard pattern.

---

## 3. In scope (this ticket)

### A — Config shape

```go
type Config struct {
    // Legacy single-root field. Still honored; when set and WatchRoots is
    // empty, treated as a single unlabeled root for backward compatibility.
    WatchPath string

    // New: explicit named roots. Each is `{Label, Path}`. Label is what
    // appears in --root flags, in the `watch_root` event column, and in
    // status output. Path is the absolute or expanded-relative directory.
    WatchRoots []WatchRoot
    // ... rest unchanged
}

type WatchRoot struct {
    Label string // stable handle; defaults to filepath.Base(Path) if empty
    Path  string // expanded, absolute
}
```

Config file shape (additive — both forms supported):
```yaml
# legacy single-root (unchanged):
watch_path: /home/foo/watched

# OR new multi-root:
watch_roots:
  - label: auth
    path: /workspace/projects/auth
  - label: billing
    path: /workspace/projects/billing
  - path: /tmp/agent-out      # label auto-derived as "agent-out"
```

CLI:
- `--watch-path <path>` becomes repeatable (current `csvList` semantics; backward compatible since single use still works). When repeated, each entry becomes an unlabeled WatchRoot (label = base name).
- New `--root <label=path>` for explicit labeling: `--root auth=/workspace/projects/auth`. Repeatable.

Precedence: defaults < config file < flags. Flag-supplied roots REPLACE (not append) config-file roots so an agent can do `--root x=/path1 --root y=/path2` without inheriting whatever was in `config.yaml`.

### B — Schema changes (events + snapshots)

Two idempotent migrations via the existing `addColumnIfMissing` helper:

```sql
ALTER TABLE events ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';
ALTER TABLE snapshots ADD COLUMN watch_root TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_events_root_created ON events(watch_root, created_at);
CREATE INDEX IF NOT EXISTS idx_events_root_relpath ON events(watch_root, rel_path, status, created_at);
```

The `watch_root` column carries the **label**, not the path. Labels can be 1–64 chars; recommend `[a-z0-9-_]`. Validate in config load.

Legacy events keep `watch_root = ''`. For an old DB upgrading to multi-root, the migration assigns no root retroactively; queries with `--root <label>` won't match them (correct).

### C — Pipeline correctness changes (the critical part)

These are the cross-root invariants. Test coverage is non-negotiable.

1. **Coalesce lookup** (`Store.FindRecentPendingByRelPath`) takes an additional `watchRoot string` parameter. Two `README.md`s in different roots do not merge.
2. **Rename detection** (`watcher.DetectRenames`) runs **per root**. The function as it stands is given a single slice of events that came from a single diff pass; that's already per-root if we call it per-root. So the structural change is: the watcher loop iterates roots, builds a per-root snapshot, diffs against the per-root previous snapshot, runs DetectRenames on per-root output, then inserts each event tagged with that root's label.
3. **Snapshot lookup** (`Store.LatestSnapshot`) takes `watchRoot`. The composite key for the snapshot identity becomes `(source, watch_root)`.
4. **Snapshot pruning** (`Store.PruneOldSnapshots`) takes `watchRoot`, prunes per `(source, root)` rather than per source.
5. **Cold-start emission** (in both `Watcher.ScanAndQueue` and `Reconcile.RunNow`) runs once per root. First reconcile against an empty DB with 3 populated roots produces N+M+P `file.created` events.
6. **Auto-mkdir** (`app.NewWithLogger`) creates each root's directory, not just `cfg.WatchPath`.

### D — Read surface (events list / events cursor / digest list)

Additive flags:
- `events list --root <label>` (repeatable for OR-filter, comma-aware) — empty = all roots.
- `events list` output adds a `watch_root` column (alongside the existing `producer_id`, `payload_json` etc.) Default `--fields` should include it when multi-root is in use; otherwise hide it for the single-root case to avoid clutter.
- `events list --group-by-root` (boolean) — sorts/groups output by root, then by `--order`. Useful for "show me each root's activity separately in one call."
- `digest list --root <label>` — filter digests by which root's events they consumed. Requires `digests.watch_root` column too. (Consumer-side: when consuming, the digest takes the dominant `watch_root` of its events; if mixed, label `"mixed"`. See open question #2.)
- `EventFilter.WatchRoots []string` added.

### E — Status surface

`sharedwatch status` and `status --json` add a `roots` field:
```json
{
  "mode": "passive",
  "roots": [
    {"label": "auth", "path": "/workspace/projects/auth", "pending": 3, "last_event_at": "..."},
    {"label": "billing", "path": "/workspace/projects/billing", "pending": 0, "last_event_at": null}
  ],
  ...
}
```

`pending` and `last_event_at` are per-root (one SQL aggregate). This is the most-asked agent question ("which roots are active right now?"); making it cheap is high-leverage.

### F — `init` subcommand

`sharedwatch init` already prints the resolved paths. Update it to print one line per root.

### G — `schema` discovery

Already reads `sqlite_master` — no code change needed; users will see `watch_root` columns appear automatically after the migration. Sanity-check this in the dogfood.

---

## 4. Out of scope

- Per-root active/passive mode.
- Per-root ignore patterns / coalesce windows / retention policies.
- Per-root reconcile cadence.
- Hot reconfiguration of root list at runtime (kill + restart).
- Per-root file locks (the one process-level lock already covers the DB).
- Mode of operation where each root has its own DB (we already support this via multiple instances — that's the workaround we're replacing).
- Symlink loops across roots (deferred to a separate hardening pass).

---

## 5. Acceptance criteria

Copy-paste against a fresh checkout:

```bash
# A1: two roots via flag, init creates both
sharedwatch \
  --root auth=/tmp/sw-multi/auth \
  --root billing=/tmp/sw-multi/billing \
  init
# Expects two watch_path lines.

# A2: drop a file in each, reconcile produces events tagged per-root
echo "x" > /tmp/sw-multi/auth/login.go
echo "y" > /tmp/sw-multi/billing/charge.go
sharedwatch \
  --root auth=/tmp/sw-multi/auth \
  --root billing=/tmp/sw-multi/billing \
  reconcile now
# Expects: recovery_events=2

# A3: events list shows both with watch_root column
sharedwatch \
  --root auth=/tmp/sw-multi/auth \
  --root billing=/tmp/sw-multi/billing \
  events list --fields rel_path,watch_root,type --format text
# Expects: login.go|auth|file.created and charge.go|billing|file.created

# A4: per-root filter
sharedwatch \
  --root auth=/tmp/sw-multi/auth \
  --root billing=/tmp/sw-multi/billing \
  events list --root auth --format json | jq '.rows | length'
# Expects: 1

# A5: coalesce does NOT merge across roots — README.md in both roots stays distinct
echo "a" > /tmp/sw-multi/auth/README.md
echo "b" > /tmp/sw-multi/billing/README.md
sharedwatch ... reconcile now
sharedwatch ... events list --path-glob 'README.md' --format json | jq '.rows | length'
# Expects: 2  (NOT 1)

# A6: rename pairing does NOT cross roots
# delete one root's foo.md, create another root's foo.md with identical size+mtime
# - expected: 1 delete + 1 create, NOT 1 rename
mv /tmp/sw-multi/auth/login.go /tmp/sw-multi/billing/login.go    # WRONG, would be a cross-root case if pairing crossed roots
sharedwatch ... reconcile now
sharedwatch ... events list --format text | grep -E 'file\.deleted|file\.renamed|file\.created'
# Expects: file.deleted login.go watch_root=auth AND file.created login.go watch_root=billing.
# (no file.renamed)

# A7: status surfaces per-root pending counts
sharedwatch ... status --json | jq '.roots'
# Expects: array of {label, path, pending, last_event_at}

# A8: cursor advances over the unified stream
sharedwatch ... events cursor reset agent-x
sharedwatch ... events list --cursor-name agent-x --limit 100 --format jsonl | wc -l
# Returns all events across roots in one ordered stream + cursor sentinel.

# A9: per-root cursor pattern still works via naming convention
sharedwatch ... events list --root auth --cursor-name agent-x-auth --format jsonl

# A10: backward compatibility — single --watch-path still works
sharedwatch --watch-path /tmp/sw-single/watch init
sharedwatch --watch-path /tmp/sw-single/watch events list --format text
# Expects: no errors; events have watch_root='' (legacy single-root)

# A11: legacy DB upgrades cleanly
# Open a v0.7.3 DB that pre-dates this migration; new binary should migrate
# without losing events. (Verify by row count before/after.)
```

Plus: every existing test still passes; `gofmt -l .` clean; `go vet ./...` clean.

---

## 6. Required tests

- `internal/db/multi_root_test.go`
  - `TestCoalesceScopedToRoot`: insert two same-relpath events with different roots, verify both survive.
  - `TestSnapshotLookupScopedToRoot`: latest snapshot per (source, root) doesn't bleed across roots.
  - `TestEventFilterByRoot`: `EventFilter.WatchRoots` filters correctly.
- `internal/watcher/rename_test.go` add:
  - `TestRenameDoesNotCrossRoots`: a delete in root A + create in root B with identical size/mtime stays as two events, not a rename.
- `internal/reconcile/reconcile_test.go` add:
  - `TestReconcileColdStartPerRoot`: three roots, each populated, first reconcile emits N+M+P events.
- `internal/app/multi_root_test.go`:
  - `TestStatusPerRoot`: with two roots populated, status JSON has the right per-root counts.
- Schema migration test:
  - `TestMigrateAddWatchRootColumn`: open a v0.7-style DB, re-open with the new binary, assert column exists and old events have `watch_root = ''`.

---

## 7. Open questions

1. **Multi-root digest semantics — one digest or one per root?**
   Proposal in this ticket: one digest covering all events in a consume window, with `digests.watch_root = ''` (or `"mixed"`) when events span roots. An alternative is *per-root digests* — the consumer produces one digest per root per cycle. Per-root is more agent-friendly but inflates digest counts. **Recommended default: single mixed digest, with `--root` filter on the consume step to opt into per-root digests.** Confirm.

2. **Should existing events with `watch_root=''` show up in `--root <label>` queries?**
   Proposal: no, never. Legacy single-root events should only be returned when no `--root` filter is set, or when the user explicitly passes `--root ''`. Otherwise a binary upgrade silently changes what an agent's existing queries return.

3. **`--root` as filter vs `--root` as definition.**
   I've used `--root auth=/path` for definition and `--root auth` for filter. The flag has two meanings depending on context. Alternative: keep `--root` for definition only and introduce `--in-root` (or reuse `--source`) for filtering. **Recommended: use the same `--root` flag, disambiguated by the presence of `=` (definition has `=`, filter doesn't).** Confirm.

4. **Label validation.**
   Proposal: `[a-zA-Z0-9_-]{1,64}`, no leading dash. Empty label = derive from base name. Reject `=` and `,`. Confirm.

5. **What about a `default` / unlabeled root?**
   For the single-root legacy case, the implicit label is `""`. For the new case, if the user passes `--watch-path /x` (without `--root x=/x`), the label is derived from `filepath.Base(path)`. There's a small UX trap: two paths with the same base name (`/a/work` and `/b/work`) would collide. **Recommended: reject collisions at config load with a clear error pointing to `--root <label>=<path>`.** Confirm.

6. **Backward compat for the `events.watch_root` column in `events list` output.**
   With single-root setups, `watch_root` is always `''` and the column is noise. **Recommended: hide the column by default when all visible events have empty `watch_root`; show it when at least one is non-empty.** Confirm — or just always show it for consistency.

---

## 8. Risk / concerns

- **Cross-root coalesce/rename correctness is the load-bearing safety property.** Without it, two unrelated `README.md`s silently merge, or a delete/create pair across roots becomes a phantom rename. Every test in §6 must exist and pass before merge.
- **Schema migration must be idempotent on legacy DBs.** The existing `addColumnIfMissing` helper covers the column add; the new indexes need `IF NOT EXISTS`. Test on a real v0.7 DB.
- **Snapshot table key change is subtle.** Code currently looks up "latest snapshot WHERE source = ?". Multi-root needs "latest snapshot WHERE source = ? AND watch_root = ?". Missing the second clause silently uses one root's snapshot for another, producing whole-folder phantom create/delete cascades.
- **Reconcile retention pruning** (`PruneOldSnapshots(source, keep)`) must become `(source, root, keep)` or the keep window collapses across roots.
- **Status output schema is a contract** once agents consume it. Adding `roots[]` is additive; renaming or restructuring is not. Lock the JSON shape in this ticket.
- **The `--watch-path` flag double-meaning.** Currently single-value; making it repeatable changes behavior subtly when used multiply. Decision: silently allow repetition (already a `csvList` would handle this); existing single-use callers unaffected.
- **`producer_id` is not changed by this ticket.** Producer remains "who is running sharedwatch," not "which root." If we ever want per-root producer attribution (e.g., a single sharedwatch ingesting from N different agents writing to N different roots), that's a separate concern. Note for future SW-AGENT-X.

---

## 9. Deliverables checklist (for the eventual tasklist)

- [ ] `Config.WatchRoots []WatchRoot` + `WatchRoot{Label, Path}` type.
- [ ] Config file parser handles `watch_roots:` list (alongside legacy `watch_path:`).
- [ ] CLI `--root <label>=<path>` (definition, repeatable) and `--root <label>` (filter, repeatable) with the `=` disambiguator.
- [ ] CLI `--watch-path` becomes repeatable.
- [ ] Schema migrations: `events.watch_root`, `digests.watch_root`, `snapshots.watch_root`, new indexes.
- [ ] `Store.FindRecentPendingByRelPath` signature extended with `watchRoot`.
- [ ] `Store.LatestSnapshot` + `Store.SaveSnapshot` + `Store.PruneOldSnapshots` take `watchRoot`.
- [ ] `EventFilter.WatchRoots []string` filter; default-emit `watch_root` column in `events list` (when non-empty).
- [ ] `Watcher.Service` loops over roots; per-root cold-start; per-root rename pairing.
- [ ] `Reconcile.Service` loops over roots; per-root cold-start; per-root drift.
- [ ] `app.New` auto-mkdir each root.
- [ ] `status` + `status --json` include `roots[]` array with per-root pending counts and last_event_at.
- [ ] `events list --root <label>` filter (repeatable, comma-aware).
- [ ] `digest list --root <label>` filter (post-§7-Q1 resolution).
- [ ] Tests per §6.
- [ ] `CHANGELOG.md` `[Unreleased]`.
- [ ] `README.md` adds a "Multiple folders" example block to the Quickstart, with a clear note that single-root is still the default.
- [ ] `docs/SCHEMA_CONTRACTS.md` updated for `watch_root` columns.
- [ ] Dogfood pass: §5 acceptance block.

---

## 10. References

- `sharedwatch-agent-fit-20260520_034245.md` §4 Gap 5 — original motivation, framed for humans. This ticket reframes it for the agent use case.
- `ticket-agent-event-access-20260520_035113.md` §4 — the prior ticket that explicitly deferred multi-folder watching.
- `tasklist_20260520_073211.md` — the session 5 worklog noting this as the one remaining real feature gap.
- `sharedwatch/internal/watcher/service.go` — current single-root scan loop; reference for what becomes per-root.
- `sharedwatch/internal/reconcile/reconcile.go` — current single-root reconcile + cold-start; same.
- `sharedwatch/internal/db/db.go` — current `FindRecentPendingByRelPath`, `LatestSnapshot`, `PruneOldSnapshots` signatures.

---

## 11. Definition of done

- All §5 acceptance commands work against a real DB on a clean Linux host.
- All §6 tests pass.
- `gofmt -l .` / `go vet ./...` / `go test ./...` clean.
- Single-root callers unaffected (existing tests still green).
- A worklog entry in the next session's `tasklist_<bashdate>.md` records what landed, what was deferred, and any new bugs caught during dogfooding.
- The agent-fit doc's "Gap 5" can credibly be marked closed. The doc's overall verdict moves from "yes, comfortably" to "fit for the many-threads agent workflow as designed."
