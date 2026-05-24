#!/usr/bin/env bash
# sharedwatch installer.
#
# Two modes:
#   1. PUBLIC (default): download the latest release tarball from GitHub,
#      verify SHA-256 against the published manifest, install the binary
#      to $PREFIX/bin (default ~/.local/bin).
#
#   2. LOCAL DEV (auto-detected): if you're sitting inside a checkout of
#      the repo (i.e. ./src/go.mod and ./scripts/rebuild.sh exist), OR
#      SHAREDWATCH_DEV=1 is set, build from source via scripts/rebuild.sh
#      and symlink the binary into $PREFIX/bin.
#
# Safe defaults:
#   - No sudo. Installs under $HOME unless PREFIX is set to a system path
#     AND the caller has write access there.
#   - No silent overwrites: prompts before clobbering an existing binary
#     unless --yes is passed.
#   - Verifies the downloaded tarball's SHA-256 against the manifest.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ab0t-com/sharedwatch/main/scripts/install.sh | bash
#   # or:
#   ./scripts/install.sh [--prefix DIR] [--version vX.Y.Z] [--yes]
#
# Env:
#   SHAREDWATCH_DEV=1      force local-dev mode even outside the repo
#   SHAREDWATCH_REPO       override default repo (default: ab0t-com/sharedwatch)
#   PREFIX                 install prefix (default: $HOME/.local)

set -euo pipefail

REPO="${SHAREDWATCH_REPO:-ab0t-com/sharedwatch}"
PREFIX="${PREFIX:-$HOME/.local}"
VERSION=""
ASSUME_YES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)  PREFIX="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --yes|-y)  ASSUME_YES=1; shift ;;
    -h|--help)
      sed -n '2,30p' "$0"
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

log()  { printf '[sharedwatch/install] %s\n' "$*"; }
die()  { printf '[sharedwatch/install] ERROR: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

confirm() {
  # confirm "message" — returns 0 if yes, 1 if no.
  if [ "$ASSUME_YES" = "1" ]; then return 0; fi
  printf '%s [y/N] ' "$1"
  read -r reply </dev/tty || return 1
  case "$reply" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

detect_local_dev() {
  # Heuristic: we're in local-dev mode if SHAREDWATCH_DEV=1, or if the
  # script's parent directory contains src/go.mod and rebuild.sh.
  if [ "${SHAREDWATCH_DEV:-0}" = "1" ]; then return 0; fi
  local here repo_root
  here="$(cd "$(dirname "$0")" && pwd)"
  repo_root="$(dirname "$here")"
  if [ -f "$repo_root/src/go.mod" ] && [ -f "$here/rebuild.sh" ]; then
    return 0
  fi
  return 1
}

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "unsupported architecture: $arch" ;;
  esac
  case "$os" in
    linux|darwin) ;;
    *) die "unsupported OS: $os" ;;
  esac
  printf '%s_%s' "$os" "$arch"
}

install_from_source() {
  log "local-dev mode: building from src/"
  local here repo_root
  here="$(cd "$(dirname "$0")" && pwd)"
  repo_root="$(dirname "$here")"

  bash "$here/rebuild.sh"

  local bin="$repo_root/src/.bin/sharedwatch"
  [ -x "$bin" ] || die "expected built binary at $bin"

  mkdir -p "$PREFIX/bin"
  local dest="$PREFIX/bin/sharedwatch"
  if [ -e "$dest" ] && ! confirm "Overwrite existing $dest?"; then
    log "aborted; left $dest untouched"
    exit 0
  fi
  # Symlink so subsequent rebuilds are picked up without re-running install.
  ln -sf "$bin" "$dest"
  log "linked $dest -> $bin"
  log "ensure $PREFIX/bin is on your PATH"
}

install_from_release() {
  log "public mode: installing from github.com/$REPO release/ (raw.githubusercontent.com)"
  have curl   || die "curl is required"
  have tar    || die "tar is required"
  have sha256sum || have shasum || die "sha256sum (or shasum) is required"

  local platform; platform="$(detect_platform)"
  log "platform: $platform"

  # The release artifacts are committed under release/vX.Y.Z/ in the repo
  # and served via raw.githubusercontent.com. This sidesteps the GitHub
  # Releases publish step entirely — anything on `main` is immediately
  # installable. See GITOPS.md §10 for the policy.
  local raw_base="https://raw.githubusercontent.com/$REPO/main"

  # Resolve version. release/LATEST holds the version string (one line).
  if [ -z "$VERSION" ]; then
    log "resolving latest version from release/LATEST..."
    VERSION="$(curl -fsSL "$raw_base/release/LATEST" | tr -d '[:space:]')"
    [ -n "$VERSION" ] || die "could not read $raw_base/release/LATEST; pass --version vX.Y.Z"
  fi
  log "version: $VERSION"

  local base="$raw_base/release/$VERSION"
  local tarball="sharedwatch_${VERSION#v}_${platform}.tar.gz"
  local manifest="manifest.yaml"

  # Promote tmp to script scope (NOT `local`) so the EXIT trap below can
  # still see it when the trap fires after this function has returned —
  # under `set -u` an undefined $tmp aborts the cleanup with
  # "tmp: unbound variable".
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp:-}"' EXIT

  log "downloading $tarball"
  curl -fsSL --retry 3 -o "$tmp/$tarball" "$base/$tarball"

  log "downloading $manifest"
  curl -fsSL --retry 3 -o "$tmp/$manifest" "$base/$manifest"

  # Verify SHA-256. Manifest entries look like:
  #   - file: <name>
  #     sha256: <hex>
  #     size_bytes: <int>
  # We find the file line, then the next sha256 line in the same block.
  log "verifying SHA-256"
  local expected
  expected="$(awk -v fname="$tarball" '
    $1 == "-" && $2 == "file:" && $3 == fname { found=1; next }
    found && $1 == "sha256:" { print $2; exit }
  ' "$tmp/$manifest")"
  [ -n "$expected" ] || die "no SHA-256 entry for $tarball in $manifest"

  local actual
  if have sha256sum; then
    actual="$(sha256sum "$tmp/$tarball" | awk '{print $1}')"
  else
    actual="$(shasum -a 256 "$tmp/$tarball" | awk '{print $1}')"
  fi
  [ "$expected" = "$actual" ] || die "SHA-256 mismatch: expected $expected got $actual"
  log "SHA-256 OK"

  log "extracting"
  tar -xzf "$tmp/$tarball" -C "$tmp"

  local src_bin="$tmp/sharedwatch"
  [ -x "$src_bin" ] || die "tarball did not contain expected 'sharedwatch' binary"

  mkdir -p "$PREFIX/bin"
  local dest="$PREFIX/bin/sharedwatch"
  if [ -e "$dest" ] && ! confirm "Overwrite existing $dest?"; then
    log "aborted; left $dest untouched"
    exit 0
  fi
  install -m 0755 "$src_bin" "$dest"
  log "installed $dest"
  log "ensure $PREFIX/bin is on your PATH; then run: sharedwatch init && sharedwatch run"
}

main() {
  if detect_local_dev; then
    install_from_source
  else
    install_from_release
  fi
}

main "$@"
