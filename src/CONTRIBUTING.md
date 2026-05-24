# Contributing

Thanks for taking the time. This is a small project with a deliberately narrow scope; contributions that hold to that scope are the easiest to merge.

## Ground rules
- **Queue-first stays queue-first.** Any change must preserve the "watcher writes only to the queue; consumer pulls digests" contract. Direct notification paths from watcher to consumer are out of scope.
- **Pull over push.** No mechanism that interrupts the consumer mid-task. Active mode is allowed; auto-triggered consumer wake-ups outside the configured cadence are not.
- **Durable by default.** Any state worth surviving a restart goes through the SQLite layer, not in-memory state.
- **One folder, one process.** No multi-host, no clustering, no networked queue.
- **Polling is fine.** fsnotify is welcome as an additive option behind a flag; not a replacement.

## Before you open a PR
Run locally:
```bash
make ci      # gofmt -l, go vet, go test ./...
```
And ideally:
```bash
make smoke   # builds the binary and exercises the README quickstart
```

If you change behavior visible to users (commands, flags, defaults, JSON schema), update:
- `README.md` if it's in the public Quickstart or command table
- `CHANGELOG.md` under `[Unreleased]`
- `docs/SCHEMA_CONTRACTS.md` if the DB schema changes
- `docs/APPLICATION_FLOW.md` or `docs/STATE_MODEL.md` if a state transition changes

## Style
- Go 1.22+. `gofmt -w .` before committing.
- Errors are returned, wrapped with `fmt.Errorf("context: %w", err)`. No `panic` in non-test code outside of `main`.
- New code paths get at least one unit test. Integration tests can use a `t.TempDir()` DB.
- No new dependencies without a clear reason. `modernc.org/sqlite` is the only required external import.
- Default to writing no comments. When a comment IS warranted, document *why*, not *what*.

## Versioning

This project uses **semantic versioning with a "branch + tag at the same name" release pattern**.

### Numbering — pre-1.0

While the project is `0.x`, expect rapid iteration. The increment levels:

| Bump | Example | When |
|---|---|---|
| **Patch** | `v0.0.2` → `v0.0.3` | Bug fix, doc tidy, internal refactor — no behaviour change for callers. |
| **Minor** | `v0.0.2` → `v0.1.0` | Additive feature, new flag, new payload field, schema migration — backwards-compatible. |
| **Major** | `v0.x.y` → `v1.0.0` | Backwards-incompatible: removed flag, renamed envelope, removed JSON field, structural change a consumer can see. |

Pre-1.0, **minor** is the working unit. `v0.0.2` ≠ "tiny change" — it means a noticeable release made of multiple commits, even though SemVer purists would call it a patch. Once the project reaches `v1.0.0`, normal SemVer semantics apply.

### Release pattern — branch *and* tag

When a release is cut, **two refs are created at the same commit**:

1. A **tag** with the version: `git tag -a v0.0.X -m "v0.0.X — <one-line summary>"` (immutable; the canonical release pointer).
2. A **branch** with the same name: `git branch v0.0.X <commit>` (mutable; lets you land patch commits on a prior release without disturbing `main`).

Git keeps tags and branches in separate namespaces, so `v0.0.X` works for both. Use whichever you want — checkout `git checkout v0.0.X` resolves to the branch if it exists, the tag otherwise.

### Promoting a feature branch to the new release

When a feature branch is ready to become the next release (this is how `v0.0.2` was cut from `feature/future`):

```bash
# 1. Pin the previous main tip as the prior version's branch + tag.
git branch v0.0.<prev> main
git tag -a v0.0.<prev> -m "v0.0.<prev> — <summary of what shipped before>"

# 2. Move main to the feature branch tip.
git checkout main
git merge --ff-only feature/<name>       # safe fast-forward when feature branched cleanly
# (If fast-forward fails, the branches have diverged; use a regular merge
# or rebase the feature branch onto main first.)

# 3. Tag + branch the new release.
git tag -a v0.0.<new> -m "v0.0.<new> — <summary of what just shipped>"
git branch v0.0.<new> main

# 4. (Optional) delete the feature branch now that it's merged.
git branch -d feature/<name>
```

This gives `main` = "the latest released state, always", with explicit historical anchors anyone can `git checkout v0.0.X` to see.

### Patch releases against an older line

If a serious bug surfaces in `v0.0.1` after `v0.0.2` has shipped:

```bash
git checkout v0.0.1                       # checks out the branch
# fix the bug, commit
git tag -a v0.0.1.1 -m "v0.0.1.1 — fix <bug>"   # patch tag on the v0.0.1 line
# v0.0.1 branch tip now points at the patch commit; tag v0.0.1 still points at original release
```

The `v0.0.1` *tag* never moves; the `v0.0.1` *branch* gains the patch commits. Consumers pinning to the tag get a stable artifact; consumers tracking the branch get the patched line.

### What to update at release time

When you bump the version, update in the same commit:

- `src/CHANGELOG.md` — close the `[Unreleased]` block and rename it to `[v0.0.X] — YYYY-MM-DD`.
- `manifest.yaml` — the `version:` field (or let `scripts/release.sh --version v0.0.X` populate the per-release copy in `dist/`).
- `src/cmd/sharedwatch/main.go` build via `-ldflags "-X main.Version=v0.0.X"` — `scripts/release.sh` and `scripts/rebuild.sh` handle this automatically when `VERSION=v0.0.X` is set.

## License
By contributing you agree your changes are licensed under the project's MIT license (see `LICENSE`).
