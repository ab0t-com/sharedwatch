# TICKET: CLI audit polish — cleanup, consistency, and doc completeness

**ID:** SW-AGENT-32
**Filed:** 2026-05-25 11:44 UTC
**Filed by:** claude (this session)
**Status:** filed — not yet claimed
**Target:** sharedwatch v0.1.2 (polish window after SW-AGENT-30/v0.1.1 ships and SW-AGENT-31 `--on-mode-change` follow-up lands)
**Estimated:** ~half a day end-to-end (top-5 quick wins ≈ 90 min; remaining items batch to another half-day if bundled)
**Branch:** `feature/cli-audit-polish` (to be created off `feature/on-digest-hooks` once that merges, OR off `main`)

---

## 1. Motivation

The full-CLI usability audit at [`docs/reports/report_20260525_054051.md`](../docs/reports/report_20260525_054051.md) ran 6 parallel Explore agents at very-thorough depth, exercising ~250 commands across every documented surface. **Net result: production-quality for the v0.1.1 release, no audit-flagged blockers** — but 2 small high-severity items and ~18 medium-severity polish items worth bundling into a single cleanup ticket before the surface area grows further.

Three themes account for almost all the findings:

1. **Two trivial dead-code / duplicate-help issues** that any operator reading help will trip on once and remember as a paper-cut.
2. **Cross-cutting text-vs-JSON field-naming inconsistency** across `intent list` / `lease list` / `digest list`. The audit's §6 table is 15 rows of "same concept, different name across surfaces." It's been a long-running gotcha (already documented in agent-system-prompt as a known footgun); time to close it.
3. **Documentation completeness gap** for SW-AGENT-29/30 features. The env vars work, the YAML keys work, `config show` lists them all — but the README env table + `config-file.md` reference don't mention any of them. Operators reading the README first get an incomplete picture of what's configurable.

Plus a handful of small subcommand frictions (silent fallthroughs, dead struct field, etc.) and discoverability polish.

---

## 2. Scope

### In scope (this ticket)

**A. High-severity quick wins** (audit §2):
- A1. Delete `lease grant --format` dead-code flag (declared, never read; `_ = *asJSON` discards immediately).
- A2. Consolidate duplicate `events retry` + `events recover-stuck` entries in `usage(root)`'s COMMANDS block.

**B. Field-naming alignment** (audit §6):
- B1. Update text output of `intent list` to use the JSON canonical names: `intent_id` (currently hidden), `actor_id` (currently `actor=`), `path_glob` (currently `path=`), `expires_at` (currently `expires=`).
- B2. Same for `lease list`: surface `lease_id`, `actor_id`, `path_glob`, `renewal_count` (currently `renewals=`), `expires_at`.
- B3. Same for `digest list`: change text `events=N` to `event_count=N` for parity with JSON.
- B4. Decide + document the `actor` (event payload) vs `actor_id` (db record) split — they're intentional (transport vs storage) per the audit reconciliation; just needs a one-line note in `docs/guides/event-broker.md` §5.

**C. Documentation completeness** (audit §4b):
- C1. Add 4 SW-AGENT-29/30 env vars to `src/README.md` env-var table: `SHAREDWATCH_ON_DIGEST`, `SHAREDWATCH_ON_DIGEST_TIMEOUT`, `SHAREDWATCH_EMIT_PROFILE`, `SHAREDWATCH_EMIT_OVERRIDE`.
- C2. Update `Skills/sharedwatch-client/references/config-file.md` with **all** keys that parse today: SW-AGENT-29's `on_digest` / `on_digest_timeout`; SW-AGENT-30's `emit_profile` / `emit_overrides` / `emit_thresholds`; plus the 10+ SW-AGENT-18 agent-default keys (`actor`, `actor_kind`, `session`, `task`, `addressee`, `default_format`, `default_root`, `default_since`, `hints`, `cursor_name`) which exist but aren't in the reference.
- C3. Fix `docs/guides/on-digest-hooks.md` §2 example: `sharedwatch run --on-digest '…'` → `sharedwatch --on-digest '…' run` (Go stdlib silently drops flags after first non-flag positional).
- C4. Add a `--path-glob` case-sensitivity note to `events list --help` description AND to `docs/guides/event-broker.md` §6 patterns. Uses `filepath.Match` which is case-sensitive on Unix.
- C5. Document the default `emit_thresholds` values in `docs/guides/event-broker.md` §2 (currently only visible via `config show --json`).

**D. Subcommand-specific friction** (audit §4c):
- D1. `--root <label>` filter on read commands accepts a label that doesn't exist in any configured root and silently returns 0 events. Fix: validate against the configured-roots set; on mismatch, error with "available labels: [a, b, c]".
- D2. `--config /nonexistent/path.yaml` silently falls through to the next config search candidate. Fix: when `--config` is **explicit** (user-supplied non-empty path), error loudly if it doesn't exist. There's an existing TODO comment matching this intent.
- D3. Unknown class name in `emit_overrides` (e.g. typo `digests=true` instead of `digest=true`) is silently ignored. Forward-compat-intent is correct; add a stderr warning at config-resolution time naming the unknown class so operators notice their typo. Don't fatal.
- D4. Delete the `Config.StorageType` field. It defaults to `"sqlite"`, but no YAML key parses it, no env var sets it, no flag binds to it, and nothing reads it for branching logic. Dead struct field.
- D5. `lease.violated` event payload has `"violator_actor": ""` when the source event lacks an `actor` in its `payload_json`. Either omit the field via `omitempty` (signals "no attribution available") OR fall back to `producer_id`. Pick one + document.

**E. Discoverability tweaks** (audit §4d):
- E1. Add a one-liner to global help: "for per-subcommand flag details, run `sharedwatch help` or see `docs/`". The SW-AGENT-26 design — `<subcommand> --help` returns global usage to avoid creating a DB on a help probe — is correct but operators don't know why they got the global help block.
- E2. Document the flag-reordering magic (`sharedwatch status --json` ≡ `sharedwatch --json status`) in the global help footer.
- E3. The `SHAREDWATCH_ROOT` env var name looks like it should DEFINE roots but actually defaults the `--root` FILTER. Add a clarifying paragraph in the README env table; do NOT rename (breaking change — defer indefinitely).

### Out of scope (defer to follow-up tickets)

- **Per-subcommand `--help` with flag details** — design tension with SW-AGENT-26 (don't open DB on help probe). Solving it properly means moving help generation to a no-DB layer, which is a bigger refactor.
- **Rising-edge gating** for `events.failed_threshold` / `events.stuck_detected` — already deferred per SW-AGENT-30 design (needs persisted prior-count state in `runtime`).
- **CSV `payload_json` encoding readability** (audit L7) — valid RFC 4180, just hard for humans; minor polish.
- **JSONL trailing-newline jq quirk** (audit L8) — only affects `jq -c .` chains that don't `select(.id)`. Workaround documented; not worth a fix.
- **`roots.mode` vs `status.mode` field-name collision** (audit L6) — would need a field rename which is a breaking change for any consumer reading either output. Defer until next major output-contract review.
- **`--on-mode-change` / `--on-startup` / `--on-shutdown` hooks** — separate ticket (SW-AGENT-31 per the SW-AGENT-30 roadmap).

---

## 3. Implementation plan

Phased so each batch is independently shippable. Each phase = one commit.

### Phase 1 — Quick wins (A1 + A2)

Two trivial main.go edits.

- Delete the `asJSON := fs.Bool("format", false, "deprecated; emits JSON")` declaration and the `_ = *asJSON` line in the `lease grant` handler.
- In `usage(root)`'s COMMANDS print block, find the duplicate `events retry` and `events recover-stuck` lines (~lines 1714 + 1729). Consolidate to one line each, e.g. `events retry [--max-retries N] [--dry-run]` and `events recover-stuck [--older-than 5m] [--dry-run]`.
- **Test**: `lease grant --help` no longer mentions `--format`; `sharedwatch help` shows each retry/recover-stuck command once.

### Phase 2 — Field-naming alignment (B1-B4)

Update text-mode formatters in main.go to emit the JSON canonical names.

- B1 — `intent list` text loop (find via grep for `actor=%s  path=%s`); change to `intent_id=%s  actor_id=%s  path_glob=%s  expires_at=%s`. Decide on separator (current code uses spaces; consider tab-aligning via `tabwriter` like `config show` does for legibility).
- B2 — `lease list` text loop; same treatment.
- B3 — `digest list` text loop; rename `events=N` to `event_count=N`.
- B4 — One-paragraph note in `docs/guides/event-broker.md` §5 "Consumer contracts" explaining the `actor` (transport / events payload) vs `actor_id` (storage / db record) intentional split.
- **Test**: existing test suite. Verify human readability by running each `list` text command on a populated DB. Update `docs/dogfood/test_dogfood.md` examples to use the new field names if they appear.
- **Compat note**: this IS a breaking change for any script parsing the text output with `awk '{split($2,a,"=");...}'`. Mitigation: text was never a stable contract (see project docs); operators wanting machine parse should already use `--json`. Worth a CHANGELOG callout.

### Phase 3 — Documentation completeness (C1-C5)

Pure docs work. Five files touched.

- C1 — `src/README.md` env-var table: add 4 rows.
- C2 — `Skills/sharedwatch-client/references/config-file.md`: rewrite the keys section to include all 17 keys that actually parse today. Group by ticket origin (SW-AGENT-18 agent defaults / SW-AGENT-29 hooks / SW-AGENT-30 emit).
- C3 — `docs/guides/on-digest-hooks.md` §2: swap example.
- C4 — `docs/guides/event-broker.md` §6 + `events list --help` description: one-line case-sensitivity note.
- C5 — `docs/guides/event-broker.md` §2: list the 3 default `emit_thresholds` values inline.
- **Test**: doc-only, no automated test; visually review each rendered page.

### Phase 4 — Subcommand friction (D1-D5)

Mix of one-line guard fixes + one struct deletion.

- D1 — In `events list` handler, after parsing `--root` filter values, check each against the configured roots' Label set. Surface error with `"available: [a, b, c]"`.
- D2 — In `internal/config/file.go` `SearchConfig`, when `explicit != ""` AND the file doesn't exist, return an error in `SearchResult` (or change the calling path in main.go to error out). Existing code comment ("caller surface its absence as an error") indicates this was always the design.
- D3 — In main.go's resolution layer (after env+config+flag merge), iterate `cfg.EmitOverrides` keys; for each that's not in `events.AllClasses()`, log a warning via `slog.Warn` (or stderr `fmt.Fprintf`) naming the unknown class.
- D4 — Remove `StorageType` field from `config.Config` struct. Remove the `"sqlite"` default initialization. Run tests + ensure nothing references it. (Likely zero ref since the audit found none.)
- D5 — In `watcher.Service.emitLeaseViolations` + `reconcile.Service.emitLeaseViolations`, change the payload to omit `violator_actor` when empty (`omitempty` on a `string` field; or build the map conditionally before marshal). Update the broker guide §3 to document the new shape.
- **Test**: unit test for D1 (filter with unknown label errors); empirical for D2 (`--config /tmp/nonexistent` should error); unit test for D3 (resolution with unknown override class logs warning); test suite for D4 (no regressions); empirical for D5 (lease.violated payload no longer carries empty `violator_actor`).

### Phase 5 — Discoverability tweaks (E1-E3)

Doc-only.

- E1, E2 — Edit `usage(root)` to add a small footer with the per-subcommand-help guidance + flag-reordering note.
- E3 — Edit README env table entry for `SHAREDWATCH_ROOT` with clarifying paragraph.
- **Test**: visual review of `sharedwatch help`.

### Phase 6 — CHANGELOG + dogfood

- Add `[Unreleased]` entry covering the polish (highlight B-phase as the breaking change for text-parsing scripts).
- Quick dogfood pass against the rebuilt binary covering: the 5 top items from the audit report §1, plus the breaking-change text format change (run each list command and visually inspect).

---

## 4. Acceptance criteria

- [ ] `sharedwatch lease grant --help` does NOT list `--format`.
- [ ] `sharedwatch help` lists each `events retry` and `events recover-stuck` command exactly once with consolidated flags.
- [ ] `sharedwatch intent list` text output includes `intent_id=…`, `actor_id=…`, `path_glob=…`, `expires_at=…`.
- [ ] `sharedwatch lease list` text output includes `lease_id=…`, `actor_id=…`, `path_glob=…`, `renewal_count=…`, `expires_at=…`.
- [ ] `sharedwatch digest list` text output uses `event_count=` instead of `events=`.
- [ ] `src/README.md` env-var table includes all 4 SW-AGENT-29/30 vars.
- [ ] `Skills/sharedwatch-client/references/config-file.md` documents all 17 currently-parsing YAML keys.
- [ ] `docs/guides/on-digest-hooks.md` §2 example uses correct flag position.
- [ ] `docs/guides/event-broker.md` §2 lists default `emit_thresholds` values inline.
- [ ] `docs/guides/event-broker.md` §6 (or `events list --help`) notes path-glob case sensitivity.
- [ ] `sharedwatch events list --root nonexistent-label` errors with available-labels suggestion (not silent zero-result).
- [ ] `sharedwatch --config /tmp/totally-missing.yaml events list` errors loudly (not silent fallthrough).
- [ ] `sharedwatch --emit-override digests=true events list` logs a warning to stderr naming the unknown class `digests` (not silent).
- [ ] `Config.StorageType` field no longer exists in source; `grep -r StorageType src/` returns empty.
- [ ] `lease.violated` event payload either omits `violator_actor` when empty OR falls back to `producer_id`; behaviour documented in `docs/guides/event-broker.md` §3.
- [ ] `sharedwatch help` footer mentions the per-subcommand-help convention + flag-reordering behaviour.
- [ ] `sharedwatch config show` includes a clarifying note (or has README updated) explaining `SHAREDWATCH_ROOT` is a FILTER default, not a definition.
- [ ] `go test ./...` passes; `gofmt -l .` + `go vet ./...` clean.
- [ ] CHANGELOG `[Unreleased]` entry covers the work (highlighting the text-format breaking change).

---

## 5. Risks + mitigations

| Risk | Mitigation |
|---|---|
| **B-phase breaks scripts parsing text output** (e.g. `awk '/actor=/{print}'`). | Text output was never a documented stable contract (per project docs). Mitigation: CHANGELOG callout + a one-paragraph migration note ("use `--json` for stable parsing"). |
| **D1 too-strict root validation** breaks workflows where the user filters by a label that exists in events but not in current config. (E.g. migrating data from another sharedwatch install.) | Soft-warn first, OR check against actual `watch_root` values present in the events table rather than only configured roots. Lean: configured-roots check + escape hatch (`--root '*'` or similar) if a use case surfaces. |
| **D2 makes `--config` fail in CI** if users were relying on the silent fallthrough behaviour (e.g. `--config $CI_CONFIG` where the var is unset). | Empty string already short-circuits; the error fires only on explicit non-empty paths. Worth a CHANGELOG note. |
| **D4 deletes a struct field** that some downstream test or doc references. | Run full test suite + `grep -r StorageType` before merge. Search docs too. |
| **D5 omitempty changes the lease.violated payload shape** — could surprise existing subscribers. | Land it pre-v1.0; document in CHANGELOG. Empty string was effectively useless info anyway. |

---

## 6. Open questions (resolve at claim time)

- Q1: B-phase text reformat — keep simple `key=value key=value` flat list, OR convert to `tabwriter`-aligned columns? (My lean: keep flat; aligned columns are nicer for `config show`-style sparse data but here the rows are dense and a flat string is easier to grep.)
- Q2: D3 unknown-class warning — log on every config-resolve (chatty if config is reloaded often), OR only on daemon startup? (Lean: every resolve. Config reload is rare; warning is noisy enough to motivate fixing the typo, not noisy enough to annoy.)
- Q3: D5 lease.violated empty-actor fallback — `omitempty` (cleaner JSON) vs `producer_id` fallback (more info)? (Lean: `omitempty`. The `producer_id` is rarely the actor; could mislead more than it informs.)

---

## 7. References

- Audit report: [`docs/reports/report_20260525_054051.md`](../docs/reports/report_20260525_054051.md) — full source for every finding referenced here. Each item below cross-refs the audit section.
- SW-AGENT-30 design + tasklist: [`docs/design/event-and-hook-surface-expansion-20260524.md`](../docs/design/event-and-hook-surface-expansion-20260524.md) + [`tickets/tasklist_20260525_015121.md`](./tasklist_20260525_015121.md). The §8 roadmap of that doc places SW-AGENT-31 (--on-mode-change hooks) ahead of this; if Q1 is "ship audit polish first," SW-AGENT-31 and this ticket can swap order without rework.
- Broker guide (consumer-facing): [`docs/guides/event-broker.md`](../docs/guides/event-broker.md) — several edits land here in C-phase + D5.
- Hooks guide: [`docs/guides/on-digest-hooks.md`](../docs/guides/on-digest-hooks.md) — C3 edits here.
- Skill reference: [`Skills/sharedwatch-client/references/config-file.md`](../Skills/sharedwatch-client/references/config-file.md) — C2 rewrites this.
- Related polish tickets for pattern reference: [`tickets/ticket-cli-polish-quiet-dryrun-jsonerrors-20260524_051945.md`](./ticket-cli-polish-quiet-dryrun-jsonerrors-20260524_051945.md) (SW-AGENT-19 — same kind of consistency cleanup).
