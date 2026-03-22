#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Preserve runtime override for upload backend before loading .env
RUNTIME_COSMOS_DA_UPLOAD_MODE="${COSMOS_DA_UPLOAD_MODE:-}"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

if [[ -n "$RUNTIME_COSMOS_DA_UPLOAD_MODE" ]]; then
  COSMOS_DA_UPLOAD_MODE="$RUNTIME_COSMOS_DA_UPLOAD_MODE"
fi

COSMOS_HOME="${COSMOS_HOME:-$ROOT_DIR/apps/cosmos-exec/.cosmos-exec}"
COSMOS_ABCI_ADDR="${COSMOS_ABCI_ADDR:-tcp://0.0.0.0:26658}"
COSMOS_IN_MEMORY="${COSMOS_IN_MEMORY:-false}"
AUTO_KILL_DB_LOCK="${AUTO_KILL_DB_LOCK:-true}"
CHAIN_LOG_FILE="${CHAIN_LOG_FILE:-$ROOT_DIR/.logs/cosmos-chain.log}"
BLOCK_LOG_KEYWORDS="${BLOCK_LOG_KEYWORDS:-block|height|commit|finalize}"
DA_LOG_KEYWORDS="${DA_LOG_KEYWORDS:-da|celestia|submit|blob|namespace}"
COSMOS_DA_SUBMITTER_ENABLED="${COSMOS_DA_SUBMITTER_ENABLED:-true}"
COSMOS_DA_SUBMIT_INTERVAL="${COSMOS_DA_SUBMIT_INTERVAL:-8s}"
COSMOS_DA_SUBMIT_GAS_PRICE="${COSMOS_DA_SUBMIT_GAS_PRICE:-${DA_GAS_PRICE:-0}}"
COSMOS_DA_UPLOAD_MODE="${COSMOS_DA_UPLOAD_MODE:-engram}"
COSMOS_DA_SUBMIT_API="${COSMOS_DA_SUBMIT_API:-https://engram-api.a-star.group/data/submit-tx}"
COSMOS_DA_SUBMIT_API_TYPE="${COSMOS_DA_SUBMIT_API_TYPE:-engram}"
COSMOS_DA_CHAIN_LOG_FILE="${COSMOS_DA_CHAIN_LOG_FILE:-$CHAIN_LOG_FILE}"

DA_SUBMITTER_PID=""
ACTIVE_DA_URL="${DA_BRIDGE_RPC:-${DA_RPC:-}}"

mkdir -p "$COSMOS_HOME"
mkdir -p "$(dirname "$CHAIN_LOG_FILE")"

log() {
  local msg="$1"
  echo "$msg" | tee -a "$CHAIN_LOG_FILE"
}

stop_da_submitter() {
  if [[ -n "$DA_SUBMITTER_PID" ]] && kill -0 "$DA_SUBMITTER_PID" >/dev/null 2>&1; then
    log "[run] stopping da submitter pid=$DA_SUBMITTER_PID"
    kill "$DA_SUBMITTER_PID" >/dev/null 2>&1 || true
    wait "$DA_SUBMITTER_PID" 2>/dev/null || true
  fi
}

start_da_submitter() {
  if [[ "$COSMOS_DA_SUBMITTER_ENABLED" != "true" ]]; then
    log "[run] da submitter disabled (COSMOS_DA_SUBMITTER_ENABLED=$COSMOS_DA_SUBMITTER_ENABLED)"
    return 0
  fi

  local da_namespace
  if [[ "$COSMOS_DA_UPLOAD_MODE" == "engram" ]]; then
    da_namespace="${ENGRAM_NAMESPACE:-${DA_NAMESPACE:-rollup}}"
  else
    da_namespace="${DA_NAMESPACE:-rollup}"
  fi

  local -a submitter_args
  submitter_args=(
    --namespace "$da_namespace"
    --chain-log-file "$COSMOS_DA_CHAIN_LOG_FILE"
    --interval "$COSMOS_DA_SUBMIT_INTERVAL"
    --chain "cosmos-exec"
    --gas-price "$COSMOS_DA_SUBMIT_GAS_PRICE"
  )

  case "$COSMOS_DA_UPLOAD_MODE" in
    engram)
      submitter_args+=(--submit-api "$COSMOS_DA_SUBMIT_API" --api-type "$COSMOS_DA_SUBMIT_API_TYPE")
      ;;
    celestia)
      if [[ -z "$ACTIVE_DA_URL" ]]; then
        log "[warn] skip da submitter: COSMOS_DA_UPLOAD_MODE=celestia but DA_BRIDGE_RPC/DA_RPC empty"
        return 0
      fi
      submitter_args+=(--da-url "$ACTIVE_DA_URL" --auth-token "${DA_AUTH_TOKEN:-}")
      ;;
    *)
      log "[err] invalid COSMOS_DA_UPLOAD_MODE=$COSMOS_DA_UPLOAD_MODE (expected: engram|celestia)"
      return 1
      ;;
  esac

  log "[run] starting da submitter sidecar"
  (
    cd "$ROOT_DIR"
    go run ./tools/cosmos-da-submit "${submitter_args[@]}" 2>&1 \
      | tee -a "$CHAIN_LOG_FILE" \
      | awk '
          BEGIN { IGNORECASE=1 }
          {
            if ($0 ~ /da|submit|blob|namespace|error|warn/) print "[da] " $0
            fflush()
          }
        '
  ) &
  DA_SUBMITTER_PID=$!
  log "[ok] da submitter started pid=$DA_SUBMITTER_PID"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log "[err] required command not found: $cmd"
    exit 1
  fi
}

validate_inputs() {
  if [[ -z "$COSMOS_HOME" ]]; then
    log "[err] COSMOS_HOME is empty"
    exit 1
  fi

  if [[ -z "$COSMOS_ABCI_ADDR" ]]; then
    log "[err] COSMOS_ABCI_ADDR is empty"
    exit 1
  fi
}

find_lock_holder_pid() {
  local lock_file="$COSMOS_HOME/data/application.db/LOCK"
  if [[ ! -f "$lock_file" ]]; then
    return 1
  fi

  if ! command -v lsof >/dev/null 2>&1; then
    return 1
  fi

  local pid
  pid="$(lsof -t "$lock_file" 2>/dev/null | head -n 1 || true)"
  if [[ -z "$pid" ]]; then
    return 1
  fi

  echo "$pid"
}

kill_lock_holder() {
  local pid="$1"

  if [[ -z "$pid" ]]; then
    return 1
  fi

  if ! kill -0 "$pid" >/dev/null 2>&1; then
    return 1
  fi

  echo "[warn] killing stale cosmos-exec process holding DB lock (pid=$pid)"
  kill "$pid" >/dev/null 2>&1 || true
  sleep 1

  if kill -0 "$pid" >/dev/null 2>&1; then
    echo "[warn] process still alive after SIGTERM, sending SIGKILL (pid=$pid)"
    kill -9 "$pid" >/dev/null 2>&1 || true
    sleep 1
  fi

  return 0
}

run_cosmos_with_retry() {
  local attempt="${1:-1}"
  local max_attempts=2
  local run_log

  run_log="$(mktemp -t run-cosmos-chain.XXXXXX.log)"

  log "[info] full chain log: $CHAIN_LOG_FILE"
  log "[info] highlighting block logs: $BLOCK_LOG_KEYWORDS"
  log "[info] highlighting da logs: $DA_LOG_KEYWORDS"

  set +e
  go run ./cmd/cosmos-exec "${COSMOS_FLAGS[@]}" 2>&1 \
    | tee "$run_log" \
    | tee -a "$CHAIN_LOG_FILE" \
    | awk -v block_kw="$BLOCK_LOG_KEYWORDS" -v da_kw="$DA_LOG_KEYWORDS" '
        BEGIN { IGNORECASE=1 }
        {
          if ($0 ~ block_kw) print "[block] " $0
          if ($0 ~ da_kw) print "[da] " $0
          fflush()
        }
      '
  local exit_code=${PIPESTATUS[0]}
  set -e

  if [[ "$exit_code" -eq 0 ]]; then
    return 0
  fi

  if [[ "$AUTO_KILL_DB_LOCK" == "true" ]] && [[ "$attempt" -lt "$max_attempts" ]] && grep -qi "database lock detected" "$run_log"; then
    local lock_holder_pid
    lock_holder_pid="$(find_lock_holder_pid || true)"
    if [[ -n "$lock_holder_pid" ]]; then
      kill_lock_holder "$lock_holder_pid" || true
      log "[run] retrying cosmos-exec start after lock cleanup"
      run_cosmos_with_retry "$((attempt + 1))"
      return $?
    fi

    log "[err] database lock detected but could not identify lock holder pid"
  fi

  return "$exit_code"
}

require_cmd go
validate_inputs

trap 'stop_da_submitter' EXIT INT TERM

COSMOS_FLAGS=(
  --address "$COSMOS_ABCI_ADDR"
  --home "$COSMOS_HOME"
)

if [[ "$COSMOS_IN_MEMORY" == "true" ]]; then
  COSMOS_FLAGS+=(--in-memory)
fi

log "[run] starting cosmos-exec chain"
log "[run] cosmos.home=$COSMOS_HOME"
log "[run] cosmos.address=$COSMOS_ABCI_ADDR"
log "[run] cosmos.in_memory=$COSMOS_IN_MEMORY"
log "[run] auto_kill_db_lock=$AUTO_KILL_DB_LOCK"
log "[run] chain_log_file=$CHAIN_LOG_FILE"
log "[da] initializing DA integration checks"

if [[ -z "${DA_BRIDGE_RPC:-}" ]]; then
  log "[warn] DA_BRIDGE_RPC is empty; DA submission may fail if node requires external DA bridge"
  log "[da] bridge_rpc=empty"
else
  log "[run] da.bridge_rpc=$DA_BRIDGE_RPC"
  log "[da] bridge_rpc=$DA_BRIDGE_RPC"
fi

if [[ -z "${DA_NAMESPACE:-}" ]]; then
  log "[warn] DA_NAMESPACE is empty; DA submission may fail if namespace is required"
  log "[da] namespace=empty"
else
  log "[run] da.namespace=$DA_NAMESPACE"
  log "[da] namespace=$DA_NAMESPACE"
fi

log "[da] waiting for DA-related events from cosmos-exec runtime"
log "[run] da.upload_mode=$COSMOS_DA_UPLOAD_MODE"
log "[run] da.active_rpc=$ACTIVE_DA_URL"
log "[run] da.submit_api=$COSMOS_DA_SUBMIT_API"
log "[run] da.submit_api_type=$COSMOS_DA_SUBMIT_API_TYPE"
log "[run] da.chain_log_file=$COSMOS_DA_CHAIN_LOG_FILE"
start_da_submitter

cd "$ROOT_DIR/apps/cosmos-exec"
run_cosmos_with_retry 1
