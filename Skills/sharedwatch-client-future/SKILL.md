---
name: sharedwatch-client-future
description: Historical skill — every "future" feature it documented has shipped. **Use [`sharedwatch-client`](../sharedwatch-client/SKILL.md) instead.** Kept as a stub for back-compat with agents that loaded this name in v0.7-era prompts.
---

# sharedwatch-client-future — deprecated

**This skill is a stub as of sharedwatch v0.0.5.**

Everything this skill was designed to anticipate has shipped:

| Concept | Shipped in | Now in |
|---|---|---|
| Multi-root (`--root label=path` definitions + `--root label` filters) | v0.8.0 | [`sharedwatch-client/SKILL.md`](../sharedwatch-client/SKILL.md) + [`commands.md`](../sharedwatch-client/references/commands.md) |
| Progressive disclosure (`overview`, `events stats`) | v0.8.0 | same |
| Attribution flags (`--actor` / `--session` / `--task` / `--intent` / `--addressee` / `--ref` / `--tag`) | v0.8.0 | same |
| `actors` registry + `actor heartbeat` | v0.8.0 | same |
| `intents` + advisory `leases` | v0.8.0 | same |
| Hypermedia drill hints (now: unified `next[]` array, not `drill` map) | v0.0.4 | same |
| Self-updater (`update --apply`) | v0.0.3 | same |
| `roots` subcommand | v0.0.3 | same |
| `config show` + `stop` + env-var resolution (`SHAREDWATCH_*`) | v0.0.5 | same |

## What to do instead

Load **[`sharedwatch-client`](../sharedwatch-client/SKILL.md)**. It now covers the full surface — there is no "future" left to anticipate at this point in the v0.x timeline.

## Why keep this stub at all

Some prompts and orchestration configs reference this skill name from when it was the authoritative "v0.8+ features" doc. Removing the directory entirely would break those loaders. A deprecation stub costs nothing and gives them a clear pointer.

## When this stub might come back to life

If a future version of sharedwatch begins work on the next batch of cross-cutting features (the hooks system from [`../../docs/design/hooks-discussion-20260524.md`](../../docs/design/hooks-discussion-20260524.md), publisher adapters from [`../../docs/design/future-features-20260523.md`](../../docs/design/future-features-20260523.md) §13, content-diff storage from [`../../docs/design/content-storage-evaluation-20260523.md`](../../docs/design/content-storage-evaluation-20260523.md)), this directory is a reasonable home to stage that work — but only after it's been ticketed and design-locked.

## References

- [`../sharedwatch-client/SKILL.md`](../sharedwatch-client/SKILL.md) — the canonical client skill
- [`../sharedwatch-client/references/commands.md`](../sharedwatch-client/references/commands.md) — current CLI surface
- [`../../docs/agent/agent-system-prompt-20260522.md`](../../docs/agent/agent-system-prompt-20260522.md) — the system prompt template (refreshed for v0.0.5)
- [`../../src/CHANGELOG.md`](../../src/CHANGELOG.md) — release-by-release feature timeline
