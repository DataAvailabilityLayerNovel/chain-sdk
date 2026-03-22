#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

CHAIN_LOG_FILE="${CHAIN_LOG_FILE:-$ROOT_DIR/.logs/cosmos-chain.log}"
NODE_HOME="${FULLNODE_HOME:-/tmp/ev-node-testapp-da}"
PASS_FILE="${FULLNODE_PASSPHRASE_FILE:-$NODE_HOME/passphrase.txt}"
CHAIN_ID="${FULLNODE_CHAIN_ID:-evnode-da-local}"
FULLNODE_APP_CMD="${FULLNODE_APP_CMD:-go run ./apps/testapp}"
FULLNODE_NODE_ROLE="${FULLNODE_NODE_ROLE:-full}"
FULLNODE_PEERS="${FULLNODE_PEERS:-}"

DA_URL="${DA_BRIDGE_RPC:-${DA_RPC:-}}"
DA_TOKEN="${DA_AUTH_TOKEN:-}"
DA_NAMESPACE_VALUE="${DA_NAMESPACE:-rollup}"
RPC_ADDR="${FULLNODE_RPC_ADDR:-0.0.0.0:7331}"
P2P_ADDR="${FULLNODE_P2P_ADDR:-/ip4/0.0.0.0/tcp/26656}"

mkdir -p "$(dirname "$CHAIN_LOG_FILE")"
mkdir -p "$NODE_HOME"

log() {
  local msg="$1"
  echo "$msg" | tee -a "$CHAIN_LOG_FILE"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log "[err][fullnode] missing command: $cmd"
    exit 1
  fi
}

require_cmd go

if [[ -z "$DA_URL" ]]; then
  log "[err][fullnode] DA_BRIDGE_RPC (or DA_RPC) is empty"
  exit 1
fi

if [[ "$COSMOS_DA_UPLOAD_MODE" == "engram" ]]; then
  log "[err][fullnode] FULLNODE currently supports Celestia RPC DA only. Set COSMOS_DA_UPLOAD_MODE=celestia"
  exit 1
fi

if [[ ! -f "$PASS_FILE" ]]; then
  mkdir -p "$(dirname "$PASS_FILE")"
  printf '%s\n' "12345678" > "$PASS_FILE"
  chmod 600 "$PASS_FILE"
fi

IFS=' ' read -r -a APP_CMD <<< "$FULLNODE_APP_CMD"
if [[ ${#APP_CMD[@]} -eq 0 ]]; then
  log "[err][fullnode] FULLNODE_APP_CMD is empty"
  exit 1
fi

ROLLKIT_AGGREGATOR="false"
if [[ "$FULLNODE_NODE_ROLE" == "sequencer" ]]; then
  ROLLKIT_AGGREGATOR="true"
fi

if [[ "$FULLNODE_NODE_ROLE" == "full" && -z "$FULLNODE_PEERS" ]]; then
  log "[warn][fullnode] FULLNODE_PEERS is empty; full node may not sync without sequencer peer"
fi

log "[run][fullnode] preparing full node home: $NODE_HOME"
log "[run][fullnode] da.url=$DA_URL"
log "[run][fullnode] da.namespace=$DA_NAMESPACE_VALUE"
log "[run][fullnode] app.cmd=$FULLNODE_APP_CMD"
log "[run][fullnode] node.role=$FULLNODE_NODE_ROLE aggregator=$ROLLKIT_AGGREGATOR"
if [[ -n "$FULLNODE_PEERS" ]]; then
  log "[run][fullnode] peers=$FULLNODE_PEERS"
fi

if [[ ! -f "$NODE_HOME/config/evnode.yml" ]]; then
  log "[run][fullnode] initializing testapp config"
  (cd "$ROOT_DIR" && "${APP_CMD[@]}" init \
    --home "$NODE_HOME" \
    --chain-id "$CHAIN_ID" \
    --rollkit.node.aggregator="$ROLLKIT_AGGREGATOR" \
    --rollkit.da.address "$DA_URL" \
    --rollkit.da.auth_token "$DA_TOKEN" \
    --rollkit.da.namespace "$DA_NAMESPACE_VALUE" \
    --rollkit.rpc.address "$RPC_ADDR" \
    --rollkit.p2p.listen_address "$P2P_ADDR" \
    --rollkit.signer.passphrase_file "$PASS_FILE")
else
  log "[ok][fullnode] existing config found, skip init"
fi

log "[run][fullnode] starting node with DA"

cd "$ROOT_DIR"
START_ARGS=(
  --home "$NODE_HOME"
  --rollkit.node.aggregator="$ROLLKIT_AGGREGATOR"
  --rollkit.da.address "$DA_URL"
  --rollkit.da.auth_token "$DA_TOKEN"
  --rollkit.da.namespace "$DA_NAMESPACE_VALUE"
  --rollkit.rpc.address "$RPC_ADDR"
  --rollkit.p2p.listen_address "$P2P_ADDR"
  --rollkit.signer.passphrase_file "$PASS_FILE"
  --rollkit.log.level debug
  --rollkit.log.format text
  --rollkit.node.block_time 2s
  --rollkit.da.block_time 12s
  --rollkit.da.max_submit_attempts 5
  --rollkit.da.request_timeout 12s
)

if [[ "$FULLNODE_NODE_ROLE" == "full" && -n "$FULLNODE_PEERS" ]]; then
  START_ARGS+=(--rollkit.p2p.peers "$FULLNODE_PEERS")
fi

"${APP_CMD[@]}" start \
  "${START_ARGS[@]}" 2>&1 \
  | tee -a "$CHAIN_LOG_FILE" \
  | awk '
      BEGIN { IGNORECASE=1 }
      {
        if ($0 ~ /da|celestia|blob|namespace|submit|commitment/) print "[da] " $0
        if ($0 ~ /block|height|finalize|commit/) print "[block] " $0
        fflush()
      }
    '
