#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

WASM_NETWORK="${COSMOS_WASM_NETWORK:-cosmos-wasm-net}"
WASM_SEQ_CONTAINER="${COSMOS_WASM_SEQ_CONTAINER:-cosmos-wasm-sequencer}"
WASM_FULL_CONTAINER="${COSMOS_WASM_FULL_CONTAINER:-cosmos-wasm-fullnode}"
WASM_WORK_DIR="${COSMOS_WASM_WORK_DIR:-/tmp/ev-node-wasm-fullnode}"
CLEAN_WORKDIR="${CLEAN_WORKDIR:-false}"
CHAIN_LOG_FILE="${CHAIN_LOG_FILE:-$ROOT_DIR/.logs/cosmos-chain.log}"

mkdir -p "$(dirname "$CHAIN_LOG_FILE")"

log() {
  local msg="$1"
  echo "$msg" | tee -a "$CHAIN_LOG_FILE"
}

remove_container() {
  local name="$1"
  if docker ps -a --format '{{.Names}}' | grep -Fxq "$name"; then
    log "[run][wasm-full] removing container: $name"
    docker rm -f "$name" >/dev/null 2>&1 || true
    log "[ok][wasm-full] removed container: $name"
  else
    log "[ok][wasm-full] container not found: $name"
  fi
}

remove_network() {
  local net="$1"
  if docker network inspect "$net" >/dev/null 2>&1; then
    log "[run][wasm-full] removing network: $net"
    docker network rm "$net" >/dev/null 2>&1 || true
    log "[ok][wasm-full] removed network: $net"
  else
    log "[ok][wasm-full] network not found: $net"
  fi
}

log "[run][wasm-full] stopping wasm full-node stack"
remove_container "$WASM_FULL_CONTAINER"
remove_container "$WASM_SEQ_CONTAINER"
remove_network "$WASM_NETWORK"

if [[ "$CLEAN_WORKDIR" == "true" ]]; then
  log "[run][wasm-full] removing work dir: $WASM_WORK_DIR"
  rm -rf "$WASM_WORK_DIR"
  log "[ok][wasm-full] removed work dir: $WASM_WORK_DIR"
fi

log "[done][wasm-full] stop completed"
