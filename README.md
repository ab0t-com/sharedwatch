# sharedwatch

> Calm, durable, pull-based activity feed for a local shared folder.

A single Go binary that watches a folder on disk, captures every change into a SQLite-backed queue, and lets you review the activity on your own cadence. MIT-licensed.

**Full program documentation lives in [`src/README.md`](src/README.md).** Free-form team docs (design discussions, reports, specs, dogfood scenarios) live under [`docs/`](docs/README.md). This file is just orientation for the repo layout.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ab0t/sharedwatch/main/scripts/install.sh | bash
```

Or clone and build locally:

```bash
git clone https://github.com/ab0t/sharedwatch
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

```bash
./scripts/release.sh --version v0.8.0
# -> dist/sharedwatch_0.8.0_{linux,darwin}_{amd64,arm64}.tar.gz
# -> dist/manifest.yaml (with SHA-256 of each tarball)
# then (you, not the script):
git tag -a v0.8.0 -m "release v0.8.0"
git push origin v0.8.0
gh release create v0.8.0 dist/*.tar.gz dist/manifest.yaml \
  --title "v0.8.0" --notes-file src/CHANGELOG.md
```

The installer downloads `manifest.yaml` alongside the tarball and verifies the SHA-256 before installing — `release.sh` and `install.sh` are designed as a pair.

## Security

- All commits and pushes are scanned by [gitleaks](https://github.com/gitleaks/gitleaks) via local hooks at `.git/hooks/{pre-commit,pre-push}`.
- Hooks auto-discover [`.gitleaks.toml`](.gitleaks.toml) in the repo root, which extends the upstream default ruleset and allowlists project-known false positives (test fixtures, design-doc examples).
- Hooks live under `.git/hooks/` (not version-controlled); copy them from a working clone or `gitleaks --help` to recreate.

## License

MIT. See [`LICENSE`](LICENSE).
