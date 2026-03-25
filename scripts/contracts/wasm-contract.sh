#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

EXEC_API_URL="${COSMOS_EXEC_API_URL:-http://127.0.0.1:50051}"
WASM_TX_TOOL="${WASM_TX_TOOL:-./cmd/cosmos-wasm-tx}"
DEPLOY_OUTPUT_FILE="${DEPLOY_OUTPUT_FILE:-/tmp/ev-node-wasm/last-deploy.env}"
WASM_WORK_DIR="${WASM_WORK_DIR:-/tmp/ev-node-wasm}"
WASM_URL="${WASM_URL:-https://github.com/CosmWasm/cw-plus/releases/download/v1.1.0/cw20_base.wasm}"
WASM_FILE="${WASM_FILE:-$WASM_WORK_DIR/cw20_base.wasm}"
SENDER="${SENDER:-}"

mkdir -p "$WASM_WORK_DIR"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/contracts/wasm-contract.sh deploy
  ./scripts/contracts/wasm-contract.sh submit --tx-base64 <TX_BASE64>
  ./scripts/contracts/wasm-contract.sh execute --msg '{"transfer":{"recipient":"cosmos1...","amount":"1"}}' [--contract cosmos1...]
  ./scripts/contracts/wasm-contract.sh query --msg '{"balance":{"address":"cosmos1..."}}' [--contract cosmos1...]

Notes:
  - Default mode uses full-stack flow via cosmos-exec-grpc API on :50051.
  - Legacy wasmd standalone fallback: export COSMOS_WASM_ALLOW_STANDALONE=1
  - default CONTRACT_ADDR is loaded from DEPLOY_OUTPUT_FILE when omitted.
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd"
    exit 1
  fi
}

load_contract_addr() {
  local contract_addr="${CONTRACT_ADDR:-}"
  if [[ -n "$contract_addr" ]]; then
    echo "$contract_addr"
    return 0
  fi

  if [[ -f "$DEPLOY_OUTPUT_FILE" ]]; then
    local loaded
    loaded="$(grep -E '^CONTRACT_ADDR=' "$DEPLOY_OUTPUT_FILE" | tail -n1 | cut -d'=' -f2- || true)"
    if [[ -n "$loaded" ]]; then
      echo "$loaded"
      return 0
    fi
  fi

  return 1
}

default_sender() {
  (cd "$ROOT_DIR/apps/cosmos-exec" && go run "$WASM_TX_TOOL" default-sender)
}

get_sender() {
  if [[ -n "$SENDER" ]]; then
    echo "$SENDER"
    return 0
  fi
  default_sender
}

ensure_wasm_file() {
  if [[ -f "$WASM_FILE" ]]; then
    return 0
  fi
  require_cmd curl
  echo "[run] downloading wasm: $WASM_URL"
  curl -fsSL "$WASM_URL" -o "$WASM_FILE"
}

submit_tx_base64() {
  local tx_base64="$1"
  curl -fsS -X POST "$EXEC_API_URL/tx/submit" \
    -H 'Content-Type: application/json' \
    --data "$(jq -nc --arg tx "$tx_base64" '{tx_base64:$tx}')"
}

wait_tx_result() {
  local hash="$1"
  for _ in $(seq 1 60); do
    local resp
    resp="$(curl -fsS "$EXEC_API_URL/tx/result?hash=$hash")"
    if [[ "$(echo "$resp" | jq -r '.found // false')" == "true" ]]; then
      echo "$resp"
      return 0
    fi
    sleep 1
  done
  return 1
}

extract_attr() {
  local result_json="$1"
  local event_type="$2"
  local key="$3"
  echo "$result_json" | jq -r --arg t "$event_type" --arg k "$key" '
    .result.events[]? | select(.type==$t) | .attributes[]? | select(.key==$k) | .value
  ' | tail -n1
}

default_init_msg() {
  local sender="$1"
  jq -nc --arg sender "$sender" '{
    name: "Token",
    symbol: "TOK",
    decimals: 6,
    initial_balances: [{address: $sender, amount: "1000000"}],
    mint: {minter: $sender, cap: "1000000000"},
    marketing: null
  }'
}

cmd_deploy() {
  require_cmd go
  require_cmd jq
  ensure_wasm_file

  local sender
  sender="$(get_sender)"

  local store_tx
  store_tx="$(cd "$ROOT_DIR/apps/cosmos-exec" && go run "$WASM_TX_TOOL" store --wasm "$WASM_FILE" --sender "$sender" --out base64)"

  local submit_store
  submit_store="$(submit_tx_base64 "$store_tx")"
  local store_hash
  store_hash="$(echo "$submit_store" | jq -r '.hash // empty')"
  if [[ -z "$store_hash" ]]; then
    echo "[err] failed to submit store tx"
    echo "$submit_store"
    exit 1
  fi

  local store_result
  if ! store_result="$(wait_tx_result "$store_hash")"; then
    echo "[err] timeout waiting store tx result: $store_hash"
    exit 1
  fi
  local store_code
  store_code="$(echo "$store_result" | jq -r '.result.code // 1')"
  if [[ "$store_code" != "0" ]]; then
    echo "[err] store tx failed"
    echo "$store_result" | jq .
    exit 1
  fi

  local code_id
  code_id="$(extract_attr "$store_result" "store_code" "code_id")"
  if [[ -z "$code_id" ]]; then
    code_id="$(echo "$store_result" | jq -r '.result.events[]? | .attributes[]? | select(.key=="code_id") | .value' | tail -n1)"
  fi
  if [[ -z "$code_id" ]]; then
    echo "[err] cannot detect code_id from store result"
    echo "$store_result" | jq .
    exit 1
  fi

  local init_msg
  if [[ -n "${INIT_MSG:-}" && "${INIT_MSG}" != "{}" ]]; then
    init_msg="$INIT_MSG"
  else
    init_msg="$(default_init_msg "$sender")"
  fi
  local label="${LABEL:-fullnode-wasm-$(date +%s)}"
  local instantiate_tx
  instantiate_tx="$(cd "$ROOT_DIR/apps/cosmos-exec" && go run "$WASM_TX_TOOL" instantiate --sender "$sender" --code-id "$code_id" --msg "$init_msg" --label "$label" --out base64)"

  local submit_init
  submit_init="$(submit_tx_base64 "$instantiate_tx")"
  local init_hash
  init_hash="$(echo "$submit_init" | jq -r '.hash // empty')"
  if [[ -z "$init_hash" ]]; then
    echo "[err] failed to submit instantiate tx"
    echo "$submit_init"
    exit 1
  fi

  local init_result
  if ! init_result="$(wait_tx_result "$init_hash")"; then
    echo "[err] timeout waiting instantiate tx result: $init_hash"
    exit 1
  fi
  local init_code
  init_code="$(echo "$init_result" | jq -r '.result.code // 1')"
  if [[ "$init_code" != "0" ]]; then
    echo "[err] instantiate tx failed"
    echo "$init_result" | jq .
    exit 1
  fi

  local contract_addr
  contract_addr="$(extract_attr "$init_result" "instantiate" "_contract_address")"
  if [[ -z "$contract_addr" ]]; then
    contract_addr="$(echo "$init_result" | jq -r '.result.events[]? | .attributes[]? | select(.key=="_contract_address" or .key=="contract_address") | .value' | tail -n1)"
  fi
  if [[ -z "$contract_addr" ]]; then
    echo "[err] cannot detect contract address from instantiate result"
    echo "$init_result" | jq .
    exit 1
  fi

  mkdir -p "$(dirname "$DEPLOY_OUTPUT_FILE")"
  cat > "$DEPLOY_OUTPUT_FILE" <<EOF
CONTRACT_ADDR=$contract_addr
CODE_ID=$code_id
SENDER=$sender
STORE_TX_HASH=$store_hash
INIT_TX_HASH=$init_hash
EOF

  echo "[ok] code_id=$code_id"
  echo "[ok] contract_address=$contract_addr"
  echo "[ok] output_file=$DEPLOY_OUTPUT_FILE"
}

cmd_submit() {
  local tx_base64=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --tx-base64)
        tx_base64="${2:-}"
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

  if [[ -z "$tx_base64" ]]; then
    echo "[err] --tx-base64 is required"
    exit 1
  fi

  submit_tx_base64 "$tx_base64" | jq .
}

cmd_execute_or_query() {
  local action="$1"
  shift

  local msg=""
  local contract_addr=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --msg)
        msg="${2:-}"
        shift 2
        ;;
      --contract)
        contract_addr="${2:-}"
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

  if [[ -z "$msg" ]]; then
    echo "[err] --msg is required"
    usage
    exit 1
  fi

  require_cmd jq
  require_cmd go

  if [[ -z "$contract_addr" ]]; then
    if ! contract_addr="$(load_contract_addr)"; then
      echo "[err] CONTRACT_ADDR not found"
      echo "[hint] pass --contract cosmos1... or run deploy first"
      exit 1
    fi
  fi

  local sender
  sender="$(get_sender)"

  if [[ "$action" == "execute" ]]; then
    local execute_tx
    execute_tx="$(cd "$ROOT_DIR/apps/cosmos-exec" && go run "$WASM_TX_TOOL" execute --sender "$sender" --contract "$contract_addr" --msg "$msg" --out base64)"
    local submit_resp
    submit_resp="$(submit_tx_base64 "$execute_tx")"
    local tx_hash
    tx_hash="$(echo "$submit_resp" | jq -r '.hash // empty')"
    if [[ -z "$tx_hash" ]]; then
      echo "[err] failed to submit execute tx"
      echo "$submit_resp"
      exit 1
    fi

    local execute_result
    if ! execute_result="$(wait_tx_result "$tx_hash")"; then
      echo "[err] timeout waiting execute tx result: $tx_hash"
      exit 1
    fi

    echo "$execute_result" | jq .
    return 0
  fi

  curl -fsS -X POST "$EXEC_API_URL/wasm/query-smart" \
    -H 'Content-Type: application/json' \
    --data "$(jq -nc --arg c "$contract_addr" --argjson m "$msg" '{contract:$c,msg:$m}')" \
    | jq .
}

main() {
  if [[ $# -lt 1 ]]; then
    usage
    exit 1
  fi

  if [[ "${COSMOS_WASM_ALLOW_STANDALONE:-}" == "1" ]]; then
    echo "[warn] legacy mode is no longer implemented in this wrapper"
    echo "[hint] use scripts/deploy-sample-contract.sh or scripts/submit-tx.sh directly if needed"
  fi

  local cmd="$1"
  shift

  case "$cmd" in
    deploy)
      cmd_deploy "$@"
      ;;
    submit)
      cmd_submit "$@"
      ;;
    execute)
      cmd_execute_or_query execute "$@"
      ;;
    query)
      cmd_execute_or_query query "$@"
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
