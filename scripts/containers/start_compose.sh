#!/usr/bin/env bash
set -uo pipefail

THIS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT=$(realpath -m "$THIS_DIR/../..")
CONTAINERS_DIR="${REPO_ROOT}/.containers"
BUILD=false
LOG=false

PWD=$(pwd)

if ! docker &>/dev/null; then
  echo "[ERROR] Docker is not installed"
  exit 1
fi

if ! docker compose &>/dev/null; then
  echo "[ERROR] Docker Compose is not installed."
  exit 1
fi

cmd=(docker compose up -d)
log_cmd=(docker compose logs -f archive-gmail)

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
  echo "  -b, --build  Build before running"
  echo "  -l, --log    Show container logs after startup"
  echo ""
}

while [[ $# -gt 0 ]]; do
  case $1 in
    -b|--build)
      BUILD=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -l|--log)
      LOG=true
      shift
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

ls -la .

if [[ "$BUILD" == "true" ]]; then
  cmd+=(--build)
fi

echo ""
echo "Running command:"
echo "  ${cmd[*]}"
echo ""
"${cmd[@]}"

if [[ "$LOG" == "true" ]]; then
  echo ""
  echo "Showing container logs. Command:"
  echo "  ${log_cmd[*]}"
  echo ""
  "${log_cmd[@]}"
fi
