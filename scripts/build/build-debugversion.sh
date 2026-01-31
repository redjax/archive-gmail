#!/usr/bin/env bash
set -uo pipefail

if ! command -v go &>/dev/null; then
  echo "[ERROR] Go is not installed."
  exit 1
fi

echo "Build debug versions of app to dist/"

mkdir -p dist

## Main app debug
go build -o dist/archive-gmail-debug ./cmd/archive-gmail

## Auth CLI debug
go build -o dist/archive-gmail-auth-debug ./cmd/authenticate

echo "Built debug binaries for archive-gmail and archive-gmail-auth in dist/"
