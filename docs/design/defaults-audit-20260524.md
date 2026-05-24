# sharedwatch — smart-defaults / config-vs-flag audit

**Date:** 2026-05-24
**Status:** discussion document → action list
**Author:** claude (this session)
**Trigger:** user question — "are we smart-defaulting everything? what else can we set in config rather than making the agent repeat it every time, or what else can be optional with a smart default?"

This document audits every CLI flag and config option to identify where an AI agent (or a human) is currently forced to repeat themselves, and proposes which of those repetitions can be replaced by config-file or env-var defaults without breaking backward compatibility.

---

## 1. Resolution chain — current and proposed

**Today:** `built-in defaults < config.yaml < global flags`

**Proposed:** `built-in defaults < config.yaml < env vars < command-line flags`

Env vars get added between config and flags so that:
- Long-lived per-shell identity (e.g. `SHAREDWATCH_ACTOR`) is set once at session start.
- Per-invocation overrides on the command line still win when needed.
- Config remains the project-wide baseline.

The existing `SHAREDWATCH_HINTS` env var already follows this convention. The proposal is to generalise the pattern to every "I'd rather not type this every time" field.

---

## 2. Audit — what every flag costs an agent today

Marked **🟢 well-defaulted**, **🟡 minor gap**, **🔴 forces repetition that hurts agents in practice**.

### Identity flags (attribution v1)

All currently flag-only. Agents have a stable identity for their whole lifetime — these are the **single biggest source of repetition** for an AI agent driving the CLI.

| Flag | State today | Proposed default surface |
|---|---|---|
| `--actor` | 🔴 flag-only | `actor:` in config + `SHAREDWATCH_ACTOR` env |
| `--actor-kind` | 🔴 flag-only | `actor_kind:` in config + `SHAREDWATCH_ACTOR_KIND` env |
| `--session` | 🔴 flag-only | `SHAREDWATCH_SESSION` env (per-shell; not in config — sessions are not project-stable) |
| `--task` | 🟡 flag-only | `SHAREDWATCH_TASK` env (per-shell) |
| `--intent` | 🟡 flag-only | flag-only stays — intents are per-action, not per-session |
| `--addressee` | 🟡 flag-only | `SHAREDWATCH_ADDRESSEE` env (rare; for agents that always reply to the same peer) |
| `--ref` | 🟢 flag-only correct | per-event |
| `--tag` | 🟢 flag-only correct | per-event |

**Win:** an agent can set `SHAREDWATCH_ACTOR=claude-coord-1 SHAREDWATCH_ACTOR_KIND=ai_agent SHAREDWATCH_SESSION=$(uuidgen)` once at session start and never type those again. `--intent "..."` stays per-action (it varies). Three flags drop from every command.

### Output preferences

| Flag | State today | Proposed default surface |
|---|---|---|
| `--format` | 🔴 flag-only; default `text` | `default_format:` in config + `SHAREDWATCH_FORMAT` env. Agents set `jsonl` once, never type it again. |
| `--limit` | 🟢 sensible per-command defaults (20 for digest list, 100 for events list) | Could add `default_limit:` but per-command defaults already serve well. **Don't add.** |
| `--order` | 🟢 `desc` is right for most queries | flag-only stays |
| `--fields` | 🟢 sensible projection | flag-only stays |
| `--hints` | 🟢 already env + auto-promote-to-agent for JSON | Add `hints:` to config.yaml for completeness |

### Query scoping

| Flag | State today | Proposed default surface |
|---|---|---|
| `--since` on `events list` | 🔴 **no default — returns whole journal**, sometimes thousands of rows | Default `24h` (matches `overview` and `events stats`). |
| `--since` on `overview` / `events stats` | 🟢 default `24h` | ✓ |
| `--cursor-name` | 🔴 flag-only; agents almost always use the same name (their own actor id) | Default to the resolved actor when `--cursor-name` is empty AND `--since` is empty (cursor mode kicks in automatically). |
| `--root` (filter on subcommands) | 🟡 flag-only | `default_root:` in config + `SHAREDWATCH_ROOT` env, for agents scoped to one root. |
| `--type` / `--source` / `--status` | 🟢 flag-only correct | per-query |
| `--path-glob` | 🟢 flag-only correct | per-query |
| `--payload-key` / `--payload-value` | 🟢 flag-only correct | per-query |

### Daemon / pipeline knobs

All already config-aware. Just listing for completeness.

| Knob | Default | In `config.yaml`? |
|---|---|---|
| `coalesce_window` | 5s | ✓ |
| `passive_interval` | 10m | ✓ |
| `active_interval` | 5s | ✓ |
| `active_ttl` | 30m | ✓ |
| `reconcile_interval` | 30m | ✓ |
| `max_batch_size` | 100 | ✓ |
| `retention_days` | 30 | ✓ |
| `actor_ttl` | 5m | ✓ |
| `ignore_patterns` | `.git, .DS_Store, *.tmp, *.swp` | ✓ — **but the default list is thin** (see §3) |
| `recursive` | true | ✓ |
| `watch_path` / `watch_roots` | XDG default / multi-root | ✓ |
| `data_dir` / `db_path` | XDG default | ✓ |

### Config-struct fields **without** a yaml parser entry (existing bug)

These fields exist on `Config` and influence behaviour, but the `Load()` function in `internal/config/file.go` doesn't actually read them from `config.yaml`. A user putting `hash_enabled: true` in their config today is silently ignored.

| Field | Current source |
|---|---|
| `IncludePatterns` | flag-only (`--include`) |
| `HashEnabled` | flag-only (`--hash`) |
| `HashMaxSize` | code default; no flag, no config |
| `ProducerID` | flag-only (`--producer`) |
| `PayloadJSON` | flag-only (attribution flags) |

**Fix:** add parser cases for each. Trivial; the schema is already there.

---

## 3. The two known gaps from the prior conversation

### Gap A — `events list --since` has no default

A bare `events list` on a journal with months of activity returns every row. For an interactive human this is mildly annoying; for an AI agent it's a token-budget killer.

**Fix:** default to `24h` when both `--since` and `--since-cursor` and `--cursor-name` are all unset. Cursor mode (which scopes by stored position) overrides naturally. Matches `overview` and `events stats` semantics.

### Gap B — `ignore_patterns` default list is sparse

Today: `.git, .DS_Store, *.tmp, *.swp`. Common dev artifacts that leak into the journal as noise:

- `node_modules`
- `__pycache__`
- `.cache`
- `.venv` / `venv`
- `target/` (Rust)
- `dist/` / `build/`
- `*.log`
- `.idea` / `.vscode` (some users want these tracked; left to user)

**Fix:** extend the built-in default ignore list with the universally-noisy ones (`node_modules`, `__pycache__`, `.cache`, `.venv`, `target`, `dist`, `build`, `*.log`). Users who want any of these tracked can override via `ignore_patterns:` in config or `--ignore '!*.log'`-style overrides (latter is a future feature).

---

## 4. Recommended phased rollout

### Phase 1 — ship now (v0.0.5, ~half a day)

Pure bug-fix + papercut work. No new mental model required.

1. Default `events list --since` to `24h` when no cursor / explicit since is set.
2. Extend default `ignore_patterns` with the 7 universally-noisy entries (§3 Gap B).
3. Add config-parser cases for the five Config-struct-without-yaml fields (§2 "Config-struct fields without a yaml parser entry").

### Phase 2 — agent identity defaults (v0.0.6, ~half a day)

The biggest UX win. Wires env vars into the resolution chain for the attribution flags. Documented as opt-in.

1. Insert env-var resolution between config and flags in the attribution-flag bind path.
2. Read `SHAREDWATCH_ACTOR`, `_ACTOR_KIND`, `_SESSION`, `_TASK`, `_ADDRESSEE` at startup.
3. Add `actor:` and `actor_kind:` to `config.yaml` parser (rare project-wide defaults, but useful for service-account-style agents).
4. Add `default_format:` (config) + `SHAREDWATCH_FORMAT` (env) for the format preference.
5. Add `default_root:` (config) + `SHAREDWATCH_ROOT` (env) for single-root-focused agents.

### Phase 3 — cursor-name auto-default (v0.0.7, ~quarter day)

Smallest scope but highest taste-call: should `events list` default `--cursor-name` to the resolved actor? Pros: zero-flag tailing for agents. Cons: implicit behaviour can surprise. Recommend yes, but only when `--since` AND `--since-cursor` are both unset (so it only kicks in when the caller clearly meant "give me what's new").

### Out of scope

- A `--profile <name>` flag that loads a named config section (over-engineering for pre-1.0).
- A "config wizard" interactive setup (the YAML is already short enough).
- Validation that env-var names match the canonical set (typos fall through to default, which is the right failure mode for env config).

---

## 5. Open questions

- **Q1.** Should `SHAREDWATCH_*` env vars be documented in the **man page** under `ENVIRONMENT`, or only in `src/README.md`? **Recommendation:** both. Man page gets a brief entry per var; README gets the full table with precedence.
- **Q2.** When config has `actor: X` and env has `SHAREDWATCH_ACTOR=Y`, env wins (per §1). But should the daemon log a WARN at startup that there's a config/env mismatch? **Recommendation:** no — env-wins-over-config is a stable contract; mismatches are intentional. INFO-level log is fine for transparency.
- **Q3.** Should we add a `sharedwatch config show` subcommand that prints the resolved effective config (with provenance — "this value came from config.yaml line 14 / env SHAREDWATCH_X / flag --y / built-in default")? **Recommendation:** yes, ship in v0.0.5 alongside Phase 1. It pays for itself the first time someone debugs why a default isn't what they expected.

---

## 6. References

- [`SW-AGENT-17 ticket`](../../tickets/ticket-smart-hints-profiles-20260524_040256.md) §3.3 — pattern for resolution chains (used here for env-var precedence).
- [`./hooks-discussion-20260524.md`](./hooks-discussion-20260524.md) — companion design discussion from the same conversation; both shaped by the "calm, opt-in, doesn't break existing behaviour" axis.
- [`../../src/internal/config/config.go`](../../src/internal/config/config.go) + [`file.go`](../../src/internal/config/file.go) — the parser to extend.
- [`../../src/cmd/sharedwatch/main.go`](../../src/cmd/sharedwatch/main.go) — `bindAttrFlags` and the root flagset wiring is where env-var resolution lands.
- [`../../GITOPS.md`](../../GITOPS.md) §Versioning — sub-1.0 phased rollout is fine.
