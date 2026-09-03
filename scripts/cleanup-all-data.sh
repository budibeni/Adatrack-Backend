#!/usr/bin/env bash
# ============================================================================
# cleanup-all-data.sh — WIPE TOTAL data runtime infra dev ajb_gps
#
# Menghapus (DESTRUKTIF, tidak bisa di-undo):
#   - Semua container & network Docker project ini
#   - Volume data: Postgres(+replica), MySQL, Redis(+replica), NATS,
#     **MinIO (minio_data — objek media dashcam B5b)**, Prometheus/Grafana/dll
#   - Docker build cache; image (opsional, lihat flag)
#
# Dampak: telemetry/alerts/fuel_logs/media (MinIO) HILANG PERMANEN.
#         Saat `docker compose up` berikutnya, volume kosong dibuat ulang dan
#         init-pg/init-mysql + seed otomatis mengisi data segar.
#
# Pemakaian:
#   ./cleanup-all-data.sh                # targeted: hanya volume infra adatrack
#   ./cleanup-all-data.sh --all          # nuke: SEMUA volume + SEMUA image
#   ./cleanup-all-data.sh --keep-images  # targeted, image dipertahankan
#
# JALANKAN SAAT DOCKER DESKTOP HIDUP. Sesudah ini, compact VHDX di Windows
# (wsl --shutdown + Optimize-VHD/diskpart) agar storage host benar-benar bebas.
# ============================================================================
set -euo pipefail

DOCKER="${DOCKER:-docker}"
MODE="targeted"      # targeted | all
KEEP_IMAGES=0
for arg in "$@"; do
  case "$arg" in
    --all) MODE="all" ;;
    --keep-images) KEEP_IMAGES=1 ;;
    *) echo "Flag tidak dikenal: $arg (pakai --all / --keep-images)" >&2; exit 1 ;;
  esac
done

if ! command -v "$DOCKER" >/dev/null 2>&1; then
  echo "ERROR: docker CLI tidak ditemukan — Docker Desktop hidup?" >&2
  exit 1
fi
if ! "$DOCKER" info >/dev/null 2>&1; then
  echo "ERROR: docker daemon tidak merespons — hidupkan Docker Desktop dulu." >&2
  exit 1
fi

echo "== BEFORE =="
df -h / | tail -1
"$DOCKER" system df || true
echo

echo ">> [1/7] Stop & remove SEMUA container…"
CID="$("$DOCKER" ps -aq 2>/dev/null || true)"
if [ -n "$CID" ]; then
  echo "$CID" | xargs "$DOCKER" rm -f >/dev/null 2>&1 || true
fi
echo "   containers removed."

echo ">> [2/7] Volume yang ADA sebelum dihapus:"
"$DOCKER" volume ls || true

if [ "$MODE" = "all" ]; then
  echo ">> [3/7] MODE ALL: remove SEMUA volume (tanpa kecuali)…"
  "$DOCKER" volume prune -af >/dev/null 2>&1 || true
else
  echo ">> [3/7] MODE TARGETED: remove volume infra adatrack"
  echo "         (postgres/mysql/redis/nats/minio/monitoring + prefiks project lama)…"
  "$DOCKER" volume ls --format '{{.Name}}' | grep -E \
    'adatrack|ajb|deployments_|mysql|postgres|redis|nats|minio|prometheus|grafana|cadvisor|alertmanager|node.?exporter|postgres.?exporter' \
    | xargs -r -n1 "$DOCKER" volume rm -f >/dev/null 2>&1 || true
fi
echo "   volumes removed."

echo ">> [4/7] Remove networks tidak terpakai…"
"$DOCKER" network prune -f >/dev/null 2>&1 || true

echo ">> [5/7] Remove build cache…"
"$DOCKER" builder prune -af >/dev/null 2>&1 || true

if [ "$KEEP_IMAGES" -eq 1 ]; then
  echo ">> [6/7] IMAGE DIPERTAHANKAN (--keep-images)"
else
  echo ">> [6/7] Remove SEMUA image (akan re-pull saat up berikutnya)…"
  "$DOCKER" image prune -af >/dev/null 2>&1 || true
fi

echo ">> [7/7] Ringkasan akhir…"
echo
echo "== AFTER =="
df -h / | tail -1
"$DOCKER" system df || true
echo
echo "SELESAI (mode: $MODE). Lanjutkan di Windows PowerShell admin untuk"
echo "membebaskan VHDX host: wsl --shutdown lalu compact disk (Optimize-VHD / diskpart)."
