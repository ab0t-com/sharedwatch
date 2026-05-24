# TICKET: CLI polish pass — `--quiet`, `--dry-run`, JSON-structured errors

**ID:** SW-AGENT-19
**Filed:** 2026-05-24 05:19 UTC
**Filed by:** claude (this session)
**Status:** in progress — claimed by claude (this session)
**Target:** sharedwatch v0.0.6
**Estimated:** ~half a day
**Branch:** `main`

---

## 1. Motivation

The v0.0.5 doc-refresh pass surfaced three small CLI gaps relative to "agent-facing best practice":

1. **No `--quiet` flag.** Agents (and humans) piping output through scripts get the `Next:` hint block on stderr-adjacent text channels, which clutters captured logs and confuses parsers. There's `--hints off` but it's per-feature; a single `--quiet` is the standard convention (`gh --quiet`, `git --quiet`, etc.).
2. **No `--dry-run` on the two destructive read-side commands** (`events retry`, `events recover-stuck`). Agents wanting to verify the impact before applying have no way to preview.
3. **Errors are unstructured text on stderr even when `--format json` is set.** An agent reading the JSON envelope from stdout has to parse stderr text to know what failed. Common pattern in modern agent CLIs: when `--format json`, errors also emit a JSON envelope.

Shell completion + per-subcommand `--help` uniformity are deferred — they help humans more than agents, and the agent-facing wins above pay off faster.

---

## 2. Scope

### In scope

- **`--quiet` global flag.** Suppresses the `Next:` block on text output AND the informational "no running daemon" / "stopped cleanly" status lines on `stop`. Equivalent to `--hints off` for the `Next:` block but also covers the friendly info lines.
- **`--dry-run` on `events retry` and `events recover-stuck`.** Print which event IDs / counts would change; don't write.
- **JSON-structured errors when `--format json` is in effect on commands that take `--format`.** A `fatal()` from inside such a handler emits:
  ```json
  {"format_version": 1, "error": {"message": "<human-readable>", "code": "<machine-readable>"}}
  ```
  to **stdout** (so the JSON consumer's stream is uninterrupted) and exits non-zero.
- **One new dogfood scenario** in `docs/dogfood/test_dogfood.md` exercising all three additions end-to-end against a real binary.
- **CHANGELOG, src/README, man page** updates.
- **Release v0.0.6** via the in-repo flow.

### Out of scope

- Shell completion (`sharedwatch completion bash|zsh|fish`). Worth doing, but humans-first; will land separately if/when a user requests.
- Per-subcommand `--help` uniformity sweep. Cosmetic; not currently a complaint source.
- Refactoring `fatal()` to a global error mode — JSON-errors implemented narrowly via a per-handler json-mode flag passed to a new `fatalJSON()` helper, leaving the existing `fatal()` untouched for non-JSON paths.

---

## 3. Design — locked decisions

### 3.1 `--quiet`

Global flag, default off. When set:
- `Next:` block is suppressed (overrides `--hints` profile resolution by forcing `off`).
- `stop` doesn't print "sent SIGTERM" / "daemon stopped cleanly" / "no running daemon" lines.
- `init` doesn't print the three `key=value` lines.
- Everything else (data output) is unchanged.

Quiet does NOT suppress errors. They still emit on stderr (or as JSON envelope per §3.3).

### 3.2 `--dry-run`

Per-command flag on `events retry` and `events recover-stuck`. When set:
- Compute which rows would change (use the same SELECT the update normally uses).
- Print the IDs (or count) that would be affected.
- Do NOT issue the UPDATE.
- Exit 0.

### 3.3 JSON-structured errors

A new helper `fatalJSON(jsonMode bool, code, msg string, format ...any)`:
- If `jsonMode == false`: same as `fatal()` — prints to stderr, exits 1.
- If `jsonMode == true`: emits a JSON envelope to **stdout** with the structure above, exits 1.

Wired into the handlers that take `--format` (`events list`, `events stats`, `sql`, `schema`, `overview`). Other handlers continue to use plain `fatal()` — their errors aren't competing with a JSON-stream consumer.

Error codes (small fixed vocabulary, additive over time):
- `bad_flag` — usage error.
- `not_found` — resource doesn't exist (cursor, root, digest id, etc.).
- `permission` — file/socket permission.
- `db_error` — SQLite or migration failure.
- `network_error` — only used by `update`.
- `internal` — anything that doesn't fit the above; carries `message` only.

---

## 4. Acceptance criteria

1. `sharedwatch --quiet status` does not print a `Next:` block even when pending > 0.
2. `sharedwatch --quiet stop` prints nothing on success (exits 0) and prints nothing on "no daemon" (exits 0).
3. `sharedwatch events retry --dry-run` prints the IDs that would be requeued and does not modify the database.
4. `sharedwatch events recover-stuck --dry-run --older-than 5m` likewise.
5. `sharedwatch events list --format json --since not-a-time` emits a JSON envelope on stdout with `"error":{"code":"bad_flag",...}` and exits 1.
6. Plain `sharedwatch events list --since not-a-time` (no --format) emits a text error on stderr and exits 1 (unchanged behaviour).
7. New dogfood scenario covering the three additions added to `docs/dogfood/test_dogfood.md` and executed manually against the v0.0.6 binary; one scenario block recorded with expected vs actual output.
8. All existing tests still green: `go test ./... -count=1`.
9. CHANGELOG `[Unreleased]` block updated; `src/README.md` global-flags table updated; `man/sharedwatch.1` ENVIRONMENT / GLOBAL FLAGS / COMMANDS sections updated.
10. Release v0.0.6 cut + pushed via the in-repo flow.

---

## 5. Test plan

- **Unit:**
  - `cmd/sharedwatch/main_test.go` (new or extend) — table tests for the JSON error envelope shape.
  - Verify `--dry-run` paths don't call UPDATE (mock or count rows before/after in an integration-style test).
- **Dogfood:**
  - Run the new scenario in `docs/dogfood/test_dogfood.md` against the v0.0.6 binary on a real `XDG_DATA_HOME` and record actual output inline. Same format as the existing 18 scenarios.

---

## 6. References

- [`docs/design/defaults-audit-20260524.md`](../docs/design/defaults-audit-20260524.md) §5 Q-block — the inspiration for treating "this is best practice" as a category worth its own ticket pass.
- [`tickets/ticket-defaults-config-introspection-stop-20260524_044950.md`](./ticket-defaults-config-introspection-stop-20260524_044950.md) — SW-AGENT-18, the previous release; established the env-var + introspection patterns that this ticket extends with `--quiet`.
- [`docs/dogfood/test_dogfood.md`](../docs/dogfood/test_dogfood.md) — where the new scenario lands.
- [`src/cmd/sharedwatch/main.go`](../src/cmd/sharedwatch/main.go) — most edits land here.
- [`GITOPS.md`](../GITOPS.md) §10 — release flow for v0.0.6.

---

## 7. Definition of done

All 10 acceptance criteria green. `release/v0.0.6/` present, `release/LATEST` = `v0.0.6`, `git push origin refs/heads/main refs/heads/v0.0.6 refs/tags/v0.0.6` succeeded. The canonical `curl | bash` installs v0.0.6 cleanly; the three new behaviours work exactly as specified; the dogfood scenario passes against the deployed binary.
