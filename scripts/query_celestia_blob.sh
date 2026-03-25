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
HEIGHT=""
TX_HASH=""
BLOCK_HEIGHT=""

CELESTIA_RPC="${CELESTIA_BRIDGE_RPC:-http://131.153.224.169:26758}"
AUTH_TOKEN="${DA_AUTH_TOKEN:-}"
CHAIN_LOG_FILE="${CHAIN_LOG_FILE:-.logs/cosmos-wasm-chain.log}"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/query_celestia_blob.sh --height <da_height> [--namespace <base64_ns>]
  ./scripts/query_celestia_blob.sh --tx-hash <tx_hash> [--namespace <base64_ns>]
  ./scripts/query_celestia_blob.sh --block-height <ev_height> [--namespace <base64_ns>]
  ./scripts/query_celestia_blob.sh --latest [--namespace <base64_ns>]

Notes:
  - Priority resolve height: --height > --tx-hash > --block-height > --latest(default).
  - If no args are provided, script first tries latest blob_height from CHAIN_LOG_FILE,
    then falls back to latest block DA height from chain RPC.
  - When using --tx-hash, script calls ./scripts/contracts/wasm-rpc.sh tx --hash ...
    and uses data_da/header_da returned by chain.
  - Requires jq and curl.

Examples:
  ./scripts/query_celestia_blob.sh --tx-hash C1AEC991E34C280429DE751ED7DDBBC202EF0C07AEE97BC3D1563FC1CCE12607
  ./scripts/query_celestia_blob.sh --block-height 42
  ./scripts/query_celestia_blob.sh --height 620070
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
      --height)
        HEIGHT="${2:-}"
        shift 2
        ;;
      --tx-hash)
        TX_HASH="${2:-}"
        shift 2
        ;;
      --block-height)
        BLOCK_HEIGHT="${2:-}"
        shift 2
        ;;
      --latest)
        HEIGHT="latest"
        shift
        ;;
      --namespace)
        NAMESPACE="${2:-}"
        shift 2
        ;;
      -h|--help|help)
        usage
        exit 0
        ;;
      *)
        if [[ -z "$HEIGHT" ]]; then
          HEIGHT="$1"
          shift
          continue
        fi
        if [[ "$NAMESPACE" == "$DEFAULT_NAMESPACE" ]]; then
          NAMESPACE="$1"
          shift
          continue
        fi
        echo "[err] unknown argument: $1"
        usage
        exit 1
        ;;
    esac
  done
}

resolve_da_height() {
  local rpc_script="$ROOT_DIR/scripts/contracts/wasm-rpc.sh"

  if [[ -n "$HEIGHT" && "$HEIGHT" != "latest" ]]; then
    echo "$HEIGHT"
    return 0
  fi

  if [[ -n "$TX_HASH" ]]; then
    local tx_json
    tx_json="$("$rpc_script" tx --hash "$TX_HASH")"
    local found
    found="$(echo "$tx_json" | jq -r '.found // false')"
    if [[ "$found" != "true" ]]; then
      echo "[err] tx not found on chain: $TX_HASH" >&2
      echo "[hint] submit tx first, then retry in a few seconds" >&2
      return 1
    fi

    local da_from_tx
    da_from_tx="$(echo "$tx_json" | jq -r '(.data_da // .data_da_height // .header_da // .header_da_height // empty)')"
    if [[ -z "$da_from_tx" || "$da_from_tx" == "null" ]]; then
      echo "[err] cannot resolve DA height from tx lookup" >&2
      return 1
    fi
    echo "$da_from_tx"
    return 0
  fi

  if [[ -n "$BLOCK_HEIGHT" ]]; then
    local block_json
    block_json="$("$rpc_script" block --height "$BLOCK_HEIGHT")"
    local da_from_block
    da_from_block="$(echo "$block_json" | jq -r '(.data_da_height // .header_da_height // empty)')"
    if [[ -z "$da_from_block" || "$da_from_block" == "null" ]]; then
      echo "[err] cannot resolve DA height from block: $BLOCK_HEIGHT" >&2
      return 1
    fi
    echo "$da_from_block"
    return 0
  fi

  local log_height
  log_height="$(resolve_da_height_from_log || true)"
  if [[ -n "$log_height" ]]; then
    echo "$log_height"
    return 0
  fi

  local latest_json
  latest_json="$("$rpc_script" latest-block)"
  local da_from_latest
  da_from_latest="$(echo "$latest_json" | jq -r '(.data_da_height // .header_da_height // empty)')"
  if [[ -z "$da_from_latest" || "$da_from_latest" == "null" ]]; then
    echo "[err] cannot resolve DA height from latest block" >&2
    return 1
  fi
  echo "$da_from_latest"
}

resolve_da_height_from_log() {
  local log_path="$CHAIN_LOG_FILE"
  if [[ ! "$log_path" = /* ]]; then
    log_path="$ROOT_DIR/$log_path"
  fi

  if [[ ! -f "$log_path" ]]; then
    return 1
  fi

  local h
  h="$(awk '
    {
      line=$0
      if (line ~ /cosmos-da-submit-celestia/ && match(line, /da_height[=:][[:space:]]*([0-9]+)/)) {
        s=substr(line, RSTART, RLENGTH)
        gsub(/[^0-9]/, "", s)
        if (s != "") latest=s
      }
    }
    END {
      if (latest != "") print latest
    }
  ' "$log_path")"

  if [[ -z "$h" ]]; then
    h="$(awk '
      {
        line=$0
        if (match(line, /blob_height[=:[:space:]]+([0-9]+)/)) {
          s=substr(line, RSTART, RLENGTH)
          gsub(/[^0-9]/, "", s)
          if (s != "") latest=s
        }
      }
      END {
        if (latest != "") print latest
      }
    ' "$log_path")"
  fi

  if [[ -z "$h" ]]; then
    return 1
  fi

  echo "$h"
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

  local resolved_height
  resolved_height="$(resolve_da_height)"

  if ! [[ "$resolved_height" =~ ^[0-9]+$ ]]; then
    echo "[err] invalid DA height resolved: $resolved_height"
    exit 1
  fi

  echo "🔍 Querying Celestia blob..."
  echo "   DA Height: $resolved_height"
  if [[ -n "$HEIGHT" && "$HEIGHT" != "latest" ]]; then
    echo "   Source: explicit height"
  elif [[ -n "$TX_HASH" ]]; then
    echo "   Source TX: $TX_HASH"
  elif [[ -n "$BLOCK_HEIGHT" ]]; then
    echo "   Source Block: $BLOCK_HEIGHT"
  else
    echo "   Source: latest block"
  fi
  echo "   Namespace: $NAMESPACE"
  echo "   RPC: $CELESTIA_RPC"
  echo "   Chain log: $CHAIN_LOG_FILE"
  echo ""

  local response
  response=$(curl -fsS -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{
      \"jsonrpc\": \"2.0\",
      \"id\": 1,
      \"method\": \"blob.GetAll\",
      \"params\": [
        $resolved_height,
        [\"$NAMESPACE\"]
      ]
    }")

  echo "📦 Response:"
  echo "$response" | jq .

  local blob_count
  blob_count="$(echo "$response" | jq -r '.result | length // 0')"
  if [[ "$blob_count" == "0" ]]; then
    echo ""
    echo "❌ No blob found at DA height=$resolved_height for namespace=$NAMESPACE"
    exit 1
  fi

  echo ""
  echo "📝 Decoded data:"
  local idx
  for ((idx=0; idx<blob_count; idx++)); do
    local b64_data
    b64_data="$(echo "$response" | jq -r ".result[$idx].data")"
    echo "--- blob[$idx] ---"
    decode_blob_data "$b64_data" || true
  done
}

main "$@"
