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
FROM_HEIGHT=""
TO_HEIGHT=""

CELESTIA_RPC="${CELESTIA_BRIDGE_RPC:-http://131.153.224.169:26758}"
AUTH_TOKEN="${DA_AUTH_TOKEN:-}"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/query_celestia_blob_range.sh --from-height <start> --to-height <end> [--namespace <base64_ns>]

Examples:
  ./scripts/query_celestia_blob_range.sh --from-height 31100 --to-height 31120
  ./scripts/query_celestia_blob_range.sh --from-height 620000 --to-height 620050 --namespace AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=
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
      --from-height)
        FROM_HEIGHT="${2:-}"
        shift 2
        ;;
      --to-height)
        TO_HEIGHT="${2:-}"
        shift 2
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
        echo "[err] unknown argument: $1"
        usage
        exit 1
        ;;
    esac
  done

  if [[ -z "$FROM_HEIGHT" || -z "$TO_HEIGHT" ]]; then
    echo "[err] --from-height and --to-height are required"
    usage
    exit 1
  fi

  if ! [[ "$FROM_HEIGHT" =~ ^[0-9]+$ && "$TO_HEIGHT" =~ ^[0-9]+$ ]]; then
    echo "[err] heights must be positive integers"
    exit 1
  fi

  if (( FROM_HEIGHT > TO_HEIGHT )); then
    echo "[err] --from-height must be <= --to-height"
    exit 1
  fi
}

query_height() {
  local h="$1"
  local response
  local http_code
  local payload

  payload="$(jq -nc --argjson h "$h" --arg ns "$NAMESPACE" '{jsonrpc:"2.0", id:1, method:"blob.GetAll", params:[$h,[$ns]]}')"

  response=$(curl -sS -w '\n%{http_code}' -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "$payload")

  http_code="$(printf '%s' "$response" | tail -n 1)"
  response="$(printf '%s' "$response" | sed '$d')"

  if [[ "$http_code" != "200" ]]; then
    echo "[h=$h] rpc_http_error=$http_code"
    return 0
  fi

  if ! echo "$response" | jq -e . >/dev/null 2>&1; then
    echo "[h=$h] rpc_invalid_json"
    return 0
  fi

  local rpc_error
  rpc_error="$(echo "$response" | jq -r '.error.message // empty')"
  if [[ -n "$rpc_error" ]]; then
    echo "[h=$h] rpc_error=$rpc_error"
    return 0
  fi

  local blob_count
  blob_count="$(echo "$response" | jq -r '.result | length // 0')"

  if [[ "$blob_count" == "0" ]]; then
    echo "[h=$h] no blob"
    return 0
  fi

  echo "[h=$h] blobs=$blob_count"
  local idx
  for ((idx=0; idx<blob_count; idx++)); do
    local b64_data
    b64_data="$(echo "$response" | jq -r ".result[$idx].data")"
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

  echo "🔍 Querying Celestia blobs in range"
  echo "   Range: $FROM_HEIGHT..$TO_HEIGHT"
  echo "   Namespace: $NAMESPACE"
  echo "   RPC: $CELESTIA_RPC"
  echo ""

  local h
  for ((h=FROM_HEIGHT; h<=TO_HEIGHT; h++)); do
    query_height "$h"
  done
}

main "$@"
