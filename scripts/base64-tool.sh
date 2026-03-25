#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/base64-tool.sh encode [--text <value> | --file <path>]
  ./scripts/base64-tool.sh decode [--text <base64> | --file <path>] [--raw]

Notes:
  - If no --text/--file is provided, reads from stdin.
  - decode default tries to pretty-print JSON if possible.
  - use --raw to print decoded bytes as-is.

Examples:
  ./scripts/base64-tool.sh encode --text 'hello'
  echo -n 'hello' | ./scripts/base64-tool.sh encode
  ./scripts/base64-tool.sh decode --text 'aGVsbG8='
  ./scripts/base64-tool.sh decode --file /tmp/payload.b64 --raw
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[err] missing command: $cmd" >&2
    exit 1
  fi
}

read_input() {
  local text="${1:-}"
  local file="${2:-}"

  if [[ -n "$text" && -n "$file" ]]; then
    echo "[err] use only one of --text or --file" >&2
    exit 1
  fi

  if [[ -n "$text" ]]; then
    printf "%s" "$text"
    return 0
  fi

  if [[ -n "$file" ]]; then
    if [[ ! -f "$file" ]]; then
      echo "[err] file not found: $file" >&2
      exit 1
    fi
    cat "$file"
    return 0
  fi

  cat
}

cmd_encode() {
  local text=""
  local file=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --text)
        text="${2:-}"
        shift 2
        ;;
      --file)
        file="${2:-}"
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

  read_input "$text" "$file" | base64
}

cmd_decode() {
  local text=""
  local file=""
  local raw="false"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --text)
        text="${2:-}"
        shift 2
        ;;
      --file)
        file="${2:-}"
        shift 2
        ;;
      --raw)
        raw="true"
        shift
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

  local decoded
  if ! decoded="$(read_input "$text" "$file" | base64 --decode 2>/dev/null)"; then
    echo "[err] invalid base64 input" >&2
    exit 1
  fi

  if [[ "$raw" == "true" ]]; then
    printf "%s" "$decoded"
    return 0
  fi

  if command -v jq >/dev/null 2>&1 && printf "%s" "$decoded" | jq -e . >/dev/null 2>&1; then
    printf "%s" "$decoded" | jq .
    return 0
  fi

  printf "%s\n" "$decoded"
}

main() {
  require_cmd base64

  if [[ $# -lt 1 ]]; then
    usage
    exit 1
  fi

  local action="$1"
  shift

  case "$action" in
    encode)
      cmd_encode "$@"
      ;;
    decode)
      cmd_decode "$@"
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      echo "[err] unknown action: $action" >&2
      usage
      exit 1
      ;;
  esac
}

main "$@"
