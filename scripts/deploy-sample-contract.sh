#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

CONTAINER_NAME="${COSMOS_WASM_SEQ_CONTAINER:-${COSMOS_WASM_CONTAINER:-cosmos-wasm-sequencer}}"
IMAGE="${COSMOS_WASM_IMAGE:-cosmwasm/wasmd:v0.45.0}"
PLATFORM="${COSMOS_WASM_PLATFORM:-linux/amd64}"
CHAIN_ID="${CHAIN_ID:-localwasm}"
MONIKER="${MONIKER:-localwasm}"
KEY_NAME="${FROM_KEY:-validator}"
KEYRING_BACKEND="${KEYRING_BACKEND:-test}"
HOME_DIR="${COSMOS_WASM_HOME:-/wasmd}"
DENOM="${DENOM:-stake}"
SEQ_RPC_PORT="${COSMOS_WASM_SEQ_RPC_PORT:-26657}"
NODE="${NODE:-http://127.0.0.1:26657}"
GAS="${GAS:-auto}"
GAS_ADJUSTMENT="${GAS_ADJUSTMENT:-1.4}"
FEES="${FEES:-5000stake}"
BROADCAST_MODE="${BROADCAST_MODE:-sync}"
CHAIN_LOG_FILE="${CHAIN_LOG_FILE:-$ROOT_DIR/.logs/cosmos-chain.log}"

WORK_DIR="${WASM_WORK_DIR:-/tmp/ev-node-wasm}"
WASM_URL="${WASM_URL:-https://github.com/CosmWasm/cw-plus/releases/download/v1.1.0/cw20_base.wasm}"
WASM_FILE="${WASM_FILE:-$WORK_DIR/cw20_base.wasm}"
DEPLOY_OUTPUT_FILE="${DEPLOY_OUTPUT_FILE:-$WORK_DIR/last-deploy.env}"

mkdir -p "$(dirname "$CHAIN_LOG_FILE")"

log() {
  local msg="$1"
  echo "$msg" | tee -a "$CHAIN_LOG_FILE"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd"
    exit 1
  fi
}

require_cmd docker
require_cmd curl
require_cmd jq

ensure_wasm_chain() {
  if docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    log "[ok][wasm] chain already running: $CONTAINER_NAME"
    return
  fi

  if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    docker rm "$CONTAINER_NAME" >/dev/null
  fi

  log "[run][wasm] starting local wasm chain: $CONTAINER_NAME"
  docker run -d \
    --name "$CONTAINER_NAME" \
    --platform "$PLATFORM" \
    -p 26657:26657 \
    -p 26656:26656 \
    -p 1317:1317 \
    "$IMAGE" \
    sh -lc "\
      set -euo pipefail; \
      wasmd init '$MONIKER' --chain-id '$CHAIN_ID' --home '$HOME_DIR' >/dev/null 2>&1; \
      wasmd config keyring-backend '$KEYRING_BACKEND' --home '$HOME_DIR' >/dev/null 2>&1; \
      wasmd config chain-id '$CHAIN_ID' --home '$HOME_DIR' >/dev/null 2>&1; \
      echo 'test test test test test test test test test test test junk' | wasmd keys add '$KEY_NAME' --recover --keyring-backend '$KEYRING_BACKEND' --home '$HOME_DIR' >/dev/null 2>&1; \
      wasmd genesis add-genesis-account \"\$(wasmd keys show '$KEY_NAME' -a --keyring-backend '$KEYRING_BACKEND' --home '$HOME_DIR')\" 100000000000'$DENOM' --home '$HOME_DIR' >/dev/null 2>&1; \
      wasmd genesis gentx '$KEY_NAME' 1000000'$DENOM' --chain-id '$CHAIN_ID' --keyring-backend '$KEYRING_BACKEND' --home '$HOME_DIR' >/dev/null 2>&1; \
      wasmd genesis collect-gentxs --home '$HOME_DIR' >/dev/null 2>&1; \
      sed -i 's/^minimum-gas-prices =.*/minimum-gas-prices = \"0.001$DENOM\"/' '$HOME_DIR'/config/app.toml; \
      wasmd start --home '$HOME_DIR' --rpc.laddr tcp://0.0.0.0:26657 --api.enable true --grpc.enable true \
    " >/dev/null

  for _ in $(seq 1 40); do
    if curl -fsS "$NODE/status" >/dev/null 2>&1; then
      log "[ok][wasm] chain ready: $NODE"
      return
    fi
    sleep 1
  done

  log "[err][wasm] chain not ready"
  log "[hint][wasm] logs: docker logs $CONTAINER_NAME"
  exit 1
}

run_wasmd() {
  docker exec -i "$CONTAINER_NAME" wasmd "$@" --home "$HOME_DIR"
}

extract_json() {
  sed -n '/^{/,$p'
}

get_max_code_id() {
  run_wasmd query wasm list-code --node "$NODE" --output json | jq -r '[.code_infos[]?.code_id | tonumber] | max // 0'
}

get_contracts_by_code() {
  local code_id="$1"
  run_wasmd query wasm list-contract-by-code "$code_id" --node "$NODE" --output json | jq -r '.contracts[]?'
}

mkdir -p "$WORK_DIR"

ensure_wasm_chain

if [[ ! -f "$WASM_FILE" ]]; then
  log "[run][wasm] downloading sample contract"
  curl -fsSL "$WASM_URL" -o "$WASM_FILE"
fi

log "[run][wasm] preparing init/exec/query payloads"
if ! run_wasmd keys show "$KEY_NAME" --keyring-backend "$KEYRING_BACKEND" --home "$HOME_DIR" >/dev/null 2>&1; then
  echo 'test test test test test test test test test test test junk' \
    | run_wasmd keys add "$KEY_NAME" --recover --keyring-backend "$KEYRING_BACKEND" --home "$HOME_DIR" >/dev/null 2>&1 || true
fi
ADDR="$(run_wasmd keys show "$KEY_NAME" -a --keyring-backend "$KEYRING_BACKEND" --home "$HOME_DIR")"

INIT_MSG_FILE="$WORK_DIR/init-msg.json"
EXEC_MSG_FILE="$WORK_DIR/exec-msg.json"
QUERY_MSG_FILE="$WORK_DIR/query-msg.json"

cat > "$INIT_MSG_FILE" <<EOF
{"name":"Token","symbol":"TOK","decimals":6,"initial_balances":[{"address":"$ADDR","amount":"1000000"}],"mint":{"minter":"$ADDR","cap":"1000000000"},"marketing":null}
EOF

cat > "$EXEC_MSG_FILE" <<EOF
{"transfer":{"recipient":"$ADDR","amount":"1"}}
EOF

cat > "$QUERY_MSG_FILE" <<EOF
{"balance":{"address":"$ADDR"}}
EOF

log "[run][wasm] storing wasm"
CODE_ID_BEFORE="$(get_max_code_id)"
STORE_IN_CONTAINER="/tmp/contract-$(date +%s).wasm"
docker cp "$WASM_FILE" "$CONTAINER_NAME:$STORE_IN_CONTAINER" >/dev/null

STORE_JSON="$(run_wasmd tx wasm store "$STORE_IN_CONTAINER" \
  --from "$KEY_NAME" \
  --keyring-backend "$KEYRING_BACKEND" \
  --chain-id "$CHAIN_ID" \
  --node "$NODE" \
  --broadcast-mode "$BROADCAST_MODE" \
  --gas "$GAS" \
  --gas-adjustment "$GAS_ADJUSTMENT" \
  --fees "$FEES" \
  --yes \
  --output json 2>&1 || true)"

TXHASH_STORE="$(echo "$STORE_JSON" | jq -r '.txhash // empty' 2>/dev/null || true)"
if [[ -z "$TXHASH_STORE" ]]; then
  TXHASH_STORE="$(echo "$STORE_JSON" | extract_json | jq -r '.txhash // empty' 2>/dev/null || true)"
fi
if [[ -z "$TXHASH_STORE" ]]; then
  echo "[err] store failed"
  echo "$STORE_JSON"
  exit 1
fi

CODE_ID=""
for _ in $(seq 1 30); do
  CODE_ID_AFTER="$(get_max_code_id)"
  if [[ "$CODE_ID_AFTER" =~ ^[0-9]+$ && "$CODE_ID_BEFORE" =~ ^[0-9]+$ && "$CODE_ID_AFTER" -gt "$CODE_ID_BEFORE" ]]; then
    CODE_ID="$CODE_ID_AFTER"
    break
  fi
  sleep 1
done

if [[ -z "$CODE_ID" ]]; then
  echo "[err] could not detect new code_id"
  exit 1
fi

log "[ok][wasm] code_id=$CODE_ID"

log "[run][wasm] instantiating contract"
CONTRACTS_BEFORE="$(get_contracts_by_code "$CODE_ID" | sort || true)"

INIT_JSON="$(run_wasmd tx wasm instantiate "$CODE_ID" "$(cat "$INIT_MSG_FILE")" \
  --label "sample-cw20-$(date +%s)" \
  --from "$KEY_NAME" \
  --keyring-backend "$KEYRING_BACKEND" \
  --chain-id "$CHAIN_ID" \
  --node "$NODE" \
  --broadcast-mode "$BROADCAST_MODE" \
  --gas "$GAS" \
  --gas-adjustment "$GAS_ADJUSTMENT" \
  --fees "$FEES" \
  --no-admin \
  --yes \
  --output json 2>&1 || true)"

TXHASH_INIT="$(echo "$INIT_JSON" | jq -r '.txhash // empty' 2>/dev/null || true)"
if [[ -z "$TXHASH_INIT" ]]; then
  TXHASH_INIT="$(echo "$INIT_JSON" | extract_json | jq -r '.txhash // empty' 2>/dev/null || true)"
fi
if [[ -z "$TXHASH_INIT" ]]; then
  echo "[err] instantiate failed"
  echo "$INIT_JSON"
  exit 1
fi

CONTRACT_ADDR=""
for _ in $(seq 1 20); do
  CONTRACTS_AFTER="$(get_contracts_by_code "$CODE_ID" | sort || true)"
  CONTRACT_ADDR="$(comm -13 <(echo "$CONTRACTS_BEFORE") <(echo "$CONTRACTS_AFTER") | head -n1)"
  if [[ -n "$CONTRACT_ADDR" ]]; then
    break
  fi
  sleep 1
done

if [[ -z "$CONTRACT_ADDR" ]]; then
  CONTRACT_ADDR="$(get_contracts_by_code "$CODE_ID" | tail -n1 || true)"
fi

if [[ -z "$CONTRACT_ADDR" ]]; then
  echo "[err] cannot detect contract address"
  exit 1
fi

log "[ok][wasm] contract_address=$CONTRACT_ADDR"

log "[run][wasm] dry execute + query"
run_wasmd tx wasm execute "$CONTRACT_ADDR" "$(cat "$EXEC_MSG_FILE")" \
  --from "$KEY_NAME" \
  --keyring-backend "$KEYRING_BACKEND" \
  --chain-id "$CHAIN_ID" \
  --node "$NODE" \
  --broadcast-mode "$BROADCAST_MODE" \
  --gas "$GAS" \
  --gas-adjustment "$GAS_ADJUSTMENT" \
  --fees "$FEES" \
  --yes \
  --output json >/dev/null

QUERY_RESULT="$(run_wasmd query wasm contract-state smart "$CONTRACT_ADDR" "$(cat "$QUERY_MSG_FILE")" --node "$NODE" --output json)"
log "[ok][wasm] query_result=$QUERY_RESULT"

cat > "$DEPLOY_OUTPUT_FILE" <<EOF
CONTRACT_ADDR=$CONTRACT_ADDR
CODE_ID=$CODE_ID
CHAIN_ID=$CHAIN_ID
NODE=$NODE
FROM_KEY=$KEY_NAME
KEYRING_BACKEND=$KEYRING_BACKEND
DENOM=$DENOM
EXEC_MSG_FILE=$EXEC_MSG_FILE
QUERY_MSG_FILE=$QUERY_MSG_FILE
EOF

log "[done][wasm] sample contract deployed"
log "[info][wasm] deploy metadata: $DEPLOY_OUTPUT_FILE"
