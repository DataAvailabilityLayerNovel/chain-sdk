#!/usr/bin/env bash
set -euo pipefail

# query_celestia_proof.sh — Get blob inclusion proof from Celestia DA layer
#
# This script queries Celestia for:
#   1. Blob data at a given DA height
#   2. NMT (Namespace Merkle Tree) inclusion proof for each blob
#   3. Optionally verifies the proof is non-empty
#
# Usage:
#   ./scripts/query_celestia_proof.sh --height <da_height> [--index <blob_index>] [--namespace <ns>]
#   ./scripts/query_celestia_proof.sh --height 620042
#   ./scripts/query_celestia_proof.sh --height 620042 --index 0
#   ./scripts/query_celestia_proof.sh --height 620042 --all

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
import binascii
import sys

value = sys.argv[1]
# If it looks like hex, decode directly
if all(c in '0123456789abcdefABCDEF' for c in value) and len(value) % 2 == 0:
    raw = binascii.unhexlify(value)
    print(base64.b64encode(raw).decode())
else:
    import hashlib
    h = hashlib.sha256(value.encode()).digest()[:10]
    raw = bytes([0]) + bytes(18) + h
    print(base64.b64encode(raw).decode())
PY
)"
  fi
fi
NAMESPACE="${NAMESPACE:-${DA_NAMESPACE_B64:-}}"
NAMESPACE="${NAMESPACE:-$DEFAULT_NAMESPACE}"

CELESTIA_RPC="${CELESTIA_BRIDGE_RPC:-${DA_BRIDGE_RPC:-${DA_RPC:-http://103.67.203.71:26758}}}"
AUTH_TOKEN="${DA_AUTH_TOKEN:-}"

HEIGHT=""
BLOB_INDEX=""
ALL_BLOBS="false"
VERIFY_ONLY="false"

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
import base64, binascii, sys
h = sys.argv[1]
raw = binascii.unhexlify(h)
print(base64.b64encode(raw).decode())
PY
    return 0
  fi
  echo "$input"
}

usage() {
  cat <<'EOF'
Usage:
  ./scripts/query_celestia_proof.sh --height <da_height> [options]

Options:
  --height <n>          DA height to query (required)
  --index <n>           Blob index to get proof for (default: 0)
  --all                 Get proofs for all blobs at this height
  --namespace <ns>      Override namespace (hex or base64)
  --verify              Only check if proof exists (exit 0/1)
  -h, --help            Show this help

Examples:
  ./scripts/query_celestia_proof.sh --height 620042
  ./scripts/query_celestia_proof.sh --height 620042 --index 0
  ./scripts/query_celestia_proof.sh --height 620042 --all
  ./scripts/query_celestia_proof.sh --height 620042 --verify
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd"
    exit 1
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --height)
        HEIGHT="${2:-}"
        shift 2
        ;;
      --index)
        BLOB_INDEX="${2:-}"
        shift 2
        ;;
      --all)
        ALL_BLOBS="true"
        shift
        ;;
      --namespace)
        NAMESPACE="${2:-}"
        shift 2
        ;;
      --verify)
        VERIFY_ONLY="true"
        shift
        ;;
      -h|--help|help)
        usage
        exit 0
        ;;
      *)
        if [[ -z "$HEIGHT" && "$1" =~ ^[0-9]+$ ]]; then
          HEIGHT="$1"
          shift
        else
          echo "[err] unknown argument: $1"
          usage
          exit 1
        fi
        ;;
    esac
  done

  if [[ -z "$HEIGHT" ]]; then
    echo "[err] --height is required"
    usage
    exit 1
  fi

  if [[ -n "$BLOB_INDEX" ]] && ! [[ "$BLOB_INDEX" =~ ^[0-9]+$ ]]; then
    echo "[err] --index must be a non-negative integer"
    exit 1
  fi
}

# Get all blobs at a height (to know how many exist)
get_blob_count() {
  local h="$1"
  local response
  response=$(curl -fsS -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{
      \"jsonrpc\": \"2.0\",
      \"id\": 1,
      \"method\": \"blob.GetAll\",
      \"params\": [$h, [\"$NAMESPACE\"]]
    }" 2>/dev/null) || { echo "0"; return; }

  echo "$response" | jq -r '.result | length // 0'
}

# Get proof for a specific blob
get_blob_proof() {
  local h="$1"
  local idx="$2"

  curl -fsS -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{
      \"jsonrpc\": \"2.0\",
      \"id\": 1,
      \"method\": \"blob.GetProof\",
      \"params\": [$h, \"$NAMESPACE\", $idx]
    }" 2>/dev/null
}

# Get blob data + commitment at index
get_blob_info() {
  local h="$1"
  local idx="$2"

  local response
  response=$(curl -fsS -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{
      \"jsonrpc\": \"2.0\",
      \"id\": 1,
      \"method\": \"blob.GetAll\",
      \"params\": [$h, [\"$NAMESPACE\"]]
    }" 2>/dev/null) || { echo "{}"; return; }

  echo "$response" | jq -r ".result[$idx] // {}"
}

# Get header at height (for data root)
get_header() {
  local h="$1"

  curl -fsS -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{
      \"jsonrpc\": \"2.0\",
      \"id\": 1,
      \"method\": \"header.GetByHeight\",
      \"params\": [$h]
    }" 2>/dev/null
}

print_proof_summary() {
  local h="$1"
  local idx="$2"
  local proof_json="$3"
  local blob_json="$4"

  local has_error
  has_error="$(echo "$proof_json" | jq -r '.error // empty')"
  if [[ -n "$has_error" ]]; then
    echo "  [err] proof request failed: $(echo "$proof_json" | jq -r '.error.message // .error')"
    return 1
  fi

  local proof_nodes
  proof_nodes="$(echo "$proof_json" | jq -r '.result | length // 0')"

  # Extract blob commitment
  local commitment
  commitment="$(echo "$blob_json" | jq -r '.commitment // "n/a"')"
  local blob_size
  blob_size="$(echo "$blob_json" | jq -r '.data | length // 0')"

  echo "  blob_index:    $idx"
  echo "  commitment:    $commitment"
  echo "  blob_size:     ${blob_size} bytes (base64)"
  echo "  proof_nodes:   $proof_nodes"

  if [[ "$proof_nodes" != "0" ]]; then
    echo "  proof_valid:   true (non-empty NMT proof)"
    # Show first and last proof node hashes
    local first_start last_end
    first_start="$(echo "$proof_json" | jq -r '.result[0].start // empty')"
    last_end="$(echo "$proof_json" | jq -r '.result[-1].end // empty')"
    if [[ -n "$first_start" ]]; then
      echo "  share_range:   start=$first_start end=$last_end"
    fi
    # Show proof nodes (abbreviated)
    echo "  proof_detail:"
    echo "$proof_json" | jq -r '.result[] | "    - start=\(.start) end=\(.end) nodes=\(.nodes | length)"'
  else
    echo "  proof_valid:   false (empty proof)"
    return 1
  fi
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

  # Get blob count first
  local blob_count
  blob_count="$(get_blob_count "$HEIGHT")"

  if [[ "$blob_count" == "0" ]]; then
    echo "[err] No blobs found at DA height=$HEIGHT for namespace"
    echo "      Namespace: $NAMESPACE"
    echo "      RPC: $CELESTIA_RPC"
    echo ""
    echo "[hint] Use ./scripts/query_celestia_blob_range.sh to find heights with blobs"
    exit 1
  fi

  echo "🔐 Celestia Blob Inclusion Proof"
  echo "   DA Height:  $HEIGHT"
  echo "   Namespace:  $NAMESPACE"
  echo "   Blobs:      $blob_count"
  echo "   RPC:        $CELESTIA_RPC"
  echo ""

  # Get header for data root
  local header_json
  header_json="$(get_header "$HEIGHT")"
  local data_hash
  data_hash="$(echo "$header_json" | jq -r '.result.dah.row_roots[0] // .result.header.data_hash // "n/a"' 2>/dev/null)"
  if [[ "$data_hash" != "n/a" && "$data_hash" != "null" && -n "$data_hash" ]]; then
    echo "   Data Root:  ${data_hash:0:32}..."
  fi
  echo ""

  # Determine which indices to query
  local indices=()
  if [[ "$ALL_BLOBS" == "true" ]]; then
    for ((i=0; i<blob_count; i++)); do
      indices+=("$i")
    done
  elif [[ -n "$BLOB_INDEX" ]]; then
    if (( BLOB_INDEX >= blob_count )); then
      echo "[err] blob index $BLOB_INDEX out of range (only $blob_count blobs at this height)"
      exit 1
    fi
    indices=("$BLOB_INDEX")
  else
    indices=("0")
  fi

  local all_valid=true
  for idx in "${indices[@]}"; do
    echo "--- Proof for blob[$idx] ---"

    local proof_json blob_json
    proof_json="$(get_blob_proof "$HEIGHT" "$idx")"
    blob_json="$(get_blob_info "$HEIGHT" "$idx")"

    if ! print_proof_summary "$HEIGHT" "$idx" "$proof_json" "$blob_json"; then
      all_valid=false
    fi
    echo ""
  done

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ "$all_valid" == "true" ]]; then
      echo "[ok] All proofs valid"
      exit 0
    else
      echo "[fail] Some proofs are invalid or missing"
      exit 1
    fi
  fi

  # Print raw proof JSON for the first/specified blob (useful for programmatic use)
  if [[ "${#indices[@]}" -eq 1 ]]; then
    echo "📋 Raw proof JSON (for programmatic use):"
    get_blob_proof "$HEIGHT" "${indices[0]}" | jq .
  fi
}

main "$@"
