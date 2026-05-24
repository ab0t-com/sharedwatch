<p align="center">
  <img src="docs/brand/hero.png" alt="sharedwatch — calm, pull-based activity feed for a local shared folder" width="1200" height="675" style="max-width: 100%; height: auto;">
</p>

<!--
  Hero image lives at docs/brand/hero.png (16:9, ideal source: 1920x1080
  or larger; rendered to 1200x675 in the README). See
  docs/brand/image-prompts-20260524.md prompt #1 (v1, photographic) or
  docs/brand/image-prompts-v2-cute-20260524.md prompt #1 (v2, cute) for
  the generation brief. The shape-checker pipeline will pad/crop drift
  back to 16:9 — but the prompt is written to that ratio so composition
  arrives correct.
-->

# sharedwatch

> Calm, durable, pull-based activity feed for a local shared folder.

A single Go binary that watches a folder on disk, captures every change into a SQLite-backed queue, and lets you review the activity on your own cadence. MIT-licensed.

**Full program documentation lives in [`src/README.md`](src/README.md).** Free-form team docs (design discussions, reports, specs, dogfood scenarios) live under [`docs/`](docs/README.md). This file is just orientation for the repo layout.

## For AI agents (and humans) sharing a filesystem

sharedwatch is built for the case where multiple writers — humans in editors, AI agents in their own runtimes, build systems, sync daemons — share one folder and need to be aware of each other's work without polling or blocking.

**Hooks — every change carries attribution.** Stamp `--actor`, `--session`, `--task`, `--intent`, `--addressee`, `--ref`, `--tag` on the root invocation (every event the daemon emits this lifetime) or on a single `test emit`. The values land in `events.payload_json` v1 and survive into digests, `events list` rows, and SQL queries. Cross-actor edits on the same file produce *distinct* events — actor-aware coalesce means one agent's attribution never silently overwrites another's.

**Awareness — agents discover what changed since they last looked.** Each agent reads with a named cursor (`sharedwatch events list --cursor-name <me> --format jsonl`) that advances atomically on every read. No polling overhead, no missed events on restart, no shared coordination needed. For a quick L1 picture across all roots, `sharedwatch overview --format json` returns an aggregate with a `drill` map of pre-computed follow-up commands. For "what folders am I watching?", `sharedwatch roots`.

**Coordination — cooperative, advisory, never blocking.** `sharedwatch intent declare <path> --actor <me> --ttl 5m` tells peers "I'm working here". `sharedwatch lease acquire <path-glob> --actor <me> --ttl 30m` declares a stronger interest; when a *different* actor writes inside an active lease the watcher logs a structured warning (`event_actor`, `lease_actor`, `lease_path_glob`, `lease_expires_at`) so cooperative peers can back off. **The watcher never blocks the write** — your underlying filesystem semantics are untouched. Coordination is a social contract, not a kernel lock.

**Self-contained.** Single Go binary. SQLite is bundled inside (pure-Go [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) — no CGO, no system `libsqlite3`). No daemons it depends on. One process per data dir. Updates via `sharedwatch update --apply` (safe by default; atomic-rename install).

**Loadable skills for agents.** Two skill packages under [`Skills/`](Skills/) are designed to be loaded into an AI agent's context: `sharedwatch-client` (operational reference) and `sharedwatch-client-future` (v0.8+ features). The agent system prompt at [`docs/agent/agent-system-prompt-20260522.md`](docs/agent/agent-system-prompt-20260522.md) is a ready-to-use template.

See [`src/README.md`](src/README.md) for the full CLI surface, [`docs/design/multi-agent-discussion-20260522.md`](docs/design/multi-agent-discussion-20260522.md) for the design rationale, and [`docs/dogfood/test_dogfood.md`](docs/dogfood/test_dogfood.md) for 18 runnable end-to-end scenarios including the canonical multi-agent handoff.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ab0t-com/sharedwatch/main/scripts/install.sh | bash
```

Or clone and build locally:

```bash
git clone https://github.com/ab0t-com/sharedwatch
cd sharedwatch
./scripts/rebuild.sh          # fmt + vet + test + build into src/.bin/sharedwatch
```

The installer auto-detects local-dev mode (it sees `src/go.mod` + `scripts/rebuild.sh`) and will build from source instead of downloading a release.

Pre-commit / pre-push gitleaks hooks live in `.git/hooks/` (local-only, not version-controlled). If you re-clone, copy them over from a working clone or recreate them from the inline templates in [`.gitleaks.toml`](.gitleaks.toml).

## Repo layout

| Path | What's there |
|---|---|
| [`src/`](src/) | Go module — all source, tests, program-internal docs (`src/docs/`) |
| [`docs/`](docs/) | Free-form team docs: design, reports, specs, agent, dogfood. Start at [`docs/README.md`](docs/README.md). |
| [`scripts/`](scripts/) | `install.sh`, `rebuild.sh`, `release.sh` |
| [`html/`](html/) | Static landing page (GitHub Pages friendly) |
| [`tickets/`](tickets/) | Filed work tickets and session tasklists |
| [`Skills/`](Skills/) | Agent-skill packages (`sharedwatch-client`, `sharedwatch-client-future`, `sharedwatch-contributor`) |
| [`.gitleaks.toml`](.gitleaks.toml) | Secret-scan ruleset + project allowlist; consumed by `.git/hooks/{pre-commit,pre-push}` |
| [`manifest.yaml`](manifest.yaml) | Release-manifest template; `scripts/release.sh` writes the populated copy into `dist/` |
| `LICENSE` | MIT |
| `dist/` | (gitignored) release tarballs + populated manifest |

## Cutting a release

Releases ship in-repo under `release/vX.Y.Z/` and are served via `raw.githubusercontent.com` — no GitHub Releases UI, no `gh` CLI. Full rules in [`GITOPS.md`](GITOPS.md) §10.

```bash
./scripts/release.sh --version vX.Y.Z
mkdir -p release/vX.Y.Z
mv dist/sharedwatch_*.tar.gz dist/manifest.yaml release/vX.Y.Z/
echo "vX.Y.Z" > release/LATEST
git add release/vX.Y.Z release/LATEST
git commit -m "release: vX.Y.Z artifacts"
git tag -a vX.Y.Z HEAD -m "vX.Y.Z — <one-line summary>"
git branch vX.Y.Z HEAD
git push origin refs/heads/main refs/heads/vX.Y.Z refs/tags/vX.Y.Z
```

The installer (`scripts/install.sh`) and the in-binary `sharedwatch update` both read `release/LATEST`, fetch the tarball + `manifest.yaml`, verify SHA-256, then install. `release.sh` and `install.sh` are designed as a pair.

## Security

- All commits and pushes are scanned by [gitleaks](https://github.com/gitleaks/gitleaks) via local hooks at `.git/hooks/{pre-commit,pre-push}`.
- Hooks auto-discover [`.gitleaks.toml`](.gitleaks.toml) in the repo root, which extends the upstream default ruleset and allowlists project-known false positives (test fixtures, design-doc examples).
- Hooks live under `.git/hooks/` (not version-controlled); copy them from a working clone or `gitleaks --help` to recreate.

## License

MIT. See [`LICENSE`](LICENSE).
