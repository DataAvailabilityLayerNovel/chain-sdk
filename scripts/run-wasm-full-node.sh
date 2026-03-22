#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

RUNTIME_COSMOS_WASM_SEQ_RPC_PORT="${COSMOS_WASM_SEQ_RPC_PORT:-}"
RUNTIME_COSMOS_WASM_SEQ_P2P_PORT="${COSMOS_WASM_SEQ_P2P_PORT:-}"
RUNTIME_COSMOS_WASM_FULL_RPC_PORT="${COSMOS_WASM_FULL_RPC_PORT:-}"
RUNTIME_COSMOS_WASM_FULL_P2P_PORT="${COSMOS_WASM_FULL_P2P_PORT:-}"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

if [[ -n "$RUNTIME_COSMOS_WASM_SEQ_RPC_PORT" ]]; then
  COSMOS_WASM_SEQ_RPC_PORT="$RUNTIME_COSMOS_WASM_SEQ_RPC_PORT"
fi
if [[ -n "$RUNTIME_COSMOS_WASM_SEQ_P2P_PORT" ]]; then
  COSMOS_WASM_SEQ_P2P_PORT="$RUNTIME_COSMOS_WASM_SEQ_P2P_PORT"
fi
if [[ -n "$RUNTIME_COSMOS_WASM_FULL_RPC_PORT" ]]; then
  COSMOS_WASM_FULL_RPC_PORT="$RUNTIME_COSMOS_WASM_FULL_RPC_PORT"
fi
if [[ -n "$RUNTIME_COSMOS_WASM_FULL_P2P_PORT" ]]; then
  COSMOS_WASM_FULL_P2P_PORT="$RUNTIME_COSMOS_WASM_FULL_P2P_PORT"
fi

WASM_IMAGE="${COSMOS_WASM_IMAGE:-cosmwasm/wasmd:v0.45.0}"
WASM_PLATFORM="${COSMOS_WASM_PLATFORM:-linux/amd64}"
WASM_CHAIN_ID="${COSMOS_WASM_CHAIN_ID:-localwasm}"
WASM_DENOM="${COSMOS_WASM_DENOM:-stake}"
WASM_KEY_NAME="${COSMOS_WASM_KEY_NAME:-validator}"
WASM_KEYRING="${COSMOS_WASM_KEYRING:-test}"
WASM_WORK_DIR="${COSMOS_WASM_WORK_DIR:-/tmp/ev-node-wasm-fullnode}"
WASM_NETWORK="${COSMOS_WASM_NETWORK:-cosmos-wasm-net}"
WASM_SEQ_CONTAINER="${COSMOS_WASM_SEQ_CONTAINER:-cosmos-wasm-sequencer}"
WASM_FULL_CONTAINER="${COSMOS_WASM_FULL_CONTAINER:-cosmos-wasm-fullnode}"
WASM_SEQ_RPC_PORT="${COSMOS_WASM_SEQ_RPC_PORT:-26657}"
WASM_FULL_RPC_PORT="${COSMOS_WASM_FULL_RPC_PORT:-27657}"
WASM_SEQ_P2P_PORT="${COSMOS_WASM_SEQ_P2P_PORT:-26656}"
WASM_FULL_P2P_PORT="${COSMOS_WASM_FULL_P2P_PORT:-27656}"

SEQ_HOME="$WASM_WORK_DIR/sequencer"
FULL_HOME="$WASM_WORK_DIR/fullnode"
CHAIN_LOG_FILE="${CHAIN_LOG_FILE:-$ROOT_DIR/.logs/cosmos-chain.log}"

mkdir -p "$SEQ_HOME" "$FULL_HOME" "$(dirname "$CHAIN_LOG_FILE")"

log() {
  local msg="$1"
  echo "$msg" | tee -a "$CHAIN_LOG_FILE"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log "[err][wasm-full] missing command: $cmd"
    exit 1
  fi
}

require_cmd docker
require_cmd curl
require_cmd jq

run_wasmd_init() {
  local home="$1"
  local moniker="$2"

  docker run --rm --platform "$WASM_PLATFORM" \
    -v "$home:/data" \
    "$WASM_IMAGE" /bin/sh -lc "
      set -e
      if [ ! -f /data/config/genesis.json ]; then
        wasmd init '$moniker' --chain-id '$WASM_CHAIN_ID' --home /data >/dev/null 2>&1
        wasmd config keyring-backend '$WASM_KEYRING' --home /data >/dev/null 2>&1
        wasmd config chain-id '$WASM_CHAIN_ID' --home /data >/dev/null 2>&1
      fi
    "
}

ensure_network() {
  if ! docker network inspect "$WASM_NETWORK" >/dev/null 2>&1; then
    log "[run][wasm-full] creating network: $WASM_NETWORK"
    docker network create "$WASM_NETWORK" >/dev/null
  fi
}

ensure_sequencer_genesis() {
  if [[ -f "$SEQ_HOME/config/genesis.json" ]] && [[ -f "$SEQ_HOME/config/priv_validator_key.json" ]]; then
    return
  fi

  run_wasmd_init "$SEQ_HOME" "wasm-sequencer"

  docker run --rm --platform "$WASM_PLATFORM" \
    -v "$SEQ_HOME:/data" \
    "$WASM_IMAGE" /bin/sh -lc "
      set -e
      echo 'test test test test test test test test test test test junk' | wasmd keys add '$WASM_KEY_NAME' --recover --keyring-backend '$WASM_KEYRING' --home /data >/dev/null 2>&1 || true
      ADDR=\$(wasmd keys show '$WASM_KEY_NAME' -a --keyring-backend '$WASM_KEYRING' --home /data)
      wasmd genesis add-genesis-account \"\$ADDR\" 100000000000'$WASM_DENOM' --home /data >/dev/null 2>&1
      wasmd genesis gentx '$WASM_KEY_NAME' 1000000'$WASM_DENOM' --chain-id '$WASM_CHAIN_ID' --keyring-backend '$WASM_KEYRING' --home /data >/dev/null 2>&1
      wasmd genesis collect-gentxs --home /data >/dev/null 2>&1
    "
}

ensure_fullnode_genesis() {
  run_wasmd_init "$FULL_HOME" "wasm-fullnode"
  cp "$SEQ_HOME/config/genesis.json" "$FULL_HOME/config/genesis.json"
}

container_running() {
  local name="$1"
  docker ps --format '{{.Names}}' | grep -Fxq "$name"
}

start_sequencer() {
  if container_running "$WASM_SEQ_CONTAINER"; then
    log "[ok][wasm-full] sequencer already running: $WASM_SEQ_CONTAINER"
    return
  fi

  docker rm -f "$WASM_SEQ_CONTAINER" >/dev/null 2>&1 || true

  log "[run][wasm-full] starting sequencer container: $WASM_SEQ_CONTAINER"
  docker run -d --name "$WASM_SEQ_CONTAINER" --platform "$WASM_PLATFORM" \
    --network "$WASM_NETWORK" \
    -p "$WASM_SEQ_RPC_PORT:26657" \
    -p "$WASM_SEQ_P2P_PORT:26656" \
    -v "$SEQ_HOME:/data" \
    "$WASM_IMAGE" /bin/sh -lc "
      wasmd start --home /data \
        --rpc.laddr tcp://0.0.0.0:26657 \
        --p2p.laddr tcp://0.0.0.0:26656 \
        --api.enable true \
        --grpc.enable true
    " >/dev/null
}

wait_rpc() {
  local rpc_url="$1"
  local retries="${2:-30}"

  for _ in $(seq 1 "$retries"); do
    if curl -fsS "$rpc_url/status" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  return 1
}

start_fullnode() {
  if container_running "$WASM_FULL_CONTAINER"; then
    log "[ok][wasm-full] full node already running: $WASM_FULL_CONTAINER"
    return
  fi

  docker rm -f "$WASM_FULL_CONTAINER" >/dev/null 2>&1 || true

  local node_id
  node_id="$(docker run --rm --platform "$WASM_PLATFORM" -v "$SEQ_HOME:/data" "$WASM_IMAGE" /bin/sh -lc "wasmd tendermint show-node-id --home /data")"

  log "[run][wasm-full] starting full node container: $WASM_FULL_CONTAINER"
  docker run -d --name "$WASM_FULL_CONTAINER" --platform "$WASM_PLATFORM" \
    --network "$WASM_NETWORK" \
    -p "$WASM_FULL_RPC_PORT:26657" \
    -p "$WASM_FULL_P2P_PORT:26656" \
    -v "$FULL_HOME:/data" \
    "$WASM_IMAGE" /bin/sh -lc "
      wasmd start --home /data \
        --rpc.laddr tcp://0.0.0.0:26657 \
        --p2p.laddr tcp://0.0.0.0:26656 \
        --p2p.persistent_peers ${node_id}@${WASM_SEQ_CONTAINER}:26656 \
        --api.enable true \
        --grpc.enable true
    " >/dev/null
}

log "[run][wasm-full] image=$WASM_IMAGE"
log "[run][wasm-full] chain_id=$WASM_CHAIN_ID denom=$WASM_DENOM"
log "[run][wasm-full] work_dir=$WASM_WORK_DIR"

ensure_network
ensure_sequencer_genesis
start_sequencer

if ! wait_rpc "http://127.0.0.1:${WASM_SEQ_RPC_PORT}" 40; then
  log "[err][wasm-full] sequencer RPC not ready"
  log "[hint][wasm-full] docker logs $WASM_SEQ_CONTAINER"
  exit 1
fi

ensure_fullnode_genesis
start_fullnode

if ! wait_rpc "http://127.0.0.1:${WASM_FULL_RPC_PORT}" 40; then
  log "[err][wasm-full] full node RPC not ready"
  log "[hint][wasm-full] docker logs $WASM_FULL_CONTAINER"
  exit 1
fi

SEQ_HEIGHT="$(curl -fsS "http://127.0.0.1:${WASM_SEQ_RPC_PORT}/status" | jq -r '.result.sync_info.latest_block_height')"
FULL_HEIGHT="$(curl -fsS "http://127.0.0.1:${WASM_FULL_RPC_PORT}/status" | jq -r '.result.sync_info.latest_block_height')"

log "[ok][wasm-full] sequencer_rpc=http://127.0.0.1:${WASM_SEQ_RPC_PORT} height=$SEQ_HEIGHT"
log "[ok][wasm-full] fullnode_rpc=http://127.0.0.1:${WASM_FULL_RPC_PORT} height=$FULL_HEIGHT"
log "[done][wasm-full] wasm full-node stack is running"
