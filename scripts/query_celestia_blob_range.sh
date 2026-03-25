#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

DEFAULT_NAMESPACE="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA="
NAMESPACE=""
if [[ -n "${DA_NAMESPACE:-}" ]]; then
  if command -v python3 >/dev/null 2>&1; then
    NAMESPACE="$(python3 - <<'PY' "${DA_NAMESPACE}"
import base64
import hashlib
import sys

value = sys.argv[1]
h = hashlib.sha256(value.encode()).digest()[:10]
raw = bytes([0]) + bytes(18) + h
print(base64.b64encode(raw).decode())
PY
)"
  fi
fi
NAMESPACE="${NAMESPACE:-${DA_NAMESPACE_B64:-}}"
NAMESPACE="${NAMESPACE:-$DEFAULT_NAMESPACE}"
FROM_HEIGHT=""
TO_HEIGHT=""

CELESTIA_RPC="${CELESTIA_BRIDGE_RPC:-${DA_BRIDGE_RPC:-${DA_RPC:-http://131.153.224.169:26758}}}"
AUTH_TOKEN="${DA_AUTH_TOKEN:-}"

normalize_namespace() {
  local input="$1"

  if [[ -z "$input" ]]; then
    echo "$input"
    return 0
  fi

  if [[ "$input" =~ ^(0x)?[0-9a-fA-F]+$ ]]; then
    local hex="$input"
    hex="${hex#0x}"
    hex="${hex#0X}"

    if (( ${#hex} % 2 != 0 )); then
      echo "[err] invalid hex namespace length (must be even): $input" >&2
      exit 1
    fi

    python3 - <<'PY' "$hex"
import base64
import binascii
import sys

h = sys.argv[1]
try:
    raw = binascii.unhexlify(h)
except binascii.Error as e:
    print(f"[err] invalid hex namespace: {e}", file=sys.stderr)
    sys.exit(1)

print(base64.b64encode(raw).decode())
PY
    return 0
  fi

  echo "$input"
}

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
  python3 - <<'PY' "$b64_data"
import base64
import hashlib
import json
import sys

b64 = sys.argv[1]

try:
    raw = base64.b64decode(b64, validate=False)
except Exception:
    print("[err] cannot decode base64 data")
    sys.exit(1)

if not raw:
    print("[err] cannot decode base64 data")
    sys.exit(1)

text = None
try:
    text = raw.decode("utf-8")
except UnicodeDecodeError:
    pass

if text is not None:
    stripped = text.strip()
    if stripped:
        try:
            obj = json.loads(text)
            print(json.dumps(obj, indent=2, ensure_ascii=False))
            sys.exit(0)
        except Exception:
            printable_ratio = sum((ch.isprintable() or ch in "\n\r\t") for ch in text) / max(len(text), 1)
            if printable_ratio >= 0.95:
                print(text)
                sys.exit(0)

print(f"[binary] bytes={len(raw)} sha256={hashlib.sha256(raw).hexdigest()}")
print(f"[binary] head_hex={raw[:64].hex()}")
PY
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
    decode_blob_data "$b64_data" | LC_ALL=C sed 's/^/    /'
  done
}

main() {
  require_cmd jq
  require_cmd curl
  require_cmd python3
  parse_args "$@"

  NAMESPACE="$(normalize_namespace "$NAMESPACE")"

  if [[ -z "$AUTH_TOKEN" ]]; then
    echo "[err] DA_AUTH_TOKEN is required"
    echo "[hint] export DA_AUTH_TOKEN=... (or set in .env)"
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
