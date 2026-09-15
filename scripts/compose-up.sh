#!/usr/bin/env bash
# ============================================================================
# compose-up.sh — variant-aware docker compose wrapper (PRD §7 / §14.1)
# ============================================================================
# Usage:
#   scripts/compose-up.sh [local|coolify] <compose args...>
#
# Examples:
#   scripts/compose-up.sh up -d                 # LOCAL (default) stack up
#   scripts/compose-up.sh local ps
#   scripts/compose-up.sh coolify config
#   COMPOSE_VARIANT=coolify scripts/compose-up.sh up -d
#
# Guarantees the "no mixed config" rule (§14.1): exactly ONE variant's compose
# file AND env file are ever passed to docker compose.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

variant="${COMPOSE_VARIANT:-local}"
if [[ $# -gt 0 && ( "$1" == "local" || "$1" == "coolify" ) ]]; then
  variant="$1"
  shift
fi

case "$variant" in
  local)
    compose_file="$ROOT/docker-compose.local.yml"
    env_file="$ROOT/.env.local"
    ;;
  coolify)
    compose_file="$ROOT/deployments/docker-compose.coolify.yml"
    env_file="$ROOT/.env.coolify"
    ;;
  *)
    echo "compose-up: unknown variant '$variant' (expected: local|coolify)" >&2
    exit 1
    ;;
esac

if [[ ! -f "$compose_file" ]]; then
  echo "compose-up: missing compose file: $compose_file" >&2
  exit 1
fi
if [[ ! -f "$env_file" ]]; then
  echo "compose-up: missing env file: $env_file" >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "compose-up: docker CLI not found" >&2
  exit 1
fi

echo "compose-up: variant=$variant file=$(basename "$compose_file") env=$(basename "$env_file")"
exec docker compose -f "$compose_file" --env-file "$env_file" "$@"