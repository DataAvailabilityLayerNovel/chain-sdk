#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/encode-namespace.sh --text <namespace>
  ./scripts/encode-namespace.sh --hex <hex_namespace>

Options:
  --text <value>   Namespace text (e.g. defSensor, rollup)
  --hex <value>    Raw namespace bytes in hex (with or without 0x)
  --size <n>       Total namespace byte size before base64 (default: 29)

Notes:
  - Output is base64 of fixed-size namespace bytes.
  - Input bytes are right-aligned and left-padded with zero bytes.
  - Default size=29 to match current Celestia namespace format used in this repo.

Examples:
  ./scripts/encode-namespace.sh --text defSensor
  # -> AAAAAAAAAAAAAAAAAAAAAAAAAABkZWZTZW5zb3I=

  ./scripts/encode-namespace.sh --text rollup
  ./scripts/encode-namespace.sh --hex 64656653656e736f72
EOF
}

TEXT=""
HEX=""
SIZE="29"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --text)
      TEXT="${2:-}"
      shift 2
      ;;
    --hex)
      HEX="${2:-}"
      shift 2
      ;;
    --size)
      SIZE="${2:-}"
      shift 2
      ;;
    -h|--help|help)
      usage
      exit 0
      ;;
    *)
      echo "[err] unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -n "$TEXT" && -n "$HEX" ]]; then
  echo "[err] use only one of --text or --hex" >&2
  exit 1
fi

if [[ -z "$TEXT" && -z "$HEX" ]]; then
  echo "[err] one of --text or --hex is required" >&2
  usage
  exit 1
fi

if ! [[ "$SIZE" =~ ^[0-9]+$ ]] || [[ "$SIZE" -le 0 ]]; then
  echo "[err] --size must be a positive integer" >&2
  exit 1
fi

python3 - <<'PY' "$TEXT" "$HEX" "$SIZE"
import base64
import re
import sys

text = sys.argv[1]
hex_input = sys.argv[2]
size = int(sys.argv[3])

if text:
    raw = text.encode("utf-8")
else:
    h = hex_input.strip().lower()
    if h.startswith("0x"):
        h = h[2:]
    if not re.fullmatch(r"[0-9a-f]*", h):
        print("[err] --hex contains non-hex characters", file=sys.stderr)
        sys.exit(1)
    if len(h) % 2 != 0:
        print("[err] --hex length must be even", file=sys.stderr)
        sys.exit(1)
    raw = bytes.fromhex(h)

if len(raw) > size:
    print(f"[err] input too long: {len(raw)} bytes (max {size})", file=sys.stderr)
    sys.exit(1)

padded = (b"\x00" * (size - len(raw))) + raw
print(base64.b64encode(padded).decode("ascii"))
PY
