#!/usr/bin/env bash
# ============================================================================
# gen-prom-targets.sh — regenerate Prometheus file_sd targets from the live
# service health ports (B4, PRD §10.4 / §14.6 step 5).
# Mirrors scripts/start-services.sh so the monitoring stack always scrapes the
# exact /metrics listeners the services expose.
#
# ALAMAT HOST (kenapa tidak boleh 127.0.0.1):
#   Service pipeline berjalan di HOST, Prometheus berjalan di container — jadi
#   127.0.0.1 dari sudut Prometheus adalah loopback container itu sendiri.
#   Script memilih alamat host SECARA EMPIRIS, berurutan:
#     1) PROM_SCRAPE_HOST bila di-set (selalu menang);
#     2) `host.docker.internal` — nama stabil lintas mesin; service `prometheus`
#        di docker-compose.yml memetakannya via extra_hosts:host-gateway;
#     3) alamat IPv4 utama host (detect_host_ip) — dipakai bila (2) tidak
#        terjangkau, mis. Docker Desktop di Windows/WSL2 di mana
#        host.docker.internal menunjuk gateway VM (~192.168.65.254), BUKAN
#        distro tempat service host-run berjalan.
#   Kandidat diuji dengan benar-benar menembak /healthz DARI DALAM container
#   Prometheus, lalu hasilnya dicetak supaya tidak ada tebak-tebakan. Bila
#   container Prometheus belum jalan (mis. `make up` pertama kali), (3)
#   langsung dipakai dan akan divalidasi ulang pada `make prom-targets`
#   berikutnya.
#
# File hasil generate TIDAK di-track git (isinya machine-specific) — lihat
# .gitignore. `make up` / `make monitoring-up` / `make prom-targets` selalu
# meregenerasinya.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh" 2>/dev/null || true
if [[ -f "$ROOT/.env.${COMPOSE_VARIANT:-local}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$ROOT/.env.${COMPOSE_VARIANT:-local}"
  set +a
fi

TARGETS_FILE="${1:-$ROOT/monitoring/targets/adatrack-services.json}"
PROM_CONTAINER="${PROM_CONTAINER:-adatrack_prometheus}"

# detect_host_ip prints the host's primary IPv4 (127.0.0.1 as last resort).
detect_host_ip() {
  local ip
  ip="$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')"
  if [[ -z "$ip" ]]; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  if [[ -z "$ip" ]]; then
    ip="127.0.0.1"
  fi
  echo "$ip"
}

# probe_host <host> <port> succeeds when a containerised Prometheus can really
# reach that host address (i.e. a pipeline service is listening there).
probe_host() {
  local host="$1" port="$2"
  docker exec "$PROM_CONTAINER" wget -q -O /dev/null -T 3 "http://${host}:${port}/healthz" >/dev/null 2>&1
}

# probe_port_list prints the candidate ports (from env, same list we are about
# to write) so the probe can use a service that is actually up.
probe_port_list() {
  local addrs=(
    "${INGESTION_METRICS_ADDR:-:8090}"
    "${LIVE_METRICS_ADDR:-:8091}"
    "${PERSISTENCE_METRICS_ADDR:-:8092}"
    "${ALERT_METRICS_ADDR:-:8094}"
    "${HTTP_ADDR:-:8082}"
    "${API_VEHICLE_HTTP_ADDR:-:8081}"
    "${MEDIA_METRICS_ADDR:-:8096}"
  )
  local a
  for a in "${addrs[@]}"; do echo "${a#:}"; done
}

# pick_target_host echoes the host address to use, plus a reason on stderr.
pick_target_host() {
  if [[ -n "${PROM_SCRAPE_HOST:-}" ]]; then
    echo "PROM_SCRAPE_HOST=$PROM_SCRAPE_HOST" >&2
    echo "$PROM_SCRAPE_HOST"
    return
  fi

  local detected; detected="$(detect_host_ip)"
  local candidates=("host.docker.internal" "$detected")
  local cand port

  for cand in "${candidates[@]}"; do
    [[ -z "$cand" ]] && continue
    while read -r port; do
      if probe_host "$cand" "$port"; then
        echo "dipilih '$cand' (terverifikasi dari dalam container $PROM_CONTAINER pada port $port)" >&2
        echo "$cand"
        return
      fi
    done < <(probe_port_list)
    echo "kandidat '$cand' tidak terjangkau dari container $PROM_CONTAINER" >&2
  done

  # Prometheus belum jalan atau belum ada service yang listen: pakai alamat host
  # hasil deteksi (perilaku yang sudah terbukti) dan validasi ulang nanti.
  # 127.0.0.1 TIDAK PERNAH benar di sini: dari dalam container itu loopback
  # container sendiri, jadi target pasti DOWN. Kalau deteksi gagal, katakan
  # dengan lantang alih-alih menulis konfigurasi yang diam-diam rusak.
  if [[ "$detected" == "127.0.0.1" ]]; then
    echo "PERINGATAN: deteksi alamat host gagal — 127.0.0.1 adalah loopback container," >&2
    echo "            sehingga seluruh target adatrack-services akan DOWN." >&2
    echo "            set PROM_SCRAPE_HOST=<alamat host yang bisa dijangkau container>." >&2
  fi
  echo "fallback ke '$detected' (container $PROM_CONTAINER belum bisa diprobe)" >&2
  echo "$detected"
}

TARGET_HOST="$(pick_target_host)"
mkdir -p "$(dirname "$TARGETS_FILE")"

addr() { local v="$1" d="$2"; v="${v:-$d}"; v="${v#:}"; echo "$TARGET_HOST:$v"; }

{
  printf '[\n'
  printf '  {"targets": ["%s"], "labels": {"service": "ingestion-tcp", "env": "local"}}' "$(addr "${INGESTION_METRICS_ADDR:-}" 8090)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "worker-live", "env": "local"}}' "$(addr "${LIVE_METRICS_ADDR:-}" 8091)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "worker-persistence", "env": "local"}}' "$(addr "${PERSISTENCE_METRICS_ADDR:-}" 8092)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "worker-alert", "env": "local"}}' "$(addr "${ALERT_METRICS_ADDR:-}" 8094)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "service-websocket", "env": "local"}}' "$(addr "${HTTP_ADDR:-}" 8082)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "api-vehicle", "env": "local"}}' "$(addr "${API_VEHICLE_HTTP_ADDR:-}" 8081)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "service-media", "env": "local"}}' "$(addr "${MEDIA_METRICS_ADDR:-}" 8096)"
  printf '\n]\n'
} > "$TARGETS_FILE"

echo "gen-prom-targets: wrote $TARGETS_FILE"
cat "$TARGETS_FILE"
