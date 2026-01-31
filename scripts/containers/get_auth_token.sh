#!/usr/bin/env bash
set -eo pipefail

THIS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT=$(realpath -m "$THIS_DIR/../..")
CONTAINERS_DIR="${REPO_ROOT}/.containers"
TOKEN_DIR="${CONTAINERS_DIR}/token"

mkdir -p "$TOKEN_DIR"

## Ensure .env is loaded
if [[ -f "${CONTAINERS_DIR}/.env" ]]; then
  export $(grep -v '^#' "${CONTAINERS_DIR}/.env" | xargs)
fi

## Override token file for authenticate container
export OAUTH2_TOKEN_FILE="./token/token.json"

## Build the authenticate image
docker build -t archive-gmail-auth \
  -f "${CONTAINERS_DIR}/auth.Dockerfile" "$REPO_ROOT"

## Run authenticate container interactively
docker run --rm -it \
  -v "$TOKEN_DIR:/token" \
  --env OAUTH2_TOKEN_FILE="/token/token.json" \
  --env GMAIL_CLIENT_ID \
  --env GMAIL_CLIENT_SECRET \
  --env GMAIL_EMAIL \
  archive-gmail-auth
