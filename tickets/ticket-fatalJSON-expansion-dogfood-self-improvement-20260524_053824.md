# TICKET: Wider `fatalJSON` coverage + diverse dogfood + self-improvement loop

**ID:** SW-AGENT-20
**Filed:** 2026-05-24 05:38 UTC
**Filed by:** claude (this session)
**Status:** in progress — claimed by claude (this session)
**Target:** sharedwatch v0.0.7
**Estimated:** ~half a day
**Branch:** `main`

---

## 1. Motivation

Two threads to pick up from the SW-AGENT-19 close:

1. **`fatalJSON` is only wired into `events list`.** The other four format-aware handlers (`events stats`, `sql`, `schema`, `overview`) still call plain `fatal()` even when the caller has `--format json` set. Agent reads stdout for JSON, gets nothing; stderr has the error in plain text. Inconsistent — fix it.

2. **The dogfood set has 19 real scenarios (plus the new #20).** Most cover *watcher* behaviour. The newer agent-facing surface (env vars, `config show`, `stop`, cursors, lease/intent coordination, recovery from crashes) is under-exercised. Diverse new scenarios surface real edge cases AND the findings feed back into the agent-facing docs — that's the "self-improvement loop" the user asked for: dogfood → finding → updated `agent-system-prompt` / Skill / CHANGELOG note.

---

## 2. Scope

### In scope

- **Extend `fatalJSON` to** `events stats`, `sql`, `schema`, `overview`. Each handler reads `isJSONFormat(*formatFlag)` after Parse and routes user-input errors through `fatalJSON` with appropriate error codes.
- **Four new dogfood scenarios** (21–24), each chosen to cover a different agent-realistic edge case:
  - **#21** — first-session canonical workflow with env-var defaults (init → emit → cursor read → roots → config show → stop). Confirms the v0.0.5 ergonomics promise end-to-end.
  - **#22** — stale lock file recovery (daemon SIGKILLed, lock left behind; what does `stop` do, what does the next `run` do).
  - **#23** — misbehaving-agent error handling (bad `--since`, bad SQL, unknown digest ID, all in JSON mode and text mode side-by-side).
  - **#24** — multi-agent lease warning (actor A acquires a lease; actor B writes inside its glob; watcher logs a structured warning).
- **Each scenario run end-to-end** against the v0.0.7 binary; actual outputs recorded in the worklog.
- **Findings feed back** into:
  - [`docs/agent/agent-system-prompt-20260522.md`](../docs/agent/agent-system-prompt-20260522.md) — add a new "PATTERN:" block for anything the dogfood reveals is non-obvious.
  - [`Skills/sharedwatch-client/SKILL.md`](../Skills/sharedwatch-client/SKILL.md) — add a "Known gotchas (from dogfood)" section if findings warrant.
- **CHANGELOG, man page, README** routine updates.
- **Release v0.0.7** via the in-repo flow.

### Out of scope

- A unit test for `fatalJSON` itself (still requires the subprocess/refactor dance; deferred from SW-AGENT-19).
- Shell completion. Still humans-first; ship when asked.
- New error codes beyond the SW-AGENT-19 vocabulary. Add only if a scenario surfaces a genuine category that doesn't fit.

---

## 3. Design — locked decisions

### 3.1 `fatalJSON` wiring pattern (consistent across handlers)

Every format-aware handler follows the same template:

```go
func handleX(ctx context.Context, a *app.App, args []string) {
    fs := flag.NewFlagSet(...)
    formatFlag := fs.String("format", flagDefault(...), ...)
    // ... bind other flags ...
    _ = fs.Parse(args)
    jsonErr := isJSONFormat(*formatFlag)

    // From here, every user-facing error path uses fatalJSON(jsonErr, code, msg).
    // DB / network / internal errors that should still surface to humans get
    // fatalJSON(jsonErr, errDBError, ...) etc.
}
```

Bias toward: every error that an agent could reasonably encounter through bad input gets `fatalJSON`. Don't refactor truly-internal errors (e.g., `os.Exit` during cfg load that happens *before* format is known) — those stay on `fatal()` because we can't yet know the output mode.

### 3.2 Dogfood scenario format

Same as existing 1–18 + 20: Goal / Setup / Actions+Expected / What this exercises / Pass criteria / Recorded run pointer to worklog.

### 3.3 Self-improvement feedback shape

For each scenario, after running:

- If output matches expected exactly: record `✓` in worklog, no doc changes needed.
- If output reveals a non-obvious behaviour: add a brief "PATTERN: <name>" block to the system prompt OR a gotcha line to the Skill (whichever audience needs it more).
- If output reveals a bug: file a follow-up ticket; don't shoehorn the fix into this release unless it's a one-liner.

---

## 4. Acceptance criteria

1. `sharedwatch events stats --root nope --format jsonl` emits a JSON error envelope on stdout (currently fatal() → text on stderr).
2. `sharedwatch sql "garbage" --format jsonl` likewise.
3. `sharedwatch schema unknown_table --format jsonl` likewise.
4. `sharedwatch overview --format json` (when something fails internally — synthetic test) likewise.
5. Four new scenarios (21–24) added to `docs/dogfood/test_dogfood.md` with the same format as existing scenarios.
6. Each new scenario is **run end-to-end against the v0.0.7 binary**; actual outputs (not just expected) recorded inline in the SW-AGENT-20 tasklist worklog.
7. Any non-obvious behaviour surfaced by the scenarios is captured in `docs/agent/agent-system-prompt-20260522.md` as a new PATTERN block OR in `Skills/sharedwatch-client/SKILL.md` as a gotcha.
8. All existing tests still green.
9. CHANGELOG `[Unreleased]` block + man page `.TH` updated.
10. Release v0.0.7 cut + pushed.

---

## 5. References

- [`tickets/ticket-cli-polish-quiet-dryrun-jsonerrors-20260524_051945.md`](./ticket-cli-polish-quiet-dryrun-jsonerrors-20260524_051945.md) — SW-AGENT-19; the prior release; established the `fatalJSON` pattern this ticket extends.
- [`docs/dogfood/test_dogfood.md`](../docs/dogfood/test_dogfood.md) — where the new scenarios land.
- [`docs/agent/agent-system-prompt-20260522.md`](../docs/agent/agent-system-prompt-20260522.md) — system prompt that gains PATTERN blocks from findings.
- [`Skills/sharedwatch-client/SKILL.md`](../Skills/sharedwatch-client/SKILL.md) — gets a gotchas section if findings warrant.
- [`src/cmd/sharedwatch/main.go`](../src/cmd/sharedwatch/main.go) — the four handlers to update.
- [`GITOPS.md`](../GITOPS.md) §10 — release flow for v0.0.7.

---

## 6. Definition of done

All 10 acceptance criteria green. `release/v0.0.7/` present; `release/LATEST` = `v0.0.7`; `git push origin refs/heads/main refs/heads/v0.0.7 refs/tags/v0.0.7` succeeded. The public installer one-liner pulls `v0.0.7`; the four `fatalJSON` extensions and the four new dogfood scenarios all behave as documented. The self-improvement loop has produced at least one concrete doc edit (or, with justification, none — if the scenarios all confirmed expected behaviour exactly).
