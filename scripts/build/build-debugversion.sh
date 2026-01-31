#!/usr/bin/env bash
set -uo pipefail

if ! command -v go &>/dev/null; then
  echo "[ERROR] Go is not installed."
  exit 1
fi

echo "Build debug version of app to dist/archive-gmail"

mkdir -p dist
go build -o dist/archive-gmail-debug ./cmd/archive-gmail
