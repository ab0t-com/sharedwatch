# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added — dogfood scenarios 33/34/35 (v0.0.8 dogfood, intent/coalesce/filter axis)
- 33: full intent lifecycle (declare → list → revoke; text/JSON; --all).
- 34: cross-actor coalesce regression check (SW-AGENT-11).
- 35: events list combined-filter matrix (--type × --path-glob × --status × payload × --fields).
- Run end-to-end against deployed v0.0.8. Net: 2 small UX bugs **fixed in-round**, 3 gotchas **documented**, 3 deeper-design questions **filed as `docs/design/intent-events-discussion-20260524.md`** for triage.

### Fixed — `intent declare --help` and `intent revoke --help`
- `intent declare --help` now shows the `<path-glob>` positional in its usage line (custom `fs.Usage`). Previously listed only flag descriptions, hiding that `<path-glob>` is required.
- `intent revoke --help` no longer treats `--help` as a positional id ("intent not found: --help") — now prints the usage string and exits 0. Behaviour matches every other subcommand's `--help`.

### Documented — three intent/coalesce gotchas (scenarios 33, 34)
- `--all` on coordination-list commands (both `lease list` and `intent list`) is a **TTL-expiry filter, not a soft-delete filter**. Released leases and revoked intents are hard-deleted; `--all` only surfaces TTL-expired entries. Generalises the gotcha from the previous round to cover both subcommands.
- `intent list --json` returns a **bare array** (not `{"intents":[...]}`) with **different column names than text form** (`actor_id`/`path_glob` vs `actor=`/`path=`); empty result is bare `null`.
- `test emit` against an `(actor, path)` already emitted within the 5s coalesce window returns a **fresh `evt_id`** but coalesces into the prior event — agents recording the returned id will hit a phantom (not in DB).
- All three documented in `docs/agent/agent-system-prompt-20260522.md` and `Skills/sharedwatch-client/SKILL.md` startup ritual.

### Filed for design — `docs/design/intent-events-discussion-20260524.md`
- Three open questions surfaced by dogfood that warrant a design call, not impulse fixes:
  - Q1: should `intent declare`/`revoke` emit events (parity with `lease.granted`/`lease.released`)?
  - Q2: should `test emit` print `coalesced into <prior_id>` instead of a phantom `emitted <new_id>` when the coalesce path triggers?
  - Q3: should `intent list --json` switch to JSONL (matching `events list`/`lease list`) for output-envelope consistency?
- Recommended: bundle as **SW-AGENT-23 — intent surface parity** for v0.0.9.

### Added — dogfood scenarios 30/31/32 (v0.0.8 dogfood)
- 30: mode active TTL lifecycle (active→passive transitions, TTL expiry).
- 31: full lease lifecycle (grant → list → renew → release; --all filter).
- 32: config search precedence end-to-end (--config > project-local > XDG).
- Run end-to-end against deployed v0.0.8. Net: 0 code bugs, 1 documented behaviour (below), 1 help-text clarification (below).

### Documented — `lease list --all` shows expired but NOT released leases (dogfood scenario 31)
- Surfaced by scenario 31: `sharedwatch lease release <id>` deletes the lease outright; subsequent `lease list --all` returns "no leases" because `--all` only surfaces leases retained in the table (i.e., those that lived out their TTL without being released).
- The flag's help text said "include expired leases" — technically accurate but easy to misread as "everything I ever did".
- Help text clarified to: `include expired leases (note: released leases are deleted, not retained — \`--all\` only surfaces leases that lived out their TTL)`.
- Documented as a known gotcha in `agent-system-prompt-20260522.md` and `Skills/sharedwatch-client/SKILL.md`. For a full audit trail of grant/release, query the `events` journal directly.

### Added — dogfood scenarios 27/28/29 (v0.0.8 dogfood)
- Scenarios 27 (update lifecycle), 28 (cursor management surface), 29 (empty-journal grace) added to `docs/dogfood/test_dogfood.md`. Each run end-to-end against the deployed v0.0.8 binary.
- **Net findings:** 1 code bug (none), 1 documented behaviour (see below), 1 scenario-text fix (cursor encode syntax — my own scenario was wrong, fixed). v0.0.8 is solid across these three axes (update lifecycle + cursor management + empty-state grace).

### Documented — `sharedwatch update` dry-run doesn't validate target existence (dogfood scenario 27)
- Surfaced by scenario 27: `sharedwatch update --version v9.9.9` (a nonexistent tag) prints the same DRY RUN plan as a real target version. The 404 only fires when the user passes `--apply`. So a clean dry-run is NOT proof the target version is real.
- This is by design (dry-run prints the resolved URL but doesn't perform any I/O), but non-obvious enough that an agent could be misled by a clean dry-run into running `--apply` on a phantom version.
- Documented as a known gotcha in `docs/agent/agent-system-prompt-20260522.md` and `Skills/sharedwatch-client/SKILL.md` (gotcha block).
- Optional follow-up (not filed): add HEAD-validation of the target manifest during dry-run, or include a `(target not verified — only confirmed on --apply)` line in dry-run output.

### Added — portable self-improvement-loop agent prompt + dogfood scenarios 25-26
- New file `prompts/self-improvement-loop-agent-prompt.md` — portable, project-agnostic version of the dogfood-driven self-improvement loop, paste-able into other CLIs / daemons / libraries. Wraps the workflow that produced 5 bug fixes + 1 doc-drift across SW-AGENT-20 and SW-AGENT-21. (183 lines.)
- New dogfood scenarios 25 (full precedence chain config→env→flag) and 26 (multi-root × --data-dir umbrella interaction) added to `docs/dogfood/test_dogfood.md`. Run against the deployed v0.0.8 binary; 26 passes; 25 surfaced one **non-obvious behaviour** (see below).

### Documented — `config show` reports cfg-layer values, not flag layer (dogfood scenario 25)
- Surfaced by scenario 25 against v0.0.8: `sharedwatch --hints agent config show` with `hints: terse` in `config.yaml` prints `hints: terse`, not `agent`. The flag wins for behaviour at runtime but `config show` displays the cfg-layer (config + env merge) value only.
- This is by design (config show is showing cfg, not the per-invocation effective value), but it's non-obvious enough that an agent will be surprised. Documented as a known gotcha in `docs/agent/agent-system-prompt-20260522.md` and `Skills/sharedwatch-client/SKILL.md`.
- No code change this round. A follow-up could add an `--effective` flag to `config show` that displays flag-layered values — file when a user asks for it.

### Changed — `--data-dir <X>` now re-derives `--watch-path` + `--db` defaults (SW-AGENT-21)
- The umbrella semantics that match the existing on-disk shape (one tree under `data_dir/` with `watch/`, `queue.db`, `sharedwatch.lock`, ...). Previously, `--data-dir` set only `cfg.DataDir` and left `watch_path` + `db_path` at their `$XDG_DATA_HOME`-derived defaults, silently splitting the install. Now: `--data-dir <X>` also sets `WatchPath = X/watch` and `DBPath = X/queue.db` *when those weren't explicitly set* via flag, env, or config. Explicit flag / config still wins.
- Matches conventions of other umbrella-shaped CLIs: `docker --root-dir`, older `helm --home`, `git --git-dir`, `homebrew prefix`, `pyenv root`.
- Surfaced by [SW-AGENT-20](../tickets/ticket-fatalJSON-expansion-dogfood-self-improvement-20260524_053824.md) dogfood scenario 21; "Known gotchas" bullet about needing to pass three flags together (or use `XDG_DATA_HOME`) is removed from `docs/agent/agent-system-prompt-20260522.md` and `Skills/sharedwatch-client/SKILL.md` accordingly.

### Added — `fatalJSON` extended to all format-aware handlers (SW-AGENT-20)
- `events stats`, `sql`, `schema`, `overview` now emit JSON-structured errors on stdout when `--format json|jsonl` is set, matching `events list`'s behaviour from v0.0.6. Consumers reading the JSON envelope no longer have to multiplex stderr to get the error.
- Same canonical error-code vocabulary (`bad_flag` / `not_found` / `permission` / `db_error` / `network_error` / `internal`).

### Fixed — `SHAREDWATCH_*` env vars now reach `test emit`'s payload (SW-AGENT-20 dogfood)
- Surfaced by scenario 21: `SHAREDWATCH_ACTOR=x sharedwatch test emit foo.md` was producing events with no actor (the env-derived attribution flowed to the watcher path but not the synthetic-emit path). The merge result was being discarded by handleTest's `rootAttr.merge(subAttr)` call.
- Fix: replace `rootAttr` in-place with the merged result so every downstream consumer sees env + config defaults.

### Fixed — `sharedwatch stop` now detects all "process already gone" errors (SW-AGENT-20 dogfood)
- Surfaced by scenario 22: ESRCH-only check missed Go's wrapped `os.ErrProcessDone`, so a stale-lock SIGTERM printed an ugly error instead of the friendly "daemon already gone" message.
- Fix: also match `os.ErrProcessDone` in the early-return path.

### Fixed — doc drift: `lease grant` (not `lease acquire`) (SW-AGENT-20 dogfood)
- The binary's lease vocabulary is `grant | release | renew | list`. Recent README / help banner / man page erroneously said `lease acquire`. Surfaced by scenario 24.
- Fix: replaced `lease acquire` with `lease grant` in `src/cmd/sharedwatch/main.go` usage(), top-level `README.md`, and `man/sharedwatch.1`.

### Added — dogfood scenarios 21–24 + "Known gotchas" feedback loop
- Scenarios 21–24 in [`docs/dogfood/test_dogfood.md`](../docs/dogfood/test_dogfood.md) cover canonical-with-env-vars, stale-lock-recovery, error-parity-text-vs-JSON, multi-agent lease warning. Each was executed end-to-end against the v0.0.7 binary; actual outputs recorded in [`tickets/tasklist_20260524_053824.md`](../tickets/tasklist_20260524_053824.md) worklog.
- Findings fed back into [`docs/agent/agent-system-prompt-20260522.md`](../docs/agent/agent-system-prompt-20260522.md) as a new "Known gotchas (surfaced by dogfood)" block, and into [`Skills/sharedwatch-client/SKILL.md`](../Skills/sharedwatch-client/SKILL.md) as a comment block in the agent-startup ritual.
- The user explicitly asked for this self-improvement loop: dogfood → finding → updated agent docs so the next agent doesn't repeat the same mistake.

### Open follow-up
- `--data-dir <X>` alone does not re-derive `watch_path` or `db_path` — they stay at XDG defaults, producing a split installation. Surfaced by scenario 21. Workaround documented in agent-system-prompt gotchas (use `XDG_DATA_HOME` instead). Fix deferred to a follow-up ticket — needs decision on whether `--data-dir` should be a "smart umbrella override" (re-derives) or stay narrow (current).

### Added — `--quiet` global flag (SW-AGENT-19)
- Suppresses the `Next:` hint block on every command (overrides any `--hints` profile resolution) AND the friendly informational lines on `init` ("watch_path=…"), `stop` ("sent SIGTERM…" / "daemon stopped cleanly" / "no running daemon"). Data output and errors are unaffected.
- Standard convention (matches `git --quiet`, `gh --quiet`). Useful when piping captured output into log parsers that get confused by the human-friendly noise.

### Added — `--dry-run` on `events retry` and `events recover-stuck`
- Both destructive read-side commands now accept `--dry-run`. Prints the event IDs that *would* be modified; does not issue the UPDATE.
- Mirrors the live UPDATE's WHERE clause via a SELECT so the preview is byte-for-byte accurate.

### Added — JSON-structured errors when `--format json|jsonl` is in effect
- New `fatalJSON(jsonMode, code, msg)` helper. When called with `jsonMode=true`, emits a JSON envelope on **stdout** (not stderr — so the consumer's stream stays uninterrupted) with shape: `{"format_version": 1, "error": {"code": "<code>", "message": "<msg>"}}` and exits 1.
- Wired into `events list` for the high-value bad-input paths: `--since` parse failure, `--until` parse failure, `--root foo=/path` rejection, `--since` / `--since-cursor` mutex violation.
- Small fixed error-code vocabulary: `bad_flag` / `not_found` / `permission` / `db_error` / `network_error` / `internal`. Consumers may key behaviour off these.
- Text errors on commands without `--format json` are unchanged (still on stderr).

### Added — env-var resolution layer for agent identity + output defaults (SW-AGENT-18 §3)
- New resolution chain: `flag > env > config.yaml > built-in default`. Env vars layer between config and flag.
- Canonical `SHAREDWATCH_*` env vars: `SHAREDWATCH_ACTOR`, `_ACTOR_KIND`, `_SESSION`, `_TASK`, `_ADDRESSEE` (attribution fields); `SHAREDWATCH_FORMAT`, `_ROOT`, `_CURSOR_NAME` (per-command defaults); `SHAREDWATCH_HINTS` (existing).
- An AI agent can now `export SHAREDWATCH_ACTOR=claude-coord-1 SHAREDWATCH_FORMAT=jsonl SHAREDWATCH_HINTS=agent` once at session start and stop typing those flags on every command.

### Added — XDG-aware config file search (SW-AGENT-18 §2)
- `Load()` now searches: `--config <path>` (when set), then `./config.yaml`, then `$XDG_CONFIG_HOME/sharedwatch/config.yaml` (or `~/.config/sharedwatch/config.yaml`). First existing wins. All optional.
- Pre-existing project-local `./config.yaml` behaviour preserved as the higher-precedence layer above XDG, so no regression for current users.

### Added — `sharedwatch config show` introspection
- New subcommand: prints the effective resolved Config, every `SHAREDWATCH_*` env var detected this invocation, the config files searched (and which one loaded), and the resolution-order legend.
- Text mode (default) is human-friendly with column alignment; `--json` emits a `format_version: 1` envelope for agent consumption.
- Closes the "is my SHAREDWATCH_X being read?" debug loop.

### Added — `sharedwatch stop` subcommand
- Reads PID from `<data_dir>/sharedwatch.lock`, sends SIGTERM, polls for lock-file removal with `--timeout` (default 10s).
- `--force` escalates to SIGKILL after timeout.
- Exits 0 on clean stop (or no lock file); 1 if daemon won't die in time without `--force`.
- Wired through the hints engine: a successful stop emits a `restart_daemon` hint suggesting `sharedwatch run`.

### Changed — config parser now reads 10+ keys that were silently ignored
- The following YAML keys were defined on `Config` but never parsed by `Load()`: `include_patterns`, `hash_enabled`, `hash_max_size`, `producer_id`. They now round-trip correctly.
- New keys for agent defaults: `actor`, `actor_kind`, `session`, `task`, `addressee`, `default_format`, `default_root`, `default_since`, `hints`, `cursor_name`. All optional.

### Added — design documents: hooks discussion + defaults audit
- [`docs/design/hooks-discussion-20260524.md`](../docs/design/hooks-discussion-20260524.md): does sharedwatch need a hooks system? Verdict — build `--on-digest <cmd>` only, when a real user asks. Don't build speculatively.
- [`docs/design/defaults-audit-20260524.md`](../docs/design/defaults-audit-20260524.md): audit of every CLI flag against "does the agent have to repeat this every time?". Drove this release.

### Changed — `events list --since` defaults to 24h when no cursor is set
- Bare `sharedwatch events list` previously returned the entire journal from the dawn of time. Now defaults `--since` to `24h` when neither `--since`, `--since-cursor`, nor `--cursor-name` is set. Matches `overview --since 24h` and `events stats --since 24h`.
- Override: pass `--since <RFC3339>` or a duration like `--since 1h` for any window; pass `--since 0` for the legacy "no lower bound" behaviour (returns everything).
- Driven by [`docs/design/defaults-audit-20260524.md`](../docs/design/defaults-audit-20260524.md) §3 Gap A. Phase-1 of the wider defaults work.

### Changed — default `ignore_patterns` extended with universally-noisy dev dirs
- Previously: `.git, .DS_Store, *.tmp, *.swp`.
- Now also includes: `node_modules`, `__pycache__`, `.cache`, `.venv`, `venv`, `target`, `dist`, `build`, `*.log`.
- Path-segment matched, so `node_modules/foo/bar.js` is excluded by the `node_modules` entry. Users who want any of these tracked override via `ignore_patterns:` in `config.yaml`.
- Driven by [`docs/design/defaults-audit-20260524.md`](../docs/design/defaults-audit-20260524.md) §3 Gap B. Phase-1 of the wider defaults work.

### Added — design discussions: hooks system + defaults audit
- [`docs/design/hooks-discussion-20260524.md`](../docs/design/hooks-discussion-20260524.md) — full discussion of whether sharedwatch needs a hooks system, what shape would fit the calm/pull architecture, and the recommendation (only `--on-digest <command>`, when a real user asks for it). 182 lines.
- [`docs/design/defaults-audit-20260524.md`](../docs/design/defaults-audit-20260524.md) — audit of every CLI flag against the "does the agent have to repeat this every time?" question. Lists Phase-1 (the two fixes above), Phase-2 (agent identity defaults via env vars), Phase-3 (cursor-name auto-default). 176 lines.

### Added — smart hints / next-step suggestions (SW-AGENT-17)
- New `internal/hints` package: a small, profile-driven, modular engine for emitting "what to run next" suggestions alongside CLI output. Zero imports from other sharedwatch packages — designed to be lifted into a standalone module later (`git mv internal/hints/ <newmod>/hints/` is the extract recipe).
- Four built-in profiles: `default` (≤4 hints, human-facing, with reasons), `agent` (≤8 hints, designed for AI consumers reading JSON), `terse` (1 hint, no reason), `off` (none).
- Resolution order: `--hints <profile>` flag > `SHAREDWATCH_HINTS` env var > auto-promote to `agent` when `--format json` is set > `default`.
- Per-command providers wired for: `status`, `roots`, `digest list`, `digest show`, `events stats`, `events list` (cursor mode), `init`, `version`, `overview`. Hints are state-derived (only shown when applicable: no "consume" hint when pending=0).
- **JSON envelope addition.** `status`, `roots`, `overview`, `events stats` envelopes gain an `omitempty` `next: [{name, command, reason}]` array. `format_version` stays 1 (additive).
- **Text addition.** A `Next:` block, two-space-indented and column-aligned via `text/tabwriter`, prints after a command's main output when hints apply.

### Changed — `overview` and `events stats` JSON: `drill` map removed in favour of unified `next` array
- The literal `drill: { "key": "command" }` map that v0.0.3 emitted on `sharedwatch overview --format json` and `sharedwatch events stats --root X --format json` is gone. Replaced with the unified `next: [{name, command, reason}]` array driven by the new hints engine.
- Same conceptual purpose (pre-computed follow-up commands) but uniform shape across every command that emits hints. **Breaking shape** — flagged here. Migrate consumers by reading `.next[].command` instead of `.drill[<key>]`.

### Added — `roots` subcommand
- `sharedwatch roots` prints the list of watched folders as a labelled table (LABEL, PATH, PENDING, LAST EVENT). Works for both single-root (synthesises a `(default)` row from `Cfg.WatchPath`) and multi-root setups. `--json` emits a `format_version: 1` envelope with a `mode` field (`single`|`multi`) and a `roots[]` array.
- Closes the recurring UX question of "what folders is sharedwatch listening to?" — previously discoverable only via `status --json | jq '.roots'`, which returned empty in single-root mode.
- Lives in `cmd/sharedwatch/roots.go`. Uses `text/tabwriter` for column alignment, no new module deps.

### Changed — help text + man page
- Expanded the embedded `sharedwatch help`: added the `roots` row, an explicit "DEFAULT PATHS" section explaining `$XDG_DATA_HOME/sharedwatch/{watch,queue.db}` defaults, and an EXAMPLES section with 9 concrete invocations (first-time setup, multi-root, attribution, cursors, schema discovery, SQL, smoke test, update).
- New `man/sharedwatch.1` man page (nroff). Install with `cp man/sharedwatch.1 /usr/local/share/man/man1/ && sudo mandb`; view with `man sharedwatch`. Covers every command, flag, and attribution field with examples.

### Changed — `update` now reads release/LATEST from raw.githubusercontent.com
- `sharedwatch update` no longer hits the GitHub Releases API (which we don't use); fetches `https://raw.githubusercontent.com/<repo>/main/release/LATEST` to resolve the latest version, then downloads from `release/<tag>/`. Matches the install.sh flow.
- The old `fetchLatestTag` helper (GitHub API + `tag_name` parsing) is gone; replaced with `fetchLatestFromRepo`.

### Added — `update` subcommand (safe self-updater)
- `sharedwatch update` prints the current version, the latest release tag fetched from GitHub, the target download URL and binary path, then exits. No changes by default.
- `sharedwatch update --apply` downloads the platform tarball + `manifest.yaml` from the release, verifies the tarball's SHA-256 against the manifest entry, extracts the binary, smoke-tests it (`<new> version` must report a sharedwatch banner), then atomic-renames it over the running binary. Cowardly refuses to install if the binary's parent directory isn't writable by the current user (no `sudo` escalation).
- `--version vX.Y.Z` pins a target tag; `--repo owner/repo` overrides the default `ab0t-com/sharedwatch`; `--yes` skips the interactive confirmation; `--timeout 60s` bounds network operations.
- Safe to run while a daemon is active: atomic rename(2) on Linux/macOS leaves the running process's inode untouched. The new binary takes effect when the daemon is next restarted.
- Lives in `cmd/sharedwatch/update.go`; uses only stdlib (`net/http`, `crypto/sha256`, `archive/tar`, `compress/gzip`). No new module deps.

### Added — watcher-side lease advisory warning (SW-AGENT-12, S7.9)
- When an event lands on a path covered by an active lease whose actor differs from the event's actor, the watcher (and reconciler) log a structured `slog.Warn` line including `event_id`, `rel_path`, `watch_root`, `event_actor`, `lease_id`, `lease_actor`, `lease_path_glob`, `lease_expires_at`. Advisory only — does not block the write. Cooperative peers see the warning in logs and can decide to back off.
- New `db.LeaseGlobMatchesPath(glob, path)` helper supports `*` (any non-slash run) and `**` (any depth), matching the EventFilter path-glob semantics. Covered by `TestLeaseGlobMatchesPath` (11 cases).
- Closes the coordination loop the v0.8.0 design promised: leases are now visible at write time, not only at read time.

### Fixed — skill freshness sweep
- Skills/sharedwatch-client-future/ no longer has stale "will land" / "when shipped" phrasing — converted to "since v0.8.0" everywhere applicable.
- Skills/sharedwatch-client/references/patterns.md SW-AGENT-11 caveat updated to present tense.
- `../docs/agent/agent-system-prompt-20260522.md` `[FUTURE]` markers removed; final block now includes a verified canonical-handoff example demonstrating spec→code→verify across two actors with intent, lease, and ref_event_id.

### Fixed — `.git` (and other directory ignore patterns) now match directory contents (dogfood S13)
- `catalog.Ignored()` previously matched only on the basename, the full rel_path, and exact equality. A pattern like `.git` matched only a file literally named `.git`, NOT files inside a `.git/` directory. So `.git/objects/abc` leaked into the journal even though `.git` was in the default ignore list. Discovered during the full 18-scenario dogfood pass.
- Extended `Ignored()` to also check each path segment. Any ancestor directory whose name matches the pattern causes the file to be ignored. Same logic applies to other directory-style patterns (`node_modules`, `.cache`, etc.).
- New regression test `TestIgnoredMatchesDirectorySegment` covers 10 cases including the bug case, three positive `.git`/`node_modules` cases, and negative cases that proved tricky (`.gitignore` should NOT match `.git`, `docs/git-tutorial.md` should NOT match `.git` — both correctly NOT ignored).

### Fixed — `--since` / `--until` accept durations (dogfood SZ.1)
- `events list --since 5m` (and `--until`) now parse Go duration strings as well as RFC3339(Nano) timestamps, matching documented behaviour. Discovered during the SW-AGENT-Z dogfood pass — the docs and Skill examples both promised duration support but the original parser only accepted RFC3339.

### Added — CI gitleaks scan (SZ.4)
- `.github/workflows/ci.yml` runs gitleaks against the full tree on every push/PR. Defense in depth alongside the local `.git/hooks/pre-commit` and `pre-push` hooks. Uses pinned gitleaks v8.24.3.

### Added — renderer compression (SW-AGENT-13, partial)
- `events stats --format text` collapses quiet hourly buckets into a `quiet HH:MM–HH:MM (Nh)` summary instead of emitting empty lines. Catches the sparse-24h-window case where 22 out of 24 hours have no activity.
- JSON / JSONL output is NOT compressed — they remain the canonical data shape per `docs/OUTPUT_CONTRACT.md`. Compression is text-only.
- DEFERRED to a followup: path-prefix factoring (`auth/{login.go, oauth.go}`), hash-stable elision, `--uncompressed` flag. All are text-only polish; no agents depend on them.

### Added — output contract + format_version sweep (SW-AGENT-14)
- `status --json` now carries `format_version: 1` as the first key. Pre-existing keys unchanged — additive only.
- New `docs/OUTPUT_CONTRACT.md` documents the envelope shape per surface (`events list`, `sql`, `schema`, `status`, `overview`, `events stats`, `intent list`, `lease list`), the version-bump policy, the `schema --format json` exception (returns a bare array by design), and the breaking-change checklist for any future bump.
- Existing envelopes that already carried `format_version` (via `internal/output` for events list / sql + the new overview / events stats surfaces) unchanged — this sweep brings status into line and codifies the policy.

### Added — intents + leases (SW-AGENT-12)
- **Intents table + CLI** (`sharedwatch intent declare <path-glob> --actor <id> --ttl 10m [--task ...] [--intent ...] [--metadata ...]`, `intent list [--actor ...] [--path-glob ...] [--all] [--json]`, `intent revoke <intent-id>`). Forward-looking advisory: "I plan to edit X within the next N minutes." Auto-pruned on every reconcile pass.
- **Leases table + CLI** (`lease grant <path-glob> --actor <id> --ttl 5m [--exclusive] [--metadata ...]`, `lease release <lease-id>`, `lease list`, `lease renew <lease-id> [--ttl 5m]`). Stronger advisory: "I'm editing X right now." Grant always succeeds by default but the response includes `conflict_with: [...]` listing any peer leases on overlapping paths; `--exclusive` makes grant refuse on conflict.
- **TTL enforcement** at insert AND renew: maximum 1 hour from `granted_at`. `RenewLease` clamps the new `expires_at` at `granted_at + LeaseTTLMax` even when the caller asks for longer — squat protection.
- Both new tables have `(actor_id, expires_at)` and `(expires_at)` indexes; auto-prune runs in the reconcile post-pass alongside the existing retention sweep.
- Tests: `TestIntentCRUD`, `TestPruneExpiredIntents`, `TestLeaseGrantRenewRelease`, `TestLeaseTTLMaxEnforced`, `TestLeaseRenewCannotExceedMaxFromGrantedAt`.
- DEFERRED to a follow-up: advisory warning when a `file.modified` event lands on a path covered by an active lease from a different actor (S7.9). The lease primitive ships and is queryable; the watcher-side warning hook can land independently.

### Added — events stats endpoint (SW-AGENT-10)
- New `sharedwatch events stats --root <label> [--since 24h] [--format text|json]`. L2 progressive-disclosure endpoint scoped to one watch_root: by_type histogram, by_actor counts, top_paths (with the actors who touched each, sorted by event count then recency then path), hourly buckets, and a drill map.
- **`--root` is REQUIRED** — forces agents to pick scope before drilling. Calling without it exits with status 2 and a clear message pointing to `overview` for cross-root counts.
- Passing `--root foo=/path` form (definition) to this filter command is rejected with a redirect message.
- `format_version: 1` first key (same convention as overview).
- Tests: `TestEventsStatsRequiresRoot`, `TestEventsStatsMultiRoot` (scope tightness — billing's charge.go must NOT appear in auth stats), `TestEventsStatsFormatVersionIsFirstKey`.

### Added — overview endpoint + drill envelope (SW-AGENT-9)
- New `sharedwatch overview [--since 24h] [--format text|json]` subcommand. Returns the L1 progressive-disclosure envelope: mode, pending, failed, events_in_range, by_type histogram, top_actors (top 5), per-root counts (when multi-root configured), active_actors (when registry populated), AND a `drill` map of follow-up shell commands.
- **`format_version: 1`** is the first JSON key emitted — agents can validate the schema before scanning the rest.
- **Drill map (HATEOAS-style hypermedia hints)**: every dimension in the overview carries one canonical follow-up command. `root:<label>` → `events list --root <label> --since 1h`; `actor:<id>` → `events list --payload-key actor --payload-value <id>`; plus `events_recent`, `events_failed`, `events_by_type`.
- **60s TTL cache** in `runtime_state` (new `UpsertRuntimeJSON` / `GetRuntimeJSON` helpers on Store). The cache is keyed on the canonical 24h window only — non-default `--since` values bypass so the cached entry stays the most-common shape.
- Tests: `TestOverviewEmptyDB`, `TestOverviewMultiRoot`, `TestOverviewFormatVersionIsFirstKey`, `TestOverviewCachedWithinTTL`.

### Added — multi-folder watching (SW-AGENT-3)
- **`watch_root` column on events / digests / snapshots.** Idempotent migration via `addColumnIfMissing`; default value `''` so legacy single-root rows continue to behave exactly as before (matching the "no `--root` filter set" semantics in EventFilter). Two new indexes: `idx_events_root_created` and `idx_events_root_relpath`.
- **`Config.WatchRoots []WatchRoot`** (Label, Path) — the multi-root configuration. Parsed from `watch_roots:` (inline `label=path,label=path` form) in config.yaml.
- **New `--root` CLI flag** with grammar disambiguated by `=`:
  - Definition (root flagset, applies to `init`/`run`/`reconcile`): `--root <label>=<path>`. Repeatable, comma-aware. Bare `--root <path>` auto-derives the label from the basename.
  - Filter (`events list`, `digest list`): `--root <label>`. Repeatable, comma-aware. Passing `<label>=<path>` to a filter command errors with a clear redirect to root.
  - `--watch-path` becomes repeatable for the auto-label single-root pattern.
- **Label validation**: 1–64 chars `[a-zA-Z0-9_-]`, no leading dash; reserved labels (`all`, `none`, `mixed`); auto-derived collisions rejected with a clear error pointing to `--root <label>=<path>`.
- **Pipeline correctness** — the load-bearing invariants:
  - `Store.FindRecentPendingByRelPathAndActor` gains a `watchRoot` parameter; coalesce key is now `(rel_path, watch_root, actor)`.
  - `Store.LatestSnapshot` / `Store.SaveSnapshot` / `Store.PruneOldSnapshots` keyed on `(source, watch_root)`. Snapshots no longer bleed across roots; pruning windows don't collapse.
  - `Watcher.Service.ScanAndQueue` and `Reconcile.Service.RunNow` iterate `Roots` and stamp each emitted event with the root's Label. Per-root cold-start emission.
  - `DetectRenames` runs per-root via the watcher loop — cross-root delete+create pairs with matching size/mtime stay as 2 events, not 1 phantom rename.
- **`status --json`** adds `roots[]` array (label, path, pending, last_event_at) ONLY when multi-root is configured. Single-root status JSON is byte-identical to pre-SW-AGENT-3 output.
- **`events list --root <label>`** filter (repeatable, comma-aware); `--include-watch-root` forces the column. The `watch_root` column auto-appears in output when the result spans more than one distinct root.
- **`digest list --root <label>`** filter. Consumer-produced digests carry `watch_root = <dominant root>` when all consumed events share one root, or `"mixed"` when the batch spans roots.
- **Auto-mkdir per root** in `app.NewWithLogger`. Each configured root's directory is created; legacy single `WatchPath` fallback unchanged.
- **Backward compatibility:** every single-root caller (config without `watch_roots:`, no `--root` flags) sees zero observable change. The empty-string `watch_root` sentinel propagates end-to-end.
- Regression tests: `TestSchemaMigrationIsIdempotent`, `TestCoalesceScopedToRoot`, `TestSnapshotsKeyedByRoot`, `TestEventFilterByRoot` (all in `internal/db/multi_root_test.go`). Plus `internal/config/roots_test.go` (label validation, normalize, collision detection).

### Added — actors registry + heartbeats (SW-AGENT-8)
- New `actors` table (idempotent migration): `(actor_id PK, label, actor_kind, focus, last_heartbeat, metadata_json)` + index on `last_heartbeat`. Schema visible via `sharedwatch schema --format json`.
- New top-level subcommand `sharedwatch actor heartbeat <actor-id> [--focus <glob>] [--kind <k>] [--label <l>] [--metadata <json>]`. Upserts the row: a thin heartbeat (just `actor_id`) refreshes `last_heartbeat` while preserving prior metadata; non-empty fields overwrite their slot. Cadence recommendation: ≤ 1/min per actor (cheap but additive).
- New `status --actors` (and `status --actors --json`) emits the live registry. Each row carries `stale=true` when `last_heartbeat < now - actor_ttl`. The `actors` array is only included when the flag is set, so existing `status --json` consumers are unaffected.
- New `Config.ActorTTL` (default 5 m), parseable from `config.yaml` under key `actor_ttl: 5m`. Drives both the staleness check and the retention prune.
- Reconcile loop now also calls `Store.PruneStaleActors(ctx, 2 * ActorTTL)` each pass — recently-stale actors stick around for one diagnostic cycle, then disappear.
- Soft-warn on actor_kind change between heartbeats (the most common identity-confusion smell). The heartbeat itself is never rejected; the warning is a slog `WARN` line.
- Tests: `internal/db/actors_test.go` (upsert idempotency, thin-heartbeat metadata preservation, list-ordering, prune, kind-change tolerance).

### Fixed — actor-aware coalesce (SW-AGENT-11)
- **Cross-actor events on the same path no longer silently merge.** The coalesce key is now `(rel_path, actor)` where `actor` is parsed from `payload_json.actor` via the new `events.ExtractActor` helper. Two different actors editing the same file inside the 5 s window produce two distinct events; same-actor events still coalesce; legacy empty-actor events still coalesce with other empty-actor events. Fixes the silent attribution loss exposed by dogfood scenario 17 (`../docs/dogfood/test_dogfood.md`).
- New `Store.FindRecentPendingByRelPathAndActor(ctx, relPath, actor, since)` returns the most recent pending event on `relPath` whose actor matches. Implementation fetches a bounded candidate window (LIMIT 16) and filters actor in Go via `events.ExtractActor` — avoids any dependency on SQLite's JSON1 extension for a hot-path query. `Store.FindRecentPendingByRelPath` is retained for callers that don't need actor scoping.
- `events.ShouldCoalesce` documents and enforces the actor-equality contract.
- Regression test `TestCoalesceScopedToActor` covers six cases: same-actor-merge, cross-actor-distinct, empty+empty-merge, empty+named-distinct, outside-window-distinct, three-actors-interleaved.

### Added — agent-fit follow-up (SW-AGENT-7 — payload v1 attribution)
- **`payload_json` v1 schema + helpers (new `internal/events/payload.go`):** canonical `PayloadV1` struct with fields `actor`, `actor_kind`, `session`, `task`, `intent`, `addressee`, `ref_event_id`, `tags`. `BuildPayloadV1` always sets `schema_version: 1`, omits empty optional fields, and returns `""` for an empty struct so the historical "no payload" behaviour is preserved when attribution isn't requested. Companion `ExtractActor(payloadJSON)` returns the actor field tolerantly (unknown keys ignored, forward-version payloads still readable).
- **First-class attribution flags:** `--actor`, `--actor-kind`, `--session`, `--task`, `--intent`, `--addressee`, `--ref` (sets `ref_event_id`), `--tag` (repeatable, comma-aware). Available on both the root flagset and on `test emit`; subcommand values override root values per-field, non-empty wins.
- **Mutual exclusion:** `--payload '<raw-json>'` and the attribution flags refuse to be mixed on `test emit`; the user picks one form.
- **Watcher + reconciler propagation:** when any attribution flag is set on the root flagset, every event emitted by the watcher diff loop AND the reconciler's drift-recovery / cold-start path stamps the v1 payload onto events that don't already carry one. Plumbed via new `Config.PayloadJSON` → `watcher.Service.PayloadJSON` / `reconcile.Service.PayloadJSON`. Empty `PayloadJSON` is strictly legacy behaviour (existing single-tenant callers unaffected).
- **Tests:** `internal/events/payload_test.go` (build/empty/round-trip/schema-version-coercion/ExtractActor table). `internal/app/attribution_test.go` (watcher path, reconciler cold-start path, empty-attribution legacy-behaviour preservation).

### Added — agent-fit follow-up (SW-AGENT-2, 4, 5, 6 + small fixes)
- **Producer-supplied payload (SW-AGENT-2):** `sharedwatch test emit <relpath> --payload '<json>'` accepts arbitrary JSON object payload; stored in `events.payload_json`. Validated as JSON before insert.
- **Payload filter (SW-AGENT-2):** `events list --payload-key K --payload-value V` post-filters by parsing `payload_json` as an object and matching `obj[K] == V`.
- **Content hashing (SW-AGENT-4):** `--hash on|off` global flag enables SHA-256 of file content during snapshot building. Bounded by `HashMaxSize` (default 1 MB) so big binaries aren't hashed. Hash now propagates correctly into `file.created`/`file.modified`/`file.deleted` events (was previously empty — a real bug discovered in dogfooding). Detects same-size, same-mtime, different-content changes.
- **Producer attribution (SW-AGENT-5):** new `events.producer_id` column (migrated idempotently). Default producer is `<hostname>:<pid>`; `--producer <name>` global flag overrides. All emitted events (watcher, reconcile, test emit) stamp the producer. `events list --producer <name>` (repeatable) filters.
- **Include-only globs (SW-AGENT-6):** `--include <pattern>` global flag (repeatable + comma-aware) restricts the watcher/reconciler to a positive set of files. Applied before `IgnorePatterns`.
- **Retry cap:** `events retry --max-retries N` only requeues events whose `retry_count < N`. Default 0 = no cap (current behavior).
- **Stuck-event recovery:** `events recover-stuck [--older-than 5m]` flips events stuck in `status='processing'` (e.g. from a crashed consumer) back to `pending`.
- `--fields` now exposes `retry_count` and `coalesced_into` (previously silently dropped).

### Fixed
- **Hash propagation in diff:** `watcher.DiffSnapshots` was constructing event rows without `Hash`, so even with hashing enabled the modified-event would carry an empty hash, and coalesce-on-pending would preserve the stale baseline hash. Now propagates `oldFile.Hash` / `newFile.Hash` into create/modify/delete events. Dogfood: frozen-mtime same-size content swap now produces a `file.modified` with the correct new hash.

### Added — agent-facing event surface (SW-AGENT-1)
- `sharedwatch events list` — read-only event query with `--since`, `--until`, `--type` (repeatable), `--source` (repeatable), `--status` (repeatable), `--path-glob` (supports `**`), `--limit`, `--order asc|desc`, `--format text|json|jsonl|csv`, `--fields` (column projection), `--since-cursor`, `--cursor-name`, `--no-advance`. Does NOT consume events.
- Cursor primitive:
  - opaque stateless tokens via `--since-cursor <tok>` (base64-encoded `{created_at_nano, id}`)
  - named server-side cursors persisted in a new `cursors` table; advance on read by default, `--no-advance` to peek
  - `sharedwatch events cursor list | reset <name> | set <name> --since-cursor <tok> | encode --created-at <ts> --id <evt_id> | decode <tok>`
- `sharedwatch sql <query>` — raw SQL escape hatch. Read-only by default (rejects DELETE/UPDATE/CREATE/etc + multi-statement); `--write` to opt in; `--explain` prints `EXPLAIN QUERY PLAN` to stderr; `--format text|json|jsonl|csv`. Accepts query inline, via `-` (stdin), or `--file <path>`; works with flags in any order relative to `-`.
- `sharedwatch schema [<table>] [--format text|json]` — prints live DDL from `sqlite_master` + structured per-column metadata.
- Shared `internal/output` package with `text|json|jsonl|csv` renderers, cursor sentinel handoff, `--fields` projection, and a `format_version: 1` field embedded in JSON/JSONL envelopes for future evolution.
- New `cursors` table: `(name PK, created_at_nano, last_id, updated_at)`.

### Added
- `sharedwatch events retry` subcommand: requeues all `failed` events back to `pending` so a follow-up `consume` can pick them up. Closes the engineering report's B10 gap (failed events had no recovery path).
- `sharedwatch digest list --status pending|read|archived` filter.
- `Makefile` targets: `ci`, `smoke`, `run`, `clean`, with `VERSION` overridable for build-time linking.
- `sharedwatch init` subcommand that materializes the data directory + DB and prints the resolved paths.
- `sharedwatch version` subcommand and `--version` global flag (versioned via `-ldflags -X main.Version=…`).
- `sharedwatch help` / `--help` with a full command table and examples.
- `--config` global flag now actually loads the file (the previous `config.Load` was dead code).
- `--watch-path`, `--db`, `--data-dir` global flag overrides (highest precedence over config file).
- `--log-format=text|json` and `--log-level=debug|info|warn|error` global flags.
- `--ignore <pattern>` global flag, repeatable and comma-aware, appended onto `cfg.IgnorePatterns`.
- `sharedwatch status --json` for machine-readable status output.
- `active_expires_in` derived field on `status` (shows the remaining active TTL).
- Snapshot-table pruning: latest 5 snapshots retained per source. Stops unbounded growth from per-tick inserts.
- Synthetic-event path validation (`test emit`): rejects empty / absolute / `..`-escaping relpaths.
- Run-time mutual exclusion via OS file lock (`<data_dir>/sharedwatch.lock`); concurrent `run` against the same DB now fails fast.
- Structured logs throughout the `run` loop (`log/slog`).
- New unit tests: `reconcile.RunNow` (first-pass + drift + snapshot-bounding), `consumer.ConsumePending` end-to-end, `renameKey` for sizes > 0x10FFFF, `validateRelPath` table-driven.

### Changed
- Default paths are now XDG-style: `$XDG_DATA_HOME/sharedwatch/{watch,queue.db}` (falls back to `$HOME/.local/share/sharedwatch/...`). Previously hardcoded to `/home/node/.openclaw/...`.
- `cfg.RetentionDays` is honored by `reconcile.RunNow` (previously hardcoded to 30 days regardless of config).
- `db.Adapter` interface extended with `MarkDigestRead`, `MarkDigestArchived`, `PruneOldProcessedEvents`, `PruneArchivedDigests`, `PruneOldSnapshots`, `RequeueFailedEvents`, `ListDigestsFiltered`. The pre-existing call sites referenced the first four through `Adapter` but they were only on `*Store`; the project did not compile.
- Reconcile cold-start emits `file.created` for every pre-existing file on the first reconcile (previously: silent baseline, forcing users to call `reconcile now` twice).
- Rename digest line shows the *relative* old path (was the absolute filesystem path; visually noisy and inconsistent with every other event line).
- SQLite open enables WAL + busy_timeout via PRAGMA so concurrent CLI calls work while `run` is active. (Previously: `migrate: database is locked (SQLITE_BUSY)` whenever a second process touched the DB.)
- `CONTRIBUTING.md` rewritten with ground rules, PR checklist, and OSS-generic framing.
- `README.md` rewritten with a 60-second Quickstart, full command table, global-flag reference, mental model, and operational notes.
- Nullable SQL columns (`old_path`, `mtime`, `content_hash`, `coalesced_into`) now scan via `sql.NullString` and bind as SQL `NULL` on insert. Previously `Scan` returned `"converting NULL to string is unsupported"` against rows with no `old_path`.
- `mode active` reports the *effective* deadline (after the `<=0 → ActiveTTL` fallback), not the requested value.
- `digest show <missing>` / `digest archive <missing>` return a clean `digest not found: <id>` instead of leaking `sql: no rows in result set` / silently succeeding.
- `digest list` prints a helpful "no digests yet" line when the table is empty.
- Unknown subcommands print `unknown subcommand: <x>` + usage and exit 2, instead of failing inside `app.New` with a misleading mkdir error.
- `version` / `help` / `--version` no longer touch the DB, so they work on a fresh checkout without writable data dirs.

### Fixed
- `renameKey` constructed via `string(rune(e.Size))` collapsed all sizes > 0x10FFFF (~1.1MB) into a single key (the Unicode replacement char). Switched to `strconv.FormatInt`.
- Removed dead `consumer.Summarize` (unreachable; `digest.RenderHumanSummary` is the live path).
- Removed orphan literal-brace directory created by an accidentally-quoted `mkdir -p "{cmd/...}"`.
- `StatusSnapshot` no longer emits `0001-01-01T00:00:00Z` for empty time fields (uses `*time.Time` + `omitempty`).

### Security / supply chain
- License: MIT (added).
- `internal/app` lock prevents concurrent `run` from corrupting state.
- Synthetic-event relpath validation closes the smallest path-traversal foothold (the `test emit` subcommand).
