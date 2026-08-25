#!/usr/bin/env bash
# ============================================================================
# compose-up.sh — wrapper `docker compose` sadar-provider (PRD §7.1.1).
#
# Sumber kebenaran provider = DATABASE_PROVIDER di backend/.env (OS env menang
# atas file, konsisten dengan internal.LoadProjectEnv()). Wrapper ini:
#   1. membaca & memvalidasi nilainya (hanya mysql|postgres — nilai lain gagal
#      cepat agar tidak ada database yang nyasar up);
#   2. men-set COMPOSE_PROFILES=<provider> sehingga `docker compose up -d`
#      HANYA men-start database yang dipilih (Redis & NATS selalu up);
#   3. meneruskan seluruh argumen ke docker compose dengan --env-file
#      backend/.env; -f backend/docker-compose.yml ditambahkan otomatis bila
#      pemanggil tidak menyediakan -f sendiri dan CWD bukan backend/.
#
# Contoh:
#   backend/scripts/compose-up.sh up -d        # infra sesuai provider di .env
#   backend/scripts/compose-up.sh ps           # status service aktif
#   DATABASE_PROVIDER=mysql backend/scripts/compose-up.sh up -d   # override OS
#
# Override file env: ENV_FILE=/path/.env backend/scripts/compose-up.sh ...
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${ENV_FILE:-$BACKEND_DIR/.env}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "[compose-up] ERROR: env file tidak ditemukan: $ENV_FILE" >&2
  exit 1
fi

provider=""
if [[ -n "${DATABASE_PROVIDER:-}" ]]; then
  provider="$DATABASE_PROVIDER" # OS env menang atas file .env (sama seperti loader Go)
else
  # Ambil baris TERAKHIR DATABASE_PROVIDER=..., buang komentar/quote/spasi.
  provider="$(grep -E '^[[:space:]]*DATABASE_PROVIDER[[:space:]]*=' "$ENV_FILE" \
    | tail -n1 \
    | sed -E 's/^[^=]*=[[:space:]]*//' \
    | tr -d '"' | tr -d "'" | tr -d '[:space:]')"
fi
provider="${provider:-postgres}" # default PROYEK = postgres (PRD §7.1.1)
provider="$(printf '%s' "$provider" | tr '[:upper:]' '[:lower:]')"

case "$provider" in
  mysql|postgres) ;;
  *)
    echo "[compose-up] ERROR: DATABASE_PROVIDER='$provider' tidak didukung (gunakan mysql|postgres). Tidak ada DB yang di-up." >&2
    exit 1
    ;;
esac

export COMPOSE_PROFILES="$provider"

extra_args=()
has_file_flag=0
for a in "$@"; do
  case "$a" in
    -f|--file|-f=*|--file=*) has_file_flag=1 ;;
  esac
done
if [[ $has_file_flag -eq 0 ]]; then
  extra_args+=( -f "$BACKEND_DIR/docker-compose.yml" )
fi

echo "[compose-up] DATABASE_PROVIDER=$provider -> COMPOSE_PROFILES=$provider"
exec docker compose --env-file "$ENV_FILE" --project-directory "$BACKEND_DIR" \
  ${extra_args[@]+"${extra_args[@]}"} "$@"
