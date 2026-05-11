#!/usr/bin/env bash
set -euo pipefail

# query_celestia_status.sh — Check Celestia DA node status
#
# Shows:
#   - Network head height
#   - Local head height (how far synced)
#   - Sync status (behind or caught up)
#   - Node info
#
# Usage:
#   ./scripts/query_celestia_status.sh

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

CELESTIA_RPC="${CELESTIA_BRIDGE_RPC:-${DA_BRIDGE_RPC:-${DA_RPC:-http://103.67.203.71:26758}}}"
AUTH_TOKEN="${DA_AUTH_TOKEN:-}"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd"
    exit 1
  fi
}

rpc_call() {
  local method="$1"
  local params="${2:-[]}"

  curl -fsS -X POST "$CELESTIA_RPC" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$method\",\"params\":$params}" 2>/dev/null
}

main() {
  require_cmd jq
  require_cmd curl

  if [[ -z "$AUTH_TOKEN" ]]; then
    echo "[err] DA_AUTH_TOKEN is required"
    echo "[hint] export DA_AUTH_TOKEN=... (or set in .env)"
    exit 1
  fi

  echo "📡 Celestia DA Node Status"
  echo "   RPC: $CELESTIA_RPC"
  echo ""

  # Network head
  local net_head_json
  net_head_json="$(rpc_call "header.NetworkHead" || echo '{}')"
  local net_height
  net_height="$(echo "$net_head_json" | jq -r '.result.header.height // .result.height // "?"')"
  local net_time
  net_time="$(echo "$net_head_json" | jq -r '.result.header.time // .result.time // "?"')"

  echo "   Network Head:"
  echo "     height: $net_height"
  echo "     time:   $net_time"
  echo ""

  # Local head
  local local_head_json
  local_head_json="$(rpc_call "header.LocalHead" || echo '{}')"
  local local_height
  local_height="$(echo "$local_head_json" | jq -r '.result.header.height // .result.height // "?"')"
  local local_time
  local_time="$(echo "$local_head_json" | jq -r '.result.header.time // .result.time // "?"')"

  echo "   Local Head:"
  echo "     height: $local_height"
  echo "     time:   $local_time"
  echo ""

  # Sync status
  if [[ "$net_height" =~ ^[0-9]+$ && "$local_height" =~ ^[0-9]+$ ]]; then
    local behind=$((net_height - local_height))
    if (( behind <= 2 )); then
      echo "   Sync: ✅ synced (behind $behind blocks)"
    else
      echo "   Sync: ⏳ syncing... ($behind blocks behind)"
    fi
  else
    echo "   Sync: ❓ unknown"
  fi
  echo ""

  # Node info (p2p)
  local info_json
  info_json="$(rpc_call "p2p.Info" || echo '{}')"
  local peer_id
  peer_id="$(echo "$info_json" | jq -r '.result.ID // "?"')"
  local num_peers
  num_peers="$(echo "$info_json" | jq -r '.result.Addrs | length // 0' 2>/dev/null || echo '?')"

  echo "   Node:"
  echo "     peer_id: ${peer_id:0:16}..."
  echo "     addrs:   $num_peers"
  echo ""

  # DA sampling stats (if available)
  local sampling_json
  sampling_json="$(rpc_call "das.SamplingStats" || echo '{}')"
  local sampled_headers
  sampled_headers="$(echo "$sampling_json" | jq -r '.result.head_of_sampled_chain // .result.SampledChainHead // "?"' 2>/dev/null)"
  local catchup_head
  catchup_head="$(echo "$sampling_json" | jq -r '.result.head_of_catchup // .result.CatchupHead // "?"' 2>/dev/null)"

  if [[ "$sampled_headers" != "?" && "$sampled_headers" != "null" ]]; then
    echo "   DAS (Data Availability Sampling):"
    echo "     sampled_head: $sampled_headers"
    echo "     catchup_head: $catchup_head"
  fi
}

main "$@"
