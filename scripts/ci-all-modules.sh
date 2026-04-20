#!/usr/bin/env bash
# ci-all-modules.sh — Unified CI for all go.mod modules.
#
# Runs vet, build, and test across every go module in the repo.
# Detects dependency drift by checking go.sum freshness.
#
# Usage:
#   ./scripts/ci-all-modules.sh          # run all checks
#   ./scripts/ci-all-modules.sh test     # test only
#   ./scripts/ci-all-modules.sh vet      # vet only
#   ./scripts/ci-all-modules.sh build    # build only
#   ./scripts/ci-all-modules.sh tidy     # tidy check only

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-all}"
FAILURES=0
MODULES=()

# Discover all modules.
while IFS= read -r gomod; do
    MODULES+=("$(dirname "$gomod")")
done < <(find "$ROOT_DIR" -name go.mod -not -path '*/vendor/*' | sort)

echo "=== Found ${#MODULES[@]} Go modules ==="
for mod in "${MODULES[@]}"; do
    echo "  $(realpath --relative-to="$ROOT_DIR" "$mod")"
done
echo ""

run_in_module() {
    local mod_dir="$1"
    shift
    local rel
    rel="$(realpath --relative-to="$ROOT_DIR" "$mod_dir")"

    echo "--- [$rel] $* ---"
    if (cd "$mod_dir" && "$@"); then
        echo "    ✓ $rel"
    else
        echo "    ✗ $rel FAILED"
        FAILURES=$((FAILURES + 1))
    fi
}

# Tidy check: ensure go.sum is up-to-date.
do_tidy() {
    echo "=== Phase: tidy check ==="
    for mod in "${MODULES[@]}"; do
        run_in_module "$mod" bash -c '
            cp go.sum go.sum.bak 2>/dev/null || true
            go mod tidy 2>&1
            if ! diff -q go.sum go.sum.bak >/dev/null 2>&1; then
                echo "go.sum is out of date — run: go mod tidy"
                mv go.sum.bak go.sum 2>/dev/null || true
                exit 1
            fi
            rm -f go.sum.bak
        '
    done
    echo ""
}

# Vet: static analysis.
do_vet() {
    echo "=== Phase: go vet ==="
    for mod in "${MODULES[@]}"; do
        run_in_module "$mod" go vet ./...
    done
    echo ""
}

# Build: compile check.
do_build() {
    echo "=== Phase: go build ==="
    for mod in "${MODULES[@]}"; do
        run_in_module "$mod" go build ./...
    done
    echo ""
}

# Test: unit tests.
do_test() {
    echo "=== Phase: go test ==="
    for mod in "${MODULES[@]}"; do
        run_in_module "$mod" go test -count=1 -timeout=120s ./...
    done
    echo ""
}

case "$MODE" in
    tidy)  do_tidy ;;
    vet)   do_vet ;;
    build) do_build ;;
    test)  do_test ;;
    all)
        do_tidy
        do_vet
        do_build
        do_test
        ;;
    *)
        echo "Usage: $0 {all|tidy|vet|build|test}"
        exit 1
        ;;
esac

echo "========================================"
if [ "$FAILURES" -gt 0 ]; then
    echo "FAILED: $FAILURES module(s) had errors"
    exit 1
else
    echo "ALL PASSED (${#MODULES[@]} modules)"
fi
