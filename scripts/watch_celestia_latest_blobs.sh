#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

DEFAULT_NAMESPACE="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA="
NAMESPACE="${DA_NAMESPACE_B64:-$DEFAULT_NAMESPACE}"
CELESTIA_RPC="${CELESTIA_BRIDGE_RPC:-http://131.153.224.169:26758}"
AUTH_TOKEN="${DA_AUTH_TOKEN:-}"

INTERVAL="${INTERVAL_SECONDS:-6}"
BACKFILL="${BACKFILL_HEIGHTS:-15}"
MAX_PER_CYCLE="${MAX_HEIGHTS_PER_CYCLE:-20}"
RETRIES="${REQUEST_RETRIES:-3}"
RETRY_SLEEP_MS="${RETRY_SLEEP_MS:-300}"
START_HEIGHT=""
ONCE="false"
SHOW_ERRORS="${SHOW_RPC_ERRORS:-false}"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/watch_celestia_latest_blobs.sh [options]

Options:
  --interval <seconds>      Poll interval in seconds (default: 6)
  --backfill <n>            Initial backfill number of heights (default: 15)
  --max-per-cycle <n>       Max heights to query per loop (default: 20)
  --retries <n>             Retry count per height on transient errors (default: 3)
  --retry-sleep-ms <ms>     Sleep between retries in milliseconds (default: 300)
  --start-height <height>   Start querying from explicit DA height
  --namespace <base64_ns>   Override namespace (base64)
  --once                    Query one pass then exit
  --show-errors             Show RPC/request errors in output
  -h, --help                Show this help

Examples:
  ./scripts/watch_celestia_latest_blobs.sh
  ./scripts/watch_celestia_latest_blobs.sh --interval 4 --backfill 30
  ./scripts/watch_celestia_latest_blobs.sh --start-height 31100
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd"
    exit 1
  fi
}

decode_blob_data() {
  local b64_data="$1"
  local decoded

  decoded="$(printf '%s' "$b64_data" | base64 -d 2>/dev/null || true)"
  if [[ -z "$decoded" ]]; then
    echo "[err] cannot decode base64 data"
    return 1
  fi

  if printf '%s' "$decoded" | jq -e . >/dev/null 2>&1; then
    printf '%s' "$decoded" | jq .
  else
    printf '%s\n' "$decoded"
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --interval)
        INTERVAL="${2:-}"
        shift 2
        ;;
      --backfill)
        BACKFILL="${2:-}"
        shift 2
        ;;
      --max-per-cycle)
        MAX_PER_CYCLE="${2:-}"
        shift 2
        ;;
      --retries)
        RETRIES="${2:-}"
        shift 2
        ;;
      --retry-sleep-ms)
        RETRY_SLEEP_MS="${2:-}"
        shift 2
        ;;
      --start-height)
        START_HEIGHT="${2:-}"
        shift 2
        ;;
      --namespace)
        NAMESPACE="${2:-}"
        shift 2
        ;;
      --once)
        ONCE="true"
        shift
        ;;
      --show-errors)
        SHOW_ERRORS="true"
        shift
        ;;
      -h|--help|help)
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

  if ! [[ "$INTERVAL" =~ ^[0-9]+$ ]] || (( INTERVAL <= 0 )); then
    echo "[err] --interval must be a positive integer"
    exit 1
  fi

  if ! [[ "$BACKFILL" =~ ^[0-9]+$ ]] || (( BACKFILL <= 0 )); then
    echo "[err] --backfill must be a positive integer"
    exit 1
  fi

  if [[ -n "$START_HEIGHT" ]] && ! [[ "$START_HEIGHT" =~ ^[0-9]+$ ]]; then
    echo "[err] --start-height must be a positive integer"
    exit 1
  fi

  if ! [[ "$MAX_PER_CYCLE" =~ ^[0-9]+$ ]] || (( MAX_PER_CYCLE <= 0 )); then
    echo "[err] --max-per-cycle must be a positive integer"
    exit 1
  fi

  if ! [[ "$RETRIES" =~ ^[0-9]+$ ]] || (( RETRIES <= 0 )); then
    echo "[err] --retries must be a positive integer"
    exit 1
  fi

  if ! [[ "$RETRY_SLEEP_MS" =~ ^[0-9]+$ ]] || (( RETRY_SLEEP_MS < 0 )); then
    echo "[err] --retry-sleep-ms must be a non-negative integer"
    exit 1
  fi
}

sleep_ms() {
  local ms="$1"
  python3 - <<'PY' "$ms"
import sys, time
time.sleep(int(sys.argv[1]) / 1000)
PY
}

log_rpc_error() {
  local msg="$1"
  if [[ "$SHOW_ERRORS" == "true" ]]; then
    echo "$msg"
  fi
}

network_head() {
  local response http_code body

  response=$(curl -sS -w '\n%{http_code}' -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d '{"jsonrpc":"2.0","id":1,"method":"header.NetworkHead","params":[]}') || return 1

  http_code="$(printf '%s' "$response" | tail -n 1)"
  body="$(printf '%s' "$response" | sed '$d')"

  if [[ "$http_code" != "200" ]]; then
    return 1
  fi

  echo "$body" | jq -r '.result.height // .result.header.height // .result.commit.height // empty'
}

query_one_height() {
  local h="$1"
  local response http_code body attempt
  local payload

  payload="$(jq -nc --argjson h "$h" --arg ns "$NAMESPACE" '{jsonrpc:"2.0", id:1, method:"blob.GetAll", params:[$h,[$ns]]}')"

  for ((attempt=1; attempt<=RETRIES; attempt++)); do
    response=$(curl -sS -w '\n%{http_code}' -X POST "$CELESTIA_RPC" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -d "$payload") || {
        if (( attempt == RETRIES )); then
          log_rpc_error "[h=$h] request_failed"
          return 0
        fi
        sleep_ms "$RETRY_SLEEP_MS"
        continue
      }

    http_code="$(printf '%s' "$response" | tail -n 1)"
    body="$(printf '%s' "$response" | sed '$d')"

    if [[ "$http_code" == "200" ]]; then
      break
    fi

    if (( attempt == RETRIES )); then
      body="$(curl -fsS -X POST "$CELESTIA_RPC" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $AUTH_TOKEN" \
        -d "$payload" 2>/dev/null || true)"
      if [[ -z "$body" ]]; then
        log_rpc_error "[h=$h] rpc_http_error=$http_code"
        return 0
      fi
      http_code="200"
      break
    fi
    sleep_ms "$RETRY_SLEEP_MS"
  done

  if ! echo "$body" | jq -e . >/dev/null 2>&1; then
    log_rpc_error "[h=$h] rpc_invalid_json"
    return 0
  fi

  local rpc_error
  rpc_error="$(echo "$body" | jq -r '.error.message // empty')"
  if [[ -n "$rpc_error" ]]; then
    log_rpc_error "[h=$h] rpc_error=$rpc_error"
    return 0
  fi

  local blob_count
  blob_count="$(echo "$body" | jq -r '.result | length // 0')"

  if [[ "$blob_count" == "0" ]]; then
    return 0
  fi

  echo "[$(date '+%Y-%m-%d %H:%M:%S')] [h=$h] blobs=$blob_count"

  local idx
  for ((idx=0; idx<blob_count; idx++)); do
    local b64_data
    b64_data="$(echo "$body" | jq -r ".result[$idx].data")"
    echo "  - blob[$idx] decoded:"
    decode_blob_data "$b64_data" | sed 's/^/    /'
  done
}

main() {
  require_cmd jq
  require_cmd curl
  parse_args "$@"

  if [[ -z "$AUTH_TOKEN" ]]; then
    echo "[err] CELESTIA_AUTH_TOKEN is required"
    echo "[hint] export CELESTIA_AUTH_TOKEN=... (or set in .env)"
    exit 1
  fi

  echo "👀 Watching latest Celestia blobs"
  echo "   RPC: $CELESTIA_RPC"
  echo "   Namespace: $NAMESPACE"
  echo "   Interval: ${INTERVAL}s"
  echo "   Backfill: $BACKFILL heights"
  echo "   Max per cycle: $MAX_PER_CYCLE"
  echo "   Retries/height: $RETRIES"
  echo "   Show RPC errors: $SHOW_ERRORS"
  [[ -n "$START_HEIGHT" ]] && echo "   Start height: $START_HEIGHT"
  echo ""

  local last_seen=0
  if [[ -n "$START_HEIGHT" ]]; then
    last_seen=$((START_HEIGHT - 1))
  fi

  while true; do
    local head
    head="$(network_head || true)"
    if [[ -z "$head" || ! "$head" =~ ^[0-9]+$ ]]; then
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] [warn] cannot read DA network head; retrying..."
      sleep "$INTERVAL"
      continue
    fi

    local from
    if (( last_seen == 0 )); then
      from=$((head > BACKFILL ? head - BACKFILL + 1 : 1))
    else
      from=$((last_seen + 1))
    fi

    local to
    if [[ "$ONCE" == "true" && -n "$START_HEIGHT" ]]; then
      to="$from"
    else
      to="$head"
      if (( from + MAX_PER_CYCLE - 1 < to )); then
        to=$((from + MAX_PER_CYCLE - 1))
      fi
    fi

    if (( from <= to )); then
      for ((h=from; h<=to; h++)); do
        query_one_height "$h"
      done
      last_seen=$to
    fi

    if [[ "$ONCE" == "true" ]]; then
      break
    fi

    sleep "$INTERVAL"
  done
}

main "$@"
