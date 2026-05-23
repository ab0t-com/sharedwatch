#!/usr/bin/env bash
# Fast local rebuild: fmt + vet + test + build into src/.bin/.
# Mirrors what install.sh used to do, but lives at repo top-level under scripts/.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(dirname "$HERE")"
SRC="$REPO_ROOT/src"

[ -f "$SRC/go.mod" ] || { echo "[rebuild] no go.mod at $SRC" >&2; exit 1; }

cd "$SRC"

if [ -f ./.env.build ]; then
  # shellcheck disable=SC1091
  . ./.env.build
fi

echo "[rebuild] gofmt"
find . -name '*.go' -print0 | xargs -0 -r gofmt -w

echo "[rebuild] vet"
go vet ./...

echo "[rebuild] tidy"
go mod tidy

echo "[rebuild] test"
go test ./... -count=1

echo "[rebuild] build"
mkdir -p .bin
VERSION="${VERSION:-dev}"
go build -ldflags "-X main.Version=$VERSION" -o .bin/sharedwatch ./cmd/sharedwatch

echo "[rebuild] done: $SRC/.bin/sharedwatch (Version=$VERSION)"
