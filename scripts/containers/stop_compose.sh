#!/usr/bin/env bash
set -uo pipefail

THIS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT=$(realpath -m "$THIS_DIR/../..")
CONTAINERS_DIR="${REPO_ROOT}/.containers"

PWD=$(pwd)

if ! docker &>/dev/null; then
  echo "[ERROR] Docker is not installed"
  exit 1
fi

if ! docker compose &>/dev/null; then
  echo "[ERROR] Docker Compose is not installed."
  exit 1
fi

cmd=(docker compose down)

function cleanup() {
  cd "$PWD"
}
trap cleanup EXIT

function usage() {
  echo ""
  echo "Usage: ${0} [OPTIONS]"
  echo ""
  echo "Options:"
  echo "  -h, --help   Print this help menu"
  echo ""
}

while [[ $# -gt 0 ]]; do
  case $1 in
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "[ERROR] Invalid arg: $1"
      usage
      exit 1
      ;;
  esac
done

cd "$CONTAINERS_DIR"

if command -v direnv &>/dev/null; then
  if [[ -f .envrc ]]; then
    direnv allow . || true
  fi
fi

echo ""
echo "Running command:"
echo "  ${cmd[*]}"
echo ""
"${cmd[@]}"
