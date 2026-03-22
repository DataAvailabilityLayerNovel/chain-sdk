#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

DA_URL="${DA_RPC:-${DA_BRIDGE_RPC:-}}"
DA_TOKEN="${DA_AUTH_TOKEN:-}"
DA_NAMESPACE_VALUE="${1:-${DA_NAMESPACE:-rollup}}"
SCAN_RANGE="${SCAN_RANGE:-80}"
DA_DEBUG_BIN="${DA_DEBUG_BIN:-/tmp/da-debug}"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err][da-check] missing command: $cmd"
    exit 1
  fi
}

require_cmd curl
require_cmd jq
require_cmd go

if [[ -z "$DA_URL" ]]; then
  echo "[err][da-check] DA_RPC/DA_BRIDGE_RPC is empty"
  exit 1
fi

if [[ ! -x "$DA_DEBUG_BIN" ]]; then
  (cd "$ROOT_DIR/tools/da-debug" && go build -o "$DA_DEBUG_BIN" .)
fi

HEAD="$(curl -sS --connect-timeout 2 --max-time 8 -H "Authorization: Bearer $DA_TOKEN" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"header.NetworkHead","params":[]}' \
  "$DA_URL" | jq -r '.result.height // .result.header.height // .result.commit.height // empty')"

if [[ -z "$HEAD" ]]; then
  echo "[err][da-check] cannot read DA network head"
  exit 1
fi

START=$((HEAD > SCAN_RANGE ? HEAD - SCAN_RANGE : 1))

echo "[run][da-check] da_url=$DA_URL"
echo "[run][da-check] namespace_input=$DA_NAMESPACE_VALUE"
echo "[run][da-check] scan_range=$START..$HEAD"

FOUND=0
for h in $(seq "$START" "$HEAD"); do
  OUT="$($DA_DEBUG_BIN query "$h" "$DA_NAMESPACE_VALUE" --da-url "$DA_URL" --auth-token "$DA_TOKEN" --timeout 8s 2>/dev/null || true)"
  if echo "$OUT" | grep -Eq '^Found [1-9]'; then
    FOUND=1
    echo "[ok][da-check] blobs_found_at_da_height=$h"
    echo "$OUT" | sed -n '1,20p'
    echo "---"
  fi
done

if [[ "$FOUND" -eq 0 ]]; then
  echo "[result][da-check] no blobs found in range"
  exit 2
fi

echo "[result][da-check] found DA blobs in namespace"
