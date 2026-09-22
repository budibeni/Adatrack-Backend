#!/usr/bin/env bash
# ============================================================================
# test.sh — run unit + integration tests for every Go module (PRD §16)
# ============================================================================
# Usage:
#   scripts/test.sh              # all modules
#   scripts/test.sh internal     # one module
#   scripts/test.sh --race
#   scripts/test.sh --e2e        # also run the live pipeline E2E script
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MODULES=(internal services/ingestion-tcp services/worker-live services/worker-persistence services/worker-alert services/service-websocket services/api-vehicle services/service-media services/foundation-check)
flags=()
target=""
run_e2e=false
for arg in "$@"; do
  case "$arg" in
    --race) flags+=("-race") ;;
    --e2e)  run_e2e=true ;;
    *)      target="$arg" ;;
  esac
done

run_module() {
  local mod="$1"
  echo "test: === ${mod} ==="
  (cd "$ROOT/$mod" && go vet ./... && go test ${flags[@]+"${flags[@]}"} -count=1 ./...)
}

if [[ -n "$target" ]]; then
  run_module "$target"
else
  for mod in "${MODULES[@]}"; do
    run_module "$mod"
  done
fi

if [[ "$run_e2e" == true ]]; then
  echo "test: === pipeline E2E ==="
  "$ROOT/scripts/e2e-pipeline.sh"
fi

echo "test: done"