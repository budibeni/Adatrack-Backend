#!/usr/bin/env bash
# ============================================================================
# coverage-report.sh — laporan coverage SELURUH service aplikasi (PRD §16)
# ============================================================================
# Kenapa skrip ini ada: loop coverage di scripts/b4-verify.sh hanya mengukur
# internal + worker-*/api-vehicle, sehingga service-websocket dan ingestion-tcp
# — dua service dengan jalur kritis (autentikasi, RBAC row-level, parser
# protokol) — tidak pernah masuk gate. Skrip ini mengukur daftar yang sama
# ditambah service yang terlewat, dengan aturan pembanding numerik yang identik
# (bandingkan angka, bukan string: "8.5" > "100.0" secara leksikografis).
#
# Infra harus hidup (make up + make migrate) karena suite ADATRACK_IT=1
# (PostgreSQL/Redis nyata) diikutkan dalam angka coverage. Default: hanya
# memperingatkan saat di bawah ambang, tidak menggagalkan build
# (COVER_STRICT=1 untuk menjadikannya kegagalan).
#
# Usage: scripts/coverage-report.sh [modul ...]
#   COVER_MIN=80     ambang peringatan (%)
#   COVER_STRICT=1   keluar dengan status 1 bila ada modul di bawah ambang
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"
load_variant_env "${COMPOSE_VARIANT:-local}" >/dev/null

COVER_MIN="${COVER_MIN:-80}"
COVER_STRICT="${COVER_STRICT:-0}"

# Daftar default = service aplikasi yang punya jalur runtime (tools/e2e hanya
# harness manual, jadi tidak diukur). Argumen CLI menimpanya.
if [[ $# -gt 0 ]]; then
  MODULES=("$@")
else
  MODULES=(
    internal
    services/ingestion-tcp
    services/worker-live
    services/worker-persistence
    services/worker-alert
    services/service-websocket
    services/api-vehicle
    services/service-media
  )
fi

# Diukur dengan suite ADATRACK_IT=1 (infra hidup) — sama seperti b4-verify step 1.
export ADATRACK_IT=1

# Modul yang TIDAK ada di loop coverage b4-verify.sh (celah gate yang diketahui).
GATE_OMITS=" services/service-websocket services/ingestion-tcp services/service-media services/worker-live "

printf '%-34s %8s  %s\n' 'module' 'cover' 'status'
printf '%-34s %8s  %s\n' '----------------------------------' '--------' '------'
failed=0
total_sum=0
total_cnt=0
for mod in "${MODULES[@]}"; do
  if [[ ! -d "$ROOT/$mod" ]]; then
    printf '%-34s %8s  %s\n' "$mod" '-' 'SKIP (direktori tidak ada)'
    continue
  fi
  # max() per paket: modul dengan banyak paket dilaporkan pada paket terbaiknya,
  # persis seperti gerbang B4.
  pct="$(cd "$ROOT/$mod" && go test -count=1 -cover ./... 2>/dev/null \
    | awk '/coverage:/{for(i=1;i<=NF;i++) if($i~/[0-9.]+%/){gsub(/%/,"",$i); if($i+0>max+0) max=$i+0}} END{print max+0}')"
  pct="${pct:-0}"
  status='ok'
  if awk "BEGIN{exit !((${pct}+0) < ${COVER_MIN})}"; then
    status="WARN <${COVER_MIN}%"
    failed=1
  fi
  if [[ "$GATE_OMITS" == *" $mod "* ]]; then
    status="$status (di luar gate b4-verify)"
  fi
  printf '%-34s %7s%%  %s\n' "$mod" "$pct" "$status"
  total_sum="$(awk "BEGIN{print ${total_sum}+${pct}}")"
  total_cnt=$((total_cnt + 1))
done

if [[ $total_cnt -gt 0 ]]; then
  avg="$(awk "BEGIN{printf \"%.1f\", ${total_sum}/${total_cnt}}")"
  printf '%-34s %7s%%  %s\n' 'RATA-RATA' "$avg" "$total_cnt modul"
fi

echo
echo "ambang: ${COVER_MIN}% (COVER_STRICT=1 untuk menggagalkan build saat di bawah ambang)"
if [[ $failed -eq 1 && "$COVER_STRICT" == "1" ]]; then
  echo 'coverage: FAIL — ada modul di bawah ambang'
  exit 1
fi
echo 'coverage: selesai'
