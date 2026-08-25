#!/bin/bash
# ============================================================================
# 02b-seed-reference.sh — MASTER reference data untuk PostgreSQL (PRD §7.1.1).
#
# Paritas dengan jalur MySQL (database/init/00-multitenant-init.sh langkah 2b):
# meng-apply database/seed/reference/*.sql (001 countries s/d 005 subdistricts,
# dihasilkan tools/genregions dari data real BPS/mledoze/kodepos) ke schema
# master (adatrack_gps_master).
#
# Catatan kompatibilitas: file seed dibuat utk MySQL dan mengakhir tiap
# statement dengan `ON DUPLICATE KEY UPDATE col=VALUES(col)...`. Konstruksi itu
# tidak ada di PostgreSQL → ditransform on-the-fly menjadi
# `ON CONFLICT DO NOTHING` (idempoten aman re-run; data referensi statis).
# Tiap file dieksekusi dalam SATU transaksi (-1) dgn ON_ERROR_STOP.
#
# Sumber file di container: volume ./database/seed → /db/seed (compose).
# ============================================================================
set -e

SEED_DIR="/db/seed/reference"
if [ ! -d "$SEED_DIR" ]; then
  echo "[seed-reference] $SEED_DIR tidak tersedia — skip."
  exit 0
fi

# Semua tabel referensi hidup di schema master.
export PGOPTIONS="-c search_path=adatrack_gps_master"

for f in "$SEED_DIR"/*.sql; do
  [ -f "$f" ] || continue
  echo "[seed-reference] apply $(basename "$f") ..."
  sed -E 's/ON DUPLICATE KEY UPDATE[^;]*/ON CONFLICT DO NOTHING/g' "$f" \
    | psql -v ON_ERROR_STOP=1 -1 -q \
        --username "${POSTGRES_USER:?}" --dbname "${POSTGRES_DB:?}"
done

echo "[seed-reference] selesai (countries/provinces/cities/districts/subdistricts)."