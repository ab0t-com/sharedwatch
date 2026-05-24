# TICKET: Smart defaults, config introspection, and a `stop` command (v0.0.5 ship)

**ID:** SW-AGENT-18
**Filed:** 2026-05-24 04:49 UTC
**Filed by:** claude (this session)
**Status:** in progress — claimed by claude (this session)
**Target:** sharedwatch v0.0.5
**Estimated:** ~1 focused engineer-day (small, well-shaped, ~80% additive)
**Branch:** `main` (pre-1.0, additive scope only — no feature branch)

---

## 1. Motivation

Three threads from the SW-AGENT-17 review converge here. The user approved all three; this ticket bundles them so they ship together as v0.0.5.

1. **Smart defaults phases 1–3** from [`docs/design/defaults-audit-20260524.md`](../docs/design/defaults-audit-20260524.md). Phase 1 (default `events list --since` and expanded `ignore_patterns`) already landed on `main`; Phase 2 (env-var resolution for agent identity + output preferences) and Phase 3 (cursor-name as env var) land here.
2. **Config introspection.** `sharedwatch config show` so an agent (or human) can print "what defaults am I actually running with, and where did each value come from?" without reading source. Eliminates the "is my SHAREDWATCH_ACTOR even being read?" debug loop.
3. **Stop command.** `sharedwatch stop` to send SIGTERM to the running daemon by reading the PID from its lock file. Today the only ways to stop a `run` are Ctrl-C in the foreground, `kill $(cat <data_dir>/sharedwatch.lock)`, or a service manager. A first-class verb closes the lifecycle gap.

---

## 2. Scope

### In scope

- **Config file location.** Search order on startup, lowest precedence first:
  - Built-in defaults (in code).
  - `$XDG_CONFIG_HOME/sharedwatch/config.yaml` (or `~/.config/sharedwatch/config.yaml` if `XDG_CONFIG_HOME` is unset). **New** — first time we look in the XDG config dir.
  - `./config.yaml` (project-local; current default — unchanged).
  - `--config <path>` (explicit override; current behaviour — unchanged).
- **Config parser completeness.** Add `Load()` cases for the five `Config` fields that today are silently ignored when present in YAML: `include_patterns`, `hash_enabled`, `hash_max_size`, `producer_id`, plus the new keys: `actor`, `actor_kind`, `default_format`, `default_root`, `hints`, `default_since`.
- **Env var resolution layer.** Insert between config and flags. Read on every invocation:
  - `SHAREDWATCH_ACTOR`, `SHAREDWATCH_ACTOR_KIND`, `SHAREDWATCH_SESSION`, `SHAREDWATCH_TASK`, `SHAREDWATCH_ADDRESSEE` — populate the attribution payload defaults.
  - `SHAREDWATCH_FORMAT` — default `--format` for commands that accept it.
  - `SHAREDWATCH_ROOT` — default `--root` filter for read commands.
  - `SHAREDWATCH_CURSOR_NAME` — default `--cursor-name` for `events list`.
  - `SHAREDWATCH_HINTS` — already supported (v0.0.4); kept as-is.
- **`sharedwatch config show` subcommand.** Prints:
  - Effective resolved config (every field with its current value).
  - Which env vars are set (and what value).
  - Which config files were searched (and which existed).
  - Resolution order legend at the bottom.
  - `--json` for machine output (envelope with `format_version: 1`).
- **`sharedwatch stop` subcommand.** Reads PID from `<data_dir>/sharedwatch.lock`, sends SIGTERM, polls for lock-file removal with a `--timeout` (default 10s), optionally escalates to SIGKILL with `--force`. Exits non-zero if the daemon won't die in time.
- **Hint provider for `stop`** in `internal/hints/providers.go`: suggests `sharedwatch run` after a successful stop.
- **CHANGELOG, src/README, top-README, man page, Skills** updates.
- **Release v0.0.5** via the in-repo flow.

### Out of scope (deferred)

- **JSON config format.** Stick with YAML for v0.0.5. If a user explicitly asks for `~/.config/sharedwatch/config.json` later, add a parser then; the format detection is a 4-line addition.
- **A `--cursor-auto` flag** that defaults `--cursor-name` to the resolved actor. The audit's Phase 3 originally proposed this; replaced here with the explicit `SHAREDWATCH_CURSOR_NAME` env var. Implicit-magic-from-actor is harder to reason about than "set this one env var".
- **Per-key provenance tracking** in `config show`. We show *which sources contributed* (config file path, env var name) but don't track per-field origin to avoid a heavy refactor of `Load()`. Sufficient for debugging in practice.
- **Hooks system.** Discussion already filed in [`docs/design/hooks-discussion-20260524.md`](../docs/design/hooks-discussion-20260524.md). Not built here.

---

## 3. Design — locked decisions

### 3.1 Resolution chain

```
flag (highest)
  → env var (SHAREDWATCH_*)
    → config file (xdg config OR project-local OR --config override)
      → built-in default (lowest)
```

Every layer's job is to fill in fields the higher layers left empty. No layer ever errors when the field is unset; defaults flow through.

### 3.2 Config file search

In `Load()`, when no explicit `--config <path>` was given, try:

```
./config.yaml
$XDG_CONFIG_HOME/sharedwatch/config.yaml   (or ~/.config/sharedwatch/config.yaml)
```

In **that order**. First found wins. Both must be optional — startup never fails on missing config. Log at INFO which file (if any) was loaded, so `--log-level debug` (or `config show`) surfaces it.

### 3.3 Env var names (canonical list)

| Env | Maps to | Notes |
|---|---|---|
| `SHAREDWATCH_ACTOR` | payload_json.actor | required to populate the others (matches flag rules) |
| `SHAREDWATCH_ACTOR_KIND` | payload_json.actor_kind | `human` / `ai_agent` / `automation` |
| `SHAREDWATCH_SESSION` | payload_json.session | per-shell session id |
| `SHAREDWATCH_TASK` | payload_json.task | per-shell task label |
| `SHAREDWATCH_ADDRESSEE` | payload_json.addressee | rare |
| `SHAREDWATCH_FORMAT` | `--format` default | `text` / `json` / `jsonl` / `csv` |
| `SHAREDWATCH_ROOT` | `--root` default | filter only; doesn't define a new root |
| `SHAREDWATCH_CURSOR_NAME` | `--cursor-name` default | named server-side cursor |
| `SHAREDWATCH_HINTS` | `--hints` default | already supported (v0.0.4) |

### 3.4 `config show` output shape

Text mode (default):

```
$ sharedwatch config show
EFFECTIVE CONFIG
  watch_path:                /home/me/.local/share/sharedwatch/watch
  db_path:                   /home/me/.local/share/sharedwatch/queue.db
  data_dir:                  /home/me/.local/share/sharedwatch
  coalesce_window:           5s
  passive_interval:          10m
  active_interval:           5s
  reconcile_interval:        30m
  max_batch_size:            100
  retention_days:            30
  actor_ttl:                 5m
  hash_enabled:              false
  ignore_patterns:           .git, .DS_Store, *.tmp, *.swp, node_modules, ...
  watch_roots:               (none — single-root mode)

ENV VARS DETECTED
  SHAREDWATCH_ACTOR=claude-coord-1
  SHAREDWATCH_FORMAT=jsonl
  SHAREDWATCH_HINTS=agent
  (no SHAREDWATCH_SESSION / _TASK / _ADDRESSEE / _ROOT / _CURSOR_NAME / _ACTOR_KIND)

CONFIG FILES SEARCHED
  ✓ /home/me/.config/sharedwatch/config.yaml  (loaded)
  ✗ ./config.yaml                              (not present)

RESOLUTION ORDER
  flag > env > config > built-in default
```

JSON mode (`--json`):

```json
{
  "format_version": 1,
  "effective": { ...resolved Config struct... },
  "env": { "SHAREDWATCH_ACTOR": "claude-coord-1", ... },
  "config_files_searched": [
    {"path": "/home/me/.config/sharedwatch/config.yaml", "loaded": true},
    {"path": "./config.yaml", "loaded": false}
  ],
  "resolution_order": ["flag", "env", "config", "default"]
}
```

### 3.5 `stop` semantics

```
$ sharedwatch stop
sent SIGTERM to pid 12345, waiting up to 10s for shutdown...
daemon stopped cleanly

$ sharedwatch stop --timeout 30s
$ sharedwatch stop --force         # escalates to SIGKILL after timeout
```

Lock file path: `filepath.Dir(cfg.DBPath) + "/sharedwatch.lock"`. Same convention as the run-side `acquireRunLock`.

Exit codes:
- `0` — daemon stopped, or no lock file (nothing to stop).
- `1` — daemon still running after timeout (and `--force` not set, or SIGKILL also failed).
- `2` — usage error (bad flag).

---

## 4. Acceptance criteria

1. `sharedwatch --hints off status` works exactly the same as before (no regressions).
2. `SHAREDWATCH_ACTOR=claude-x sharedwatch test emit foo.md` produces an event with `payload_json.actor == "claude-x"`, even without `--actor` on the command line.
3. `--actor flag-value` on the command line wins over `SHAREDWATCH_ACTOR=env-value`.
4. `$XDG_CONFIG_HOME/sharedwatch/config.yaml` is loaded when no `./config.yaml` exists.
5. `./config.yaml` (when present) wins over the XDG config file.
6. `--config <explicit-path>` wins over both.
7. `sharedwatch config show` lists every field, env var, and searched config file as in §3.4.
8. `sharedwatch config show --json | jq .format_version` returns `1`.
9. With a running daemon, `sharedwatch stop` sends SIGTERM, waits, and exits 0 cleanly. The lock file is gone after exit.
10. Without a running daemon, `sharedwatch stop` prints a friendly "no running daemon" line and exits 0.
11. `sharedwatch stop --force` escalates to SIGKILL after `--timeout`.
12. Five previously-ignored YAML keys (`include_patterns`, `hash_enabled`, `hash_max_size`, `producer_id`, `actor`) are now read from `config.yaml` and respected.
13. All existing test packages still green: `gofmt -l .` empty, `go vet ./...` clean, `go test ./... -count=1` passes.
14. CHANGELOG `[Unreleased]` block updated. `src/README.md` global flags + env var table updated. Top-level `README.md` "For AI agents" section mentions the new env vars. `man/sharedwatch.1` adds `stop` + `config show` + env var documentation. `Skills/sharedwatch-client/SKILL.md` mentions `export SHAREDWATCH_ACTOR=...` as the agent-startup ritual.
15. Release v0.0.5 cut + pushed per GITOPS.md §10. `curl ... | bash` installs `v0.0.5` cleanly.

---

## 5. Test plan

- **Unit (`internal/config/file_test.go`):**
  - `TestLoad_XDG_Fallback` — XDG path loaded when no project-local config.
  - `TestLoad_ProjectLocalWinsOverXDG` — project-local takes precedence.
  - `TestLoad_ExplicitConfigWins` — `--config <path>` overrides both.
  - `TestLoad_NewKeys_*` — five new keys parsed correctly.
- **Unit (`cmd/sharedwatch/`):**
  - `TestResolveAttrFromEnv_*` — every env var maps correctly; flag wins over env.
- **Integration (smoke in this session):**
  - `SHAREDWATCH_ACTOR=x test emit y.md && events list | jq .[].payload_json` — actor present.
  - `sharedwatch config show` — output matches §3.4 shape.
  - `sharedwatch run &; sharedwatch stop` — clean shutdown.
- **No new hints test needed.** The `stop` hint is trivial (one Hint, one test).

---

## 6. References

- [`docs/design/defaults-audit-20260524.md`](../docs/design/defaults-audit-20260524.md) — the audit this ticket implements.
- [`docs/design/hooks-discussion-20260524.md`](../docs/design/hooks-discussion-20260524.md) — companion design doc from same conversation; NOT built here.
- [`tickets/ticket-smart-hints-profiles-20260524_040256.md`](./ticket-smart-hints-profiles-20260524_040256.md) — SW-AGENT-17, the immediately prior ticket; shipped v0.0.4 and established the "hints engine + env-var-aware resolution" pattern this ticket extends.
- [`src/internal/config/{config.go,file.go}`](../src/internal/config/) — config struct + parser.
- [`src/internal/app/lock.go`](../src/internal/app/lock.go) — lock-file convention used by `stop`.
- [`GITOPS.md`](../GITOPS.md) §10 — release flow for v0.0.5.

---

## 7. Definition of done

All 15 acceptance criteria green. `release/v0.0.5/` present, `release/LATEST` = `v0.0.5`, `git push origin refs/heads/main refs/heads/v0.0.5 refs/tags/v0.0.5` succeeded. The canonical `curl | bash` installs v0.0.5; `sharedwatch config show` prints the expected output; `SHAREDWATCH_ACTOR=x sharedwatch test emit y.md && sharedwatch events list --format jsonl | jq .` shows `x` in `payload_json.actor`. Ticket closed; worklog in the companion tasklist closes out the last entry.
