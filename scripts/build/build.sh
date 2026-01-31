#!/usr/bin/env bash
set -uo pipefail

if ! command -v go &>/dev/null; then
  echo "[ERROR] Go is not installed."
  exit 1
fi

echo "Build trimmed version of app to dist/archive-gmail"

mkdir -p dist
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/archive-gmail ./cmd/archive-gmail
