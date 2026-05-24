# sharedwatch — `.sharedwatchignore` file discussion

**Date:** 2026-05-24
**Status:** discussion document (not a decision, not a ticket — yet)
**Author:** claude (this session)
**Companion to:** [`hooks-discussion-20260524.md`](./hooks-discussion-20260524.md), [`defaults-audit-20260524.md`](./defaults-audit-20260524.md) — same "settle the design in writing before any code" pattern.

This document exists to think through the question **"should sharedwatch support a `.gitignore`-style per-project ignore file?"** before any code is written.

---

## 1. The question

A user proposed: a `.sharedwatchignore` file in the watched folder root, where users edit ignore patterns directly — like `.gitignore` for git, `.dockerignore` for docker, `.eslintignore` for eslint, `.gcloudignore` for gcloud, `.prettierignore` for prettier. The convention is so well-established that devs reach for it instinctively.

Two sub-questions:

- **Does sharedwatch need it?** The product already has `ignore_patterns:` in `config.yaml` and `--ignore <pat>` as a flag. Why add a third surface?
- **If yes, what's the right shape?** Where does it live, what syntax does it accept, how do precedence + hot-reload + multi-root work?

---

## 2. What we have today

| Layer | Where | Strengths | Weaknesses |
|---|---|---|---|
| Built-in defaults | `internal/config/config.go` `Default()` | Covers `.git`, `.DS_Store`, `*.tmp`, `*.swp`, `node_modules`, `__pycache__`, `.cache`, `.venv`, `venv`, `target`, `dist`, `build`, `*.log` out of the box | Hard-coded; users can't add without editing source |
| `ignore_patterns:` in `config.yaml` | `~/.config/sharedwatch/config.yaml` or `./config.yaml` | Persistent, per-user or per-project | Lives *outside* the watched tree; users edit a separate file; not multi-root-aware (one global list) |
| `--ignore <pat>` flag | CLI | Per-invocation; great for one-offs | Has to be remembered every time; can't be checked into the repo |

What's missing: a **project-local, version-controllable, discoverable** ignore source. Right now if a developer clones a repo and runs sharedwatch on it, they have no way to tell sharedwatch "for this folder specifically, also ignore my IDE's caches" without editing a config file outside the folder.

`.sharedwatchignore` fills exactly that gap.

---

## 3. Why this is the right shape

Three reasons it's worth building:

1. **Convention.** Every tool that watches a filesystem hierarchy has converged on this pattern. The mental model is free for devs — no docs reading required to know `.sharedwatchignore` is "stuff I want sharedwatch to skip".

2. **Locality.** Lives next to the work it filters. When the project ages and accumulates new caches / build dirs / generated files, the ignore file ages with it — and gets reviewed in the same PR that introduces the new noise. Config-file-only ignores rot because they live somewhere the dev never looks.

3. **Multi-root naturalness.** A watcher with three labelled roots — `auth/`, `billing/`, `frontend/` — has three different noise profiles. The Python project ignores `__pycache__`; the Go project ignores `target/`; the TypeScript project ignores `.next/`. One global `ignore_patterns:` can't express this without union-ing every project's noise. A `.sharedwatchignore` per root keeps each project's ignore-shape local to it.

---

## 4. Design decisions (proposed — these would be locked at ticket time)

### 4.1 Precedence (additive, all layers union)

```
built-in defaults
  ∪ config.yaml `ignore_patterns:`
  ∪ .sharedwatchignore (per-root, if present at the root's directory)
  ∪ --ignore flag (CLI, one-shot)
```

All layers UNION. Ignores are additive — once any layer says "skip this", we skip. No negation across layers in v1.

### 4.2 Where the file lives

**Per-root, at the root's directory.** For each configured watch root:
- Single-root setup with `WatchPath=/work/auth` → look for `/work/auth/.sharedwatchignore`.
- Multi-root with `--root auth=/work/auth --root billing=/work/billing` → look for `/work/auth/.sharedwatchignore` AND `/work/billing/.sharedwatchignore`. Each applies to its own root only.

### 4.3 Syntax — a subset of gitignore

Match what devs already know. Support:

- Glob patterns with `*`, `**`, `?` (matching the same semantics as our existing `ignore_patterns` — path-segment-aware so `node_modules` excludes `node_modules/foo/bar`).
- `# comments` (whole-line).
- Blank lines (ignored).

**Do NOT support in v1:**

- `!negation` patterns. Adding "include this thing that would otherwise be ignored" is real complexity (ordering matters, debugging is hard) and we don't have a use case yet. Defer until a user asks.
- `/`-anchored patterns. Today our matcher is path-segment-based; adding anchored semantics is a separate design question. Defer.
- Trailing `/` directory-only markers. Our matcher already implies "directory or anything under it"; adding `/` semantics is fiddly. Defer.

If a user writes any of those, v1 ignores them silently (no error). When/if we add support, no migration is needed — existing files just start gaining the new semantics.

### 4.4 Hot-reload

Re-read each root's `.sharedwatchignore` on every watcher scan tick (5s in active, 10m in passive). Cost: one `os.Stat` + at most one short `os.ReadFile`. Cheap.

**Crucially: cache by file mtime.** Don't re-parse if mtime hasn't changed. Avoids reading the file every tick on a busy folder.

**Don't watch the ignore file via the watcher itself.** Feedback-loop-y. The scan-tick re-read is cleaner.

### 4.5 The ignore file ignores itself

Always treat `.sharedwatchignore` as ignored. Editing the file should NOT generate `file.modified` events on itself — that's just noise. Hard-coded in the default ignore pattern list.

### 4.6 Multi-root semantics

Each root reads its own `.sharedwatchignore` independently. A pattern in `/work/auth/.sharedwatchignore` does not affect events on `/work/billing/`.

### 4.7 Discoverability via `config show`

`sharedwatch config show` adds a section listing which `.sharedwatchignore` files were found and how many patterns each contributed:

```
SHAREDWATCHIGNORE FILES
  ✓ /work/auth/.sharedwatchignore       (12 patterns)
  ✓ /work/billing/.sharedwatchignore    (3 patterns)
  ✗ /work/frontend/.sharedwatchignore   (not present)
```

Closes the "why isn't my ignore working?" debug loop, same way the existing CONFIG FILES SEARCHED block closes "why isn't my config loading?".

---

## 5. Alternatives considered

### Alt 1 — extend `config.yaml` with a per-root ignore list

Add `ignore_patterns_by_root: { auth: [...], billing: [...] }`. Solves the multi-root noise problem without a new file. **Loses** on locality (still lives outside the watched folder), convention (no one expects per-root ignores in a YAML map), and version-control (the YAML lives in the user's home dir, not the project).

### Alt 2 — read `.gitignore` directly

Many noisy paths are already in `.gitignore`. If we just read `.gitignore` automatically, users get sensible defaults for free. **Loses** because (a) `.gitignore` syntax has the full surface area we're explicitly deferring (negation, anchored, dir-only), (b) `.gitignore` is what GIT should skip, which isn't always what a *watcher* should skip — e.g. `dist/` is gitignored but you might genuinely want sharedwatch to notice a deploy artifact changing. Conflating the two semantics is a mistake. Better to provide an explicit channel.

### Alt 3 — `.sharedwatchignore` AND read `.gitignore` as a fallback

Best of both? But: each layer added increases the "where did this ignore come from?" debug surface. v1 should ship the minimum that's useful (`.sharedwatchignore` only). If users ask, we can add `read_gitignore: true` as a config option later.

### Alt 4 — name it `.swignore` (shorter)

Saves typing the long form once per project. **Loses** because it's a non-standard abbreviation; devs would have to learn it. `.gcloudignore` and `.dockerignore` are both longer; `.sharedwatchignore` fits the convention.

---

## 6. Recommendation

**Build it. Ship as v0.0.9.** Scope is narrow, design is settled, value is real.

Implementation footprint (rough):

- New file `src/internal/catalog/sharedwatchignore.go` — parser + cache (~80 LOC).
- Integration in `internal/catalog/Ignored()` — accept an extra `[]string` of per-root patterns alongside the existing union. (~10 LOC change.)
- Per-root cache in `internal/watcher/service.go` — load + mtime-cache on scan tick. (~30 LOC.)
- Default ignore list adds `.sharedwatchignore` (so the file itself doesn't emit events). (~1 line.)
- `config show` block listing detected `.sharedwatchignore` files + their pattern counts. (~30 LOC.)
- Tests: parser unit tests (table-driven, ~15 cases); integration test that drops a `.sharedwatchignore` and confirms events stop firing. (~80 LOC.)
- Docs: CHANGELOG, man page (FILES section), top-level README, src/README "Configuration" block, agent-system-prompt mention.
- One new dogfood scenario (#25) in `docs/dogfood/test_dogfood.md` exercising the file end-to-end.

Estimate: **~half a day** for code + tests; another quarter for docs + dogfood + release.

---

## 7. Open questions (resolve at ticket time)

- **Q1.** What's the right place to look for **a fallback "user-global" `.sharedwatchignore`** outside any root? Probably `$XDG_CONFIG_HOME/sharedwatch/ignore` for "applies to every root I watch". Tentative answer: yes, ship in the same release; cheap to add.
- **Q2.** Should the parser warn (via a one-line stderr message) when it encounters a `!negation` or `/`-anchored pattern it's silently ignoring? Strong instinct: yes, once per file, at startup. Lets users discover the gap themselves rather than puzzle over why their negation isn't working.
- **Q3.** Should `config show` *also* enumerate the *effective* ignore patterns after the union? Useful for debugging but verbose. Maybe only when `--verbose` is added — defer.
- **Q4.** How does the parser handle whitespace, BOM, CRLF? Treat as garbage-in-still-works: trim whitespace, strip BOM, accept any line ending. Don't error on weird input.

---

## 8. Recommendation in one sentence

**File SW-AGENT-22; ship `.sharedwatchignore` in v0.0.9.** Narrow scope (subset of gitignore, per-root, hot-reload via scan tick, no negation in v1, ignore-itself by default), with `config show` integration so users can debug what's loaded. Estimated ~half a day. Closes the "project-local discoverable ignore" gap that every other filesystem tool has solved the same way.

---

## 9. References

- [`hooks-discussion-20260524.md`](./hooks-discussion-20260524.md) — same "settle the design before any code" pattern.
- [`defaults-audit-20260524.md`](./defaults-audit-20260524.md) — adjacent discussion about what's user-configurable in the tool.
- [`../../src/internal/catalog/ignore.go`](../../src/internal/catalog/ignore.go) — the existing `Ignored()` function that the new layer integrates with.
- [`../../src/internal/config/config.go`](../../src/internal/config/config.go) `Default()` — where the default ignore list lives.
- gitignore reference: `man gitignore` — the syntax subset to mimic.
- [`../../SELF_IMPROVEMENT.md`](../../SELF_IMPROVEMENT.md) — if a user reports surprise after shipping, scenario 25 is the regression.
