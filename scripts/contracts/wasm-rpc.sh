#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

EVNODE_RPC_URL="${EVNODE_RPC_URL:-${WASM_RPC_URL:-${NODE:-http://127.0.0.1:38331}}}"
export EVNODE_RPC_URL

usage() {
  cat <<'EOF'
Usage:
  ./scripts/contracts/wasm-rpc.sh status
  ./scripts/contracts/wasm-rpc.sh latest-block
  ./scripts/contracts/wasm-rpc.sh block --height <n>
  ./scripts/contracts/wasm-rpc.sh tx --hash <HEX_HASH> [--scan-depth 300]
  ./scripts/contracts/wasm-rpc.sh txs --height <n>

Examples:
  ./scripts/contracts/wasm-rpc.sh latest-block
  ./scripts/contracts/wasm-rpc.sh block --height 100
  ./scripts/contracts/wasm-rpc.sh tx --hash C1AEC991E34C280429DE751ED7DDBBC202EF0C07AEE97BC3D1563FC1CCE12607
  ./scripts/contracts/wasm-rpc.sh txs --height 100

Env:
  EVNODE_RPC_URL or WASM_RPC_URL or NODE (default: http://127.0.0.1:38331)
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd"
    exit 1
  fi
}

fetch_json() {
  go run ./tools/evnode-rpc "$@"
}

cmd_status() {
  fetch_json status
}

cmd_latest_block() {
  fetch_json latest-block
}

cmd_block() {
  local height=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --height)
        height="${2:-}"
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        echo "[err] unknown argument: $1"
        usage
        exit 1
        ;;
    esac
  done

  if [[ -z "$height" ]]; then
    echo "[err] --height is required"
    exit 1
  fi

  fetch_json block --height "$height"
}

cmd_tx() {
  local hash=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --hash)
        hash="${2:-}"
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        echo "[err] unknown argument: $1"
        usage
        exit 1
        ;;
    esac
  done

  if [[ -z "$hash" ]]; then
    echo "[err] --hash is required"
    exit 1
  fi

  fetch_json tx --hash "$hash"
}

cmd_txs() {
  local height=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --height)
        height="${2:-}"
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        echo "[err] unknown argument: $1"
        usage
        exit 1
        ;;
    esac
  done

  if [[ -z "$height" ]]; then
    echo "[err] --height is required"
    exit 1
  fi

  fetch_json txs --height "$height"
}

main() {
  require_cmd go

  if [[ $# -lt 1 ]]; then
    usage
    exit 1
  fi

  local cmd="$1"
  shift

  case "$cmd" in
    status)
      cmd_status "$@"
      ;;
    latest-block)
      cmd_latest_block "$@"
      ;;
    block)
      cmd_block "$@"
      ;;
    tx)
      cmd_tx "$@"
      ;;
    txs)
      cmd_txs "$@"
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      echo "[err] unknown command: $cmd"
      usage
      exit 1
      ;;
  esac
}

main "$@"
