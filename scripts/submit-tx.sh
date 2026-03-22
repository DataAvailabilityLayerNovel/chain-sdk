#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHAIN_LOG_FILE="${CHAIN_LOG_FILE:-$ROOT_DIR/.logs/cosmos-chain.log}"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

CONTAINER_NAME="${COSMOS_WASM_SEQ_CONTAINER:-${COSMOS_WASM_CONTAINER:-cosmos-wasm-sequencer}}"
HOME_DIR="${COSMOS_WASM_HOME:-/wasmd}"
CHAIN_ID="${CHAIN_ID:-localwasm}"
SEQ_RPC_PORT="${COSMOS_WASM_SEQ_RPC_PORT:-26657}"
NODE_INTERNAL="${NODE_INTERNAL:-${NODE:-http://127.0.0.1:26657}}"
NODE_EXTERNAL="${NODE_EXTERNAL:-http://127.0.0.1:${SEQ_RPC_PORT}}"
FROM_KEY="${FROM_KEY:-validator}"
KEYRING_BACKEND="${KEYRING_BACKEND:-test}"
GAS="${GAS:-auto}"
GAS_ADJUSTMENT="${GAS_ADJUSTMENT:-1.4}"
FEES="${FEES:-5000stake}"
BROADCAST_MODE="${BROADCAST_MODE:-sync}"

DEPLOY_OUTPUT_FILE="${DEPLOY_OUTPUT_FILE:-/tmp/ev-node-wasm/last-deploy.env}"

mkdir -p "$(dirname "$CHAIN_LOG_FILE")"

log() {
  local msg="$1"
  echo "$msg" | tee -a "$CHAIN_LOG_FILE"
}

if [[ -f "$DEPLOY_OUTPUT_FILE" ]]; then
  set -a
  source "$DEPLOY_OUTPUT_FILE"
  set +a
fi

CONTRACT_ADDR="${CONTRACT_ADDR:-}"
RECIPIENT="${RECIPIENT:-}"
AMOUNT="${AMOUNT:-1}"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd"
    exit 1
  fi
}

require_cmd docker
require_cmd jq

if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
  log "[err][wasm] chain container is not running: $CONTAINER_NAME"
  log "[hint][wasm] run: ./scripts/deploy-sample-contract.sh"
  exit 1
fi

run_wasmd() {
  docker exec -i "$CONTAINER_NAME" wasmd "$@" --home "$HOME_DIR"
}

get_height() {
  local h=""

  h="$(curl -fsS --connect-timeout 2 --max-time 3 "$NODE_EXTERNAL/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height // empty' 2>/dev/null || true)"
  if [[ -n "$h" ]]; then
    echo "$h"
    return 0
  fi

  h="$(run_wasmd status --node "$NODE_INTERNAL" 2>/dev/null | jq -r '.SyncInfo.latest_block_height // .result.sync_info.latest_block_height // empty' 2>/dev/null || true)"
  if [[ -n "$h" ]]; then
    echo "$h"
  fi
}

if [[ -z "$CONTRACT_ADDR" ]]; then
  log "[err][wasm] CONTRACT_ADDR is empty"
  log "[hint][wasm] set CONTRACT_ADDR=... or deploy first: ./scripts/deploy-sample-contract.sh"
  exit 1
fi

if [[ -z "$RECIPIENT" ]]; then
  RECIPIENT="$(run_wasmd keys show "$FROM_KEY" -a --keyring-backend "$KEYRING_BACKEND")"
fi

EXEC_MSG="{\"transfer\":{\"recipient\":\"$RECIPIENT\",\"amount\":\"$AMOUNT\"}}"
QUERY_MSG="{\"balance\":{\"address\":\"$RECIPIENT\"}}"

HEIGHT_BEFORE="$(get_height)"
log "[run][wasm] submitting tx to contract"
log "[block][wasm] height_before_tx=${HEIGHT_BEFORE:-unknown}"

TX_JSON="$(run_wasmd tx wasm execute "$CONTRACT_ADDR" "$EXEC_MSG" \
  --from "$FROM_KEY" \
  --keyring-backend "$KEYRING_BACKEND" \
  --chain-id "$CHAIN_ID" \
  --node "$NODE_INTERNAL" \
  --broadcast-mode "$BROADCAST_MODE" \
  --gas "$GAS" \
  --gas-adjustment "$GAS_ADJUSTMENT" \
  --fees "$FEES" \
  --yes \
  --output json 2>&1 || true)"

TXHASH="$(echo "$TX_JSON" | jq -r '.txhash // empty' 2>/dev/null || true)"
if [[ -z "$TXHASH" ]]; then
  TXHASH="$(echo "$TX_JSON" | sed -n '/^{/,$p' | jq -r '.txhash // empty' 2>/dev/null || true)"
fi

if [[ -z "$TXHASH" ]]; then
  log "[err][wasm] submit tx failed"
  echo "$TX_JSON" | tee -a "$CHAIN_LOG_FILE"
  exit 1
fi

log "[ok][wasm] txhash=$TXHASH"
sleep 1
HEIGHT_AFTER="$(get_height)"
log "[block][wasm] height_after_tx=${HEIGHT_AFTER:-unknown}"

log "[run][wasm] querying recipient balance"
QUERY_RESULT="$(run_wasmd query wasm contract-state smart "$CONTRACT_ADDR" "$QUERY_MSG" --node "$NODE_INTERNAL" --output json)"

log "[ok][wasm] balance_result=$QUERY_RESULT"
log "[done][wasm] tx submitted"
