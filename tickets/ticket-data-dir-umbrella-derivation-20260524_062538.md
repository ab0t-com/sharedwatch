# TICKET: `--data-dir` re-derives `watch_path` + `db_path` defaults (umbrella semantics)

**ID:** SW-AGENT-21
**Filed:** 2026-05-24 06:25 UTC
**Filed by:** claude (this session)
**Status:** in progress — claimed by claude (this session)
**Target:** sharedwatch v0.0.8
**Estimated:** ~quarter day
**Branch:** `main`

---

## 1. Motivation

[`tickets/tasklist_20260524_053824.md`](./tasklist_20260524_053824.md) Finding F2: `sharedwatch --data-dir /tmp/X init` silently splits the install — `data_dir` moves to `/tmp/X`, but `watch_path` + `db_path` stay at their `$XDG_DATA_HOME`-derived defaults. Surprising. The workaround documented in [`docs/agent/agent-system-prompt-20260522.md`](../docs/agent/agent-system-prompt-20260522.md) ("use `XDG_DATA_HOME` instead, or pass three flags together") is a sign the surface itself is wrong.

The conventional answer for **umbrella-shaped CLIs** (single data tree, multiple sub-locations: e.g. `docker --root-dir`, `helm --home`, `git --git-dir`, `homebrew prefix`, `pyenv root`) is that the umbrella flag re-derives sub-paths when those aren't explicitly set. Sharedwatch is umbrella-shaped; its CLI surface should match.

---

## 2. Scope

### In scope

- `--data-dir <X>` sets `DataDir = X`; additionally, when `--watch-path` and `--db` are unset AND the cfg fields are still at their built-in defaults, derive:
  - `WatchPath = X/watch`
  - `DBPath    = X/queue.db`
- Re-run dogfood scenario 21 against the patched binary to confirm the fix.
- Remove the corresponding bullet from the "Known gotchas" section in [`docs/agent/agent-system-prompt-20260522.md`](../docs/agent/agent-system-prompt-20260522.md) and [`Skills/sharedwatch-client/SKILL.md`](../Skills/sharedwatch-client/SKILL.md) (since the workaround no longer applies).
- Bump CHANGELOG, man page, embedded help-banner text.
- Release v0.0.8 via the in-repo flow.

### Out of scope

- Re-deriving paths from a config-file `data_dir:` key. Pre-existing behaviour. Leave alone; this ticket is about CLI-flag semantics only.
- Changing how `XDG_DATA_HOME` works. Already correct.
- Smart umbrella behaviour for `--db` alone or `--watch-path` alone. Single-purpose flags stay narrow — they don't imply each other.

---

## 3. Design — locked decisions

### 3.1 Resolution chain (per path)

For `WatchPath`:

```
1. --watch-path <P>           (explicit CLI flag, highest)
2. derive from --data-dir     (when --data-dir is set AND --watch-path is not)
3. config.yaml `watch_path:`  (file-level override)
4. <default_data_dir>/watch   (built-in fallback from defaultDataHome())
```

For `DBPath`: identical pattern, with `<data_dir>/queue.db`.

### 3.2 "Still at default" detection

We only re-derive when the user hasn't already set the value through `config.yaml` — explicit choices win over implicit derivation. Detection: compare current `cfg.WatchPath` / `cfg.DBPath` against the values `config.Default()` would produce. If they match → still default → re-derive. If they differ → user (or config) set them explicitly → respect their choice.

A tiny helper `isDefaultWatchPath(cfg, defaultCfg)` / `isDefaultDBPath(cfg, defaultCfg)` keeps the comparison readable.

### 3.3 Order of operations (in main.go)

The current order is: built-in defaults → config-file load → CLI flag overrides. Our derivation slots cleanly between config-load and flag-override:

```go
cfg := config.Default()
defaults := cfg                          // freeze defaults for later comparison
cfg = applyConfigFile(cfg, ...)
cfg = applyEnvToConfig(cfg)

// SW-AGENT-21: --data-dir umbrella derivation. Lands after config + env
// (so a config-file `data_dir:` is honoured) but before the explicit
// --data-dir / --watch-path / --db flag overrides (so those still win).
// We re-derive only when the path is still the *built-in default* —
// config.yaml entries are preserved.
if *dataDir != "" {
    cfg.DataDir = *dataDir
    if len(watchPaths) == 0 && len(rootDefs) == 0 && cfg.WatchPath == defaults.WatchPath {
        cfg.WatchPath = filepath.Join(*dataDir, "watch")
    }
    if *dbPath == "" && cfg.DBPath == defaults.DBPath {
        cfg.DBPath = filepath.Join(*dataDir, "queue.db")
    }
}
if *dbPath != "" { cfg.DBPath = *dbPath }                 // explicit wins
```

---

## 4. Acceptance criteria

1. `XDG_DATA_HOME=A sharedwatch --data-dir B init` prints `watch_path=B/watch`, `db_path=B/queue.db`, `data_dir=B`.
2. `XDG_DATA_HOME=A sharedwatch --data-dir B --watch-path /custom/place init` prints `watch_path=/custom/place`, `db_path=B/queue.db`, `data_dir=B` (explicit flag wins).
3. A `config.yaml` with `watch_path: /custom-yaml` passed via `--config` is **not** overridden by `--data-dir` (config-set values are not "default" anymore).
4. A bare `sharedwatch init` (no flags, no config) behaves exactly as before — `XDG_DATA_HOME` is the umbrella.
5. Dogfood scenario 21 passes end-to-end against the v0.0.8 binary using `--data-dir` (instead of `XDG_DATA_HOME`).
6. The `--data-dir` bullet is removed from `docs/agent/agent-system-prompt-20260522.md` "Known gotchas" and `Skills/sharedwatch-client/SKILL.md` startup ritual.
7. All existing tests still green; one new test in `cmd/sharedwatch/main_test.go` (or equivalent) covers the umbrella derivation.
8. CHANGELOG `[Unreleased]` block + man page `.TH` (`v0.0.8`) + embedded help-banner DEFAULT PATHS section updated.
9. Release v0.0.8 cut + pushed.

---

## 5. References

- [`SELF_IMPROVEMENT.md`](../SELF_IMPROVEMENT.md) — this ticket is the worked example of "Finding F2 (deeper design issue) → file a follow-up ticket → ship the fix".
- [`tickets/tasklist_20260524_053824.md`](./tasklist_20260524_053824.md) — SW-AGENT-20 worklog where F2 was recorded.
- [`docs/agent/agent-system-prompt-20260522.md`](../docs/agent/agent-system-prompt-20260522.md) — gotchas section to update.
- [`Skills/sharedwatch-client/SKILL.md`](../Skills/sharedwatch-client/SKILL.md) — startup ritual block to update.
- [`src/cmd/sharedwatch/main.go`](../src/cmd/sharedwatch/main.go) — the code change.
- [`GITOPS.md`](../GITOPS.md) §10 — release flow.

---

## 6. Definition of done

All 9 acceptance criteria green. `release/v0.0.8/` present; `release/LATEST` = `v0.0.8`; refs pushed. The public installer one-liner pulls v0.0.8; the umbrella derivation works as specified; the agent gotchas section shrinks by one bullet.
