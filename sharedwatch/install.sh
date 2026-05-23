#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

if [ -f ./.env.build ]; then
  # shellcheck disable=SC1091
  . ./.env.build
fi

echo "[sharedwatch] formatting"
find . -name '*.go' -print0 | xargs -0 -r gofmt -w

echo "[sharedwatch] tidy"
go mod tidy

echo "[sharedwatch] test"
go test ./...

echo "[sharedwatch] build"
mkdir -p .bin
go build -o .bin/sharedwatch ./cmd/sharedwatch

echo "[sharedwatch] done"
