# GITOPS.md

Rules for working with this repository's git history, branches, tags, hooks, and remotes. This is an instruction set, not a guide. When in doubt, follow the rules verbatim.

For *what* changes count as patch / minor / major and how to cut a release, see [`src/CONTRIBUTING.md`](src/CONTRIBUTING.md) §Versioning.

---

## 1. Branches

- **`main`** is the canonical branch. It always points at the latest released state.
- **`vX.Y.Z` branches** are immutable historical anchors at each release commit (see §3). Use for pinning, never for active work.
- **`feature/<short-name>` branches** are the only place active work happens.
  - Branch from `main` only.
  - One feature per branch.
  - Delete the branch (`git branch -d feature/<name>`) after it lands on `main`.
- **No personal namespaces** (`<username>/...`). Branch names describe the work, not the person.
- **No long-lived branches** other than `main` and the version branches. Anything older than 30 days that isn't merged is either landed or abandoned.

## 2. Commits

- Each commit is one logical change. If you can't describe it in one sentence, split it.
- Commit message format:
  - Line 1: imperative subject under 72 chars. `fix:`, `feat:`, `docs:`, `refactor:`, `test:`, `chore:` prefix optional but encouraged.
  - Blank line.
  - Body: *why*, not *what*. The diff shows what.
- Never commit:
  - Secrets, tokens, credentials, `.env*` (except `.env.example`).
  - Binary build artifacts (`src/.bin/`, `dist/`).
  - Local DB files (`*.db`, `*.db-wal`, `*.db-shm`).
  - OS / editor cruft (`.DS_Store`, `*.swp`, `.idea/`, `.vscode/`).
  - Anything `.gitignore` already excludes — if you have to `git add -f`, stop and ask why.
- Never amend a commit that has been pushed. Use a new commit instead.
- Never `--no-verify` to bypass hooks. If a hook blocks you, fix the cause.

## 3. Tags

- Tags are **annotated** (`git tag -a`), never lightweight. Annotation carries the message and tagger.
- Tag name format: `vX.Y.Z`. No prefixes, no suffixes (except `vX.Y.Z.W` for patch-on-patch — rare).
- Every release tag has a same-named branch pointing at the same commit (see §4).
- Always pass the commit explicitly: `git tag -a vX.Y.Z <commit-sha> -m "<message>"`. Never rely on HEAD.
- **Tags are immutable** once pushed. Don't move, don't delete-and-recreate. The single exception: a tag is wrong *and* less than one hour old *and* you have confirmed no other clone has fetched it.
- Push tags as a separate step from branches:
  ```bash
  git push origin --tags                          # all tags
  git push origin refs/tags/vX.Y.Z                # one tag
  ```

## 4. The "branch + tag at the same name" pattern

For each released version, two refs exist at the same commit:

- **Tag** `vX.Y.Z` — immutable canonical pointer.
- **Branch** `vX.Y.Z` — mutable; gains commits if a patch is needed on that line.

Git keeps the two in separate namespaces, but command-line tools see ambiguity. **Always disambiguate when pushing:**

```bash
git push origin refs/heads/vX.Y.Z       # the branch
git push origin refs/tags/vX.Y.Z        # the tag
```

A bare `git push origin vX.Y.Z` is forbidden.

## 5. Versioning

See [`src/CONTRIBUTING.md`](src/CONTRIBUTING.md) §Versioning for level definitions and the release flow.

One rule worth repeating here: cutting a release means **one commit gets both** the tag *and* the branch. They are created in the same change set and pushed together.

## 6. Pushing

- Default: `git push` (no force). If git refuses, **understand why before adding force**.
- For brand-new branches that the remote doesn't have yet: plain `git push -u origin <branch>` is correct.
- Force-push is allowed only:
  - To a branch you wholly own (e.g. your unmerged `feature/...`).
  - To a brand-new repo where you are the sole owner and no other clone exists.
  - To overwrite a remote auto-stub (e.g. GitHub-initialized README) on first push.
- **Prefer `--force-with-lease` over `--force`** — it refuses if the remote moved since your last fetch.
- **Never force-push `main`** once the repo has any other clone or contributor.
- **Never force-push tags** except under the one-hour rule in §3.

## 7. Pulling and integrating

- Default to **rebase, not merge**, when pulling:
  ```bash
  git config --global pull.rebase true     # one-time
  git pull                                  # now rebases
  ```
- Merge commits are reserved for landing a `feature/...` branch into `main`. Use `--no-ff` so the feature's history is visible:
  ```bash
  git checkout main
  git merge --no-ff feature/<name> -m "merge feature/<name>"
  ```
- If `main` can fast-forward (no divergence), `git merge --ff-only feature/<name>` is also fine and produces a linear history.

## 8. Hooks (gitleaks secret-scanning)

- `.git/hooks/pre-commit` and `.git/hooks/pre-push` run gitleaks. Both are mandatory.
- Local hooks live in `.git/hooks/` (not version-controlled). A fresh clone needs them re-installed manually (or recreated from the inline templates in `.gitleaks.toml`).
- `.gitleaks.toml` at the repo root carries the allowlist. Extend the allowlist before adding inline `# gitleaks:allow` comments.
- **Never** pass `--no-verify` to a `git commit` or `git push`. If a hook reports a leak:
  1. Verify it is genuine (read the redacted output).
  2. Rotate the credential immediately if real.
  3. Rewrite history to remove it (`git filter-repo` / interactive rebase) before re-attempting the push.
  4. If it is a false positive, add an entry to `.gitleaks.toml` `[allowlist]` and re-stage.
- If a hook itself is broken, fix the hook in a dedicated commit; back up `.git/hooks/<hook>.bak` first so a revert is one `mv` away.

## 9. Remotes

- `origin` always points at `git@github.com:ab0t-com/sharedwatch.git` (SSH) or `https://github.com/ab0t-com/sharedwatch.git` (HTTPS). Both are equivalent; pick based on auth setup.
- No additional named remotes unless documented in this file.
- Authentication:
  - HTTPS: use a Personal Access Token (PAT) with the `repo` scope. Paste it as the "password" when prompted. Optionally cache with `git config --global credential.helper store` (plain text on disk) or `'cache --timeout=86400'` (memory only).
  - SSH: add the key under GitHub → Settings → SSH and GPG keys.
  - **No password auth.** GitHub removed it in 2021.

## 10. Releases

**Releases live in the repo itself**, under `release/vX.Y.Z/`, and are served to installers via `raw.githubusercontent.com`. We do not use GitHub Releases (the publish-button workflow) and we do not use `gh` CLI. Anything on `main` is immediately installable; cutting a "release" means committing the artifacts.

```bash
# 1. Build cross-platform artifacts + manifest into dist/ (ephemeral staging).
./scripts/release.sh --version vX.Y.Z

# 2. Move the artifacts into the in-repo release/ directory.
mkdir -p release/vX.Y.Z
mv dist/sharedwatch_*.tar.gz dist/manifest.yaml release/vX.Y.Z/
echo "vX.Y.Z" > release/LATEST          # the version installers pick by default

# 3. Tag + branch + commit + push (per §3, §4, §6).
git add release/vX.Y.Z release/LATEST
git commit -m "release: vX.Y.Z artifacts"
git tag -a vX.Y.Z HEAD -m "vX.Y.Z — <one-line summary>"
git branch vX.Y.Z HEAD
git push origin refs/heads/main refs/heads/vX.Y.Z refs/tags/vX.Y.Z
```

That's the whole release. No web UI, no `gh`, no separate publish step.

Why in-repo: removes the publish step entirely, keeps the install one-liner working off `main` automatically, and makes `release/manifest.yaml` reviewable in normal PRs. Trade-off: the repo grows by ~16 MB per release, and clones get slower forever. Acceptable for a small Go binary; would not be acceptable for larger artifacts. If the repo ever grows past ~500 MB total, revisit and migrate to GitHub Releases.

The per-version `release/vX.Y.Z/manifest.yaml` is consumed by `scripts/install.sh` (for fresh installs) and the in-binary `sharedwatch update --apply` (for upgrades). Both verify SHA-256 from the manifest. **Do not edit a published manifest** — cut a new release if a correction is needed.

## 11. Repo settings (GitHub UI)

The following are enforced in GitHub settings, not in repo files:

- **Default branch:** `main`.
- **Branch protection on `main`:**
  - Require pull request before merge: ON (when collaborators exist; OFF if solo).
  - Require status checks to pass: ON if any CI is configured.
  - Restrict pushes that create matching refs: leave OFF (so `vX.Y.Z` tag/branch pushes still work).
- **Visibility:** public.
- **License:** MIT (file at repo root).
- **Security alerts:** ON (Dependabot, secret scanning).

## 12. Quick recipes

**Land a feature**
```bash
git checkout -b feature/foo main
# work, commit, repeat
git checkout main
git pull --rebase
git merge --no-ff feature/foo -m "merge feature/foo"
git push origin main
git branch -d feature/foo
```

**Promote `main` to a new release version**
```bash
# bump CHANGELOG, manifest.yaml, commit on main first
git tag -a vX.Y.Z HEAD -m "vX.Y.Z — <summary>"
git branch vX.Y.Z HEAD
git push origin refs/heads/main refs/heads/vX.Y.Z refs/tags/vX.Y.Z
```

**Patch an older version**
```bash
git checkout vX.Y.Z          # branch (mutable), not the tag
# fix, commit
git tag -a vX.Y.Z.1 HEAD -m "vX.Y.Z.1 — patch <bug>"
git push origin refs/heads/vX.Y.Z refs/tags/vX.Y.Z.1
```

**Move a tag less than an hour old to the correct commit (one-hour rule, §3)**
```bash
git tag -d vX.Y.Z
git tag -a vX.Y.Z <correct-sha> -m "vX.Y.Z — <message>"
git push --force origin refs/tags/vX.Y.Z
```

**Inspect what GitHub actually has**
```bash
git ls-remote origin                    # all refs
git ls-remote origin 'refs/tags/v*'     # just version tags
git ls-remote origin refs/heads/main    # just main's tip
```
