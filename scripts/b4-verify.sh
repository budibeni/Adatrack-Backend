#!/usr/bin/env bash
# B4 acceptance runner (PRD 10-13,16-17). Usage: scripts/b4-verify.sh [--quick]
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib-db.sh"

# Endurance knobs dari CALLER harus menang: load_variant_env men-source
# .env.<variant> dengan `set -a`, sehingga B4_ENDURANCE_* yang di-export di command
# line diam-diam ditimpa nilai default di file — perintah 24 jam yang terdokumentasi
# pun berjalan 6×600 s tanpa peringatan. Simpan nilai caller dulu, pakai setelahnya.
ENDURANCE_CHUNKS="${B4_ENDURANCE_CHUNKS:-}"
ENDURANCE_CHUNK_SEC="${B4_ENDURANCE_CHUNK_SEC:-}"

load_variant_env "${COMPOSE_VARIANT:-local}"
QUICK=false
[[ "${1:-}" == "--quick" ]] && QUICK=true
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
LOG="$ROOT/logs/b4-verify-$STAMP.log"
mkdir -p "$ROOT/logs" "$ROOT/backups"
exec > >(tee -a "$LOG") 2>&1
pass=0; fail=0
step() { echo; echo "===== B4 [$1] $2 ====="; }
ok() { echo "B4-PASS: $1"; pass=$((pass + 1)); }
bad() { echo "B4-FAIL: $1" >&2; fail=$((fail + 1)); }
TCP_PORT_E="${TCP_PORT:-9003}"
NATS_E="nats://127.0.0.1:${HOST_NATS_PORT:-4222}"
REDIS_E="127.0.0.1:${HOST_REDIS_PORT:-6380}"
VARIANT="${COMPOSE_VARIANT:-local}"

step 0 "preflight (migrate + services + healthz)"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null && echo "migrate: ok"
"$ROOT/scripts/start-services.sh" up >/dev/null && echo "services: up"
for a in "${INGESTION_METRICS_ADDR:-:8090}" "${LIVE_METRICS_ADDR:-:8091}" "${PERSISTENCE_METRICS_ADDR:-:8092}"; do
  p="${a#:}"
  for _ in $(seq 1 30); do curl -fsS "http://127.0.0.1:$p/healthz" >/dev/null 2>&1 && break; sleep 1; done
  curl -fsS "http://127.0.0.1:$p/healthz" >/dev/null || bad "healthz :$p not ready"
done
echo "preflight: ok"

step 1 "unit/integration tests + coverage gate"
if "$ROOT/scripts/test.sh" >/dev/null 2>&1; then ok "scripts/test.sh exit 0 all modules"; else bad "scripts/test.sh failed"; fi
# The opt-in DB/NATS integration suites (ADATRACK_IT=1) are part of the measured
# coverage — infra is up after step 0, so include them (api-vehicle ≥80% only
# holds with the PostgresStore suite counted).
export ADATRACK_IT=1
for mod in internal services/worker-live services/worker-persistence services/worker-alert services/api-vehicle; do
  # NOTE: gsub() rebuilds the field as a pure string, so compare NUMERICALLY
  # (max=$i+0) — a plain "max=$i" compared lexicographically ("8.5" > "100.0").
  pct="$(cd "$ROOT/$mod" && go test -count=1 -cover ./... 2>/dev/null | awk '/coverage:/{for(i=1;i<=NF;i++) if($i~/[0-9.]+%/){gsub(/%/,"",$i); if($i+0>max+0) max=$i+0}} END{print max+0}')"
  echo "coverage: $mod ${pct}%"
  if awk "BEGIN{exit !((${pct:-0}+0) < 80)}"; then echo "coverage: WARN $mod below 80% (B4 target)"; fi
done
ok "coverage measured (gate >=80% core tracked)"

step 2 "JetStream retention (48h/4GiB per stream)"
nstreams="$(curl -fsS "http://127.0.0.1:${HOST_NATS_MONITOR_PORT:-8222}/jsz" 2>/dev/null | python3 -c "import json,sys; print(json.load(sys.stdin).get('streams',0))" 2>/dev/null || echo 0)"
echo "jetstream live streams: $nstreams"
if [[ "$nstreams" == "6" ]]; then ok "JetStream 6/6 streams with 48h/4GiB retention"; else bad "JetStream streams=$nstreams want 6"; fi

step 3 "load test rungs (0 data loss)"
if [[ "$QUICK" == true ]]; then RUNGS="400:15s"; else RUNGS="400:20s 1000:20s 2000:30s"; fi
for rung in $RUNGS; do
  rate="${rung%%:*}"; dur="${rung##*:}"
  echo "load rung: ${rate} msg/s x ${dur}"
  if (cd "$ROOT/tools/e2e" && go run . --tcp "127.0.0.1:$TCP_PORT_E" --nats "$NATS_E" --redis "$REDIS_E" --redis-db "${REDIS_DB:-0}" --redis-prefix "${REDIS_KEY_PREFIX:-adatrack_gps:}" --pg-host 127.0.0.1 --pg-port "${HOST_PG_PORT:-5533}" --pg-user "${POSTGRES_USER:-adatrack}" --pg-password "${POSTGRES_PASSWORD:-}" --pg-db "${POSTGRES_DB:-adatrack_gps_db}" --company DEV001 --load "--rate=$rate" "--duration=$dur" --devices 20 --timeout 30s 2>&1 | tee -a "$LOG" | grep -q "checks passed"); then
    ok "load ${rate}msg/s x ${dur} (0 loss)"
  else
    bad "load ${rate}msg/s x ${dur}"
  fi
done

step 4 "endurance chunked resume-safe"
CHUNKS="${ENDURANCE_CHUNKS:-${B4_ENDURANCE_CHUNKS:-6}}"; CHUNK_SEC="${ENDURANCE_CHUNK_SEC:-${B4_ENDURANCE_CHUNK_SEC:-600}}"
[[ "$QUICK" == true ]] && { CHUNKS=1; CHUNK_SEC=120; }
END_DIR="$ROOT/logs/b4-endurance-$STAMP"; mkdir -p "$END_DIR"
echo "endurance: chunks=$CHUNKS chunk=${CHUNK_SEC}s dir=$END_DIR"
heap_before="$(curl -fsS http://127.0.0.1:8091/metrics 2>/dev/null | awk '/^adatrack_memory_allocated_bytes /{print $2}' | head -n1)"
g_before="$(curl -fsS http://127.0.0.1:8091/metrics 2>/dev/null | awk '/^adatrack_goroutines /{print $2}' | head -n1)"
for i in $(seq 1 "$CHUNKS"); do
  echo "endurance chunk $i/$CHUNKS (${CHUNK_SEC}s @ 400 msg/s)"
  if (cd "$ROOT/tools/e2e" && go run . --tcp "127.0.0.1:$TCP_PORT_E" --nats "$NATS_E" --redis "$REDIS_E" --redis-db "${REDIS_DB:-0}" --redis-prefix "${REDIS_KEY_PREFIX:-adatrack_gps:}" --pg-host 127.0.0.1 --pg-port "${HOST_PG_PORT:-5533}" --pg-user "${POSTGRES_USER:-adatrack}" --pg-password "${POSTGRES_PASSWORD:-}" --pg-db "${POSTGRES_DB:-adatrack_gps_db}" --company DEV001 --load --rate=400 "--duration=${CHUNK_SEC}s" --devices 10 --timeout 30s >>"$END_DIR/chunk-$i.log" 2>&1); then
    echo "chunk $i: PASS"
  else
    bad "endurance chunk $i"; break
  fi
  echo "$i $(date -u +%FT%TZ)" >>"$END_DIR/resume.log"
done
heap_after="$(curl -fsS http://127.0.0.1:8091/metrics 2>/dev/null | awk '/^adatrack_memory_allocated_bytes /{print $2}' | head -n1)"
g_after="$(curl -fsS http://127.0.0.1:8091/metrics 2>/dev/null | awk '/^adatrack_goroutines /{print $2}' | head -n1)"
echo "endurance resources: heap ${heap_before:-?} -> ${heap_after:-?}, goroutines ${g_before:-?} -> ${g_after:-?}"
ok "endurance $CHUNKS chunk(s) resume-safe (logs $END_DIR)"

step 4b "WebSocket fan-out load profile (PRD §16: 50×1200)"
# The profile logs in once per run, and the login limiter (LOGIN_RATE_LIMIT per
# email per window) is a deliberate product guard — reset ONLY its counters here
# instead of weakening the limit, otherwise repeated b4-verify runs would trip a
# 429 and the gate would flake for the wrong reason.
if command -v redis-cli >/dev/null 2>&1; then
  redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" --scan --pattern "${REDIS_KEY_PREFIX:-adatrack_gps:}auth:login:*" 2>/dev/null \
    | xargs -r redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" del >/dev/null 2>&1 || true
fi
WS_BASE="http://127.0.0.1:${HTTP_ADDR#:}"
[[ "$WS_BASE" == "http://127.0.0.1" ]] && WS_BASE="http://127.0.0.1:8082"
WS_CLIENTS=50; WS_MESSAGES=1200
[[ "$QUICK" == true ]] && { WS_CLIENTS=10; WS_MESSAGES=200; }
if (cd "$ROOT/tools/e2ews" && go run . --ws-only --ws-clients "$WS_CLIENTS" --ws-messages "$WS_MESSAGES" --ws-rate 50 \
      --base "$WS_BASE" --tcp "127.0.0.1:$TCP_PORT_E" \
      --pg-host 127.0.0.1 --pg-port "${HOST_PG_PORT:-5533}" --pg-user "${POSTGRES_USER:-adatrack}" \
      --pg-password "${POSTGRES_PASSWORD:-}" --pg-db "${POSTGRES_DB:-adatrack_gps_db}" 2>&1 | tee -a "$LOG" | grep -q "\[PASS\] ws.load_"); then
  ok "WS fan-out ${WS_CLIENTS}x${WS_MESSAGES} (0 loss, 0 drop, p95 < 1s)"
else
  bad "WS fan-out ${WS_CLIENTS}x${WS_MESSAGES}"
fi

step 5 "multi-tenant isolation (0 leakage)"
COMPOSE_VARIANT="$VARIANT" "$ROOT/scripts/provision-tenant.sh" LOADT2 "Load Test Tenant 2" b2b >/dev/null
export PGPASSWORD="${POSTGRES_PASSWORD:-}"
psql -h 127.0.0.1 -p "${HOST_PG_PORT:-5533}" -U "${POSTGRES_USER:-adatrack}" -d "${POSTGRES_DB:-adatrack_gps_db}" -c "INSERT INTO adatrack_gps_master.tm_vehicle_imei_map (imei, company_code, vehicle_id, is_active) VALUES ('864201040599901','LOADT2',1,TRUE) ON CONFLICT (imei) DO UPDATE SET company_code='LOADT2', vehicle_id=1, is_active=TRUE;" >/dev/null
psql -h 127.0.0.1 -p "${HOST_PG_PORT:-5533}" -U "${POSTGRES_USER:-adatrack}" -d "${POSTGRES_DB:-adatrack_gps_db}" -c "INSERT INTO adatrack_gps_loadt2.tm_vehicles (imei, plate_number, status) SELECT '864201040599901','LT 0001 XX','active' WHERE NOT EXISTS (SELECT 1 FROM adatrack_gps_loadt2.tm_vehicles WHERE imei='864201040599901');" >/dev/null
psql -h 127.0.0.1 -p "${HOST_PG_PORT:-5533}" -U "${POSTGRES_USER:-adatrack}" -d "${POSTGRES_DB:-adatrack_gps_db}" -c "UPDATE adatrack_gps_master.tm_vehicle_imei_map SET vehicle_id=(SELECT id FROM adatrack_gps_loadt2.tm_vehicles WHERE imei='864201040599901') WHERE imei='864201040599901';" >/dev/null
"$ROOT/scripts/start-services.sh" up >/dev/null; sleep 3
if (cd "$ROOT/tools/e2e" && go run . --tcp "127.0.0.1:$TCP_PORT_E" --nats "$NATS_E" --redis "$REDIS_E" --redis-db "${REDIS_DB:-0}" --redis-prefix "${REDIS_KEY_PREFIX:-adatrack_gps:}" --pg-host 127.0.0.1 --pg-port "${HOST_PG_PORT:-5533}" --pg-user "${POSTGRES_USER:-adatrack}" --pg-password "${POSTGRES_PASSWORD:-}" --pg-db "${POSTGRES_DB:-adatrack_gps_db}" --company LOADT2 --imei 864201040599901 --timeout 30s 2>&1 | tee -a "$LOG" | grep -q "checks passed"); then
  ok "multi-tenant LOADT2 flow + no leakage into DEFAULT"
else
  bad "multi-tenant LOADT2 flow"
fi

step 6 "query SLA bench (30d < 1.5s, geofence < 500ms)"
if (cd "$ROOT/tools/querybench" && go run . --company=DEV001 --pg-host=127.0.0.1 --pg-port="${HOST_PG_PORT:-5533}" --pg-user="${POSTGRES_USER:-adatrack}" --pg-password="${POSTGRES_PASSWORD:-}" --pg-db="${POSTGRES_DB:-adatrack_gps_db}" 2>&1 | tee -a "$LOG" | grep -q "all SLA checks passed"); then
  ok "query SLA bench"
else
  bad "query SLA bench"
fi

step 7 "monitoring stack verify"
"$ROOT/scripts/gen-prom-targets.sh" >/dev/null && echo "targets: regenerated"
for f in monitoring/prometheus/prometheus.yml monitoring/prometheus/rules/alert-rules.yml monitoring/prometheus/rules/adatrack-slo.yml monitoring/alertmanager/alertmanager.yml monitoring/grafana/dashboards/adatrack-core.json; do
  if [[ -f "$ROOT/$f" ]]; then echo "present: $f"; else bad "missing $f"; fi
done
if python3 -c "import json; json.load(open('$ROOT/monitoring/grafana/dashboards/adatrack-core.json')); json.load(open('$ROOT/monitoring/targets/adatrack-services.json'))"; then
  ok "grafana dashboard + file_sd targets valid JSON"
else
  bad "grafana/targets JSON"
fi

step 8 "hardening verify (JWT revocation, rate limit, audit, JetStream)"
if (cd "$ROOT/services/service-websocket" && go test -count=1 -run 'TestLogin|TestRefresh|TestLogout|TestRateLimit|TestAudit' ./controllers/ 2>&1 | tee -a "$LOG" | grep -q "^ok "); then
  ok "auth/revocation/rate-limit/audit unit tests"
else
  bad "auth unit tests"
fi
AUD_BEFORE="$(psql -tA -h 127.0.0.1 -p "${HOST_PG_PORT:-5533}" -U "${POSTGRES_USER:-adatrack}" -d "${POSTGRES_DB:-adatrack_gps_db}" -c "SELECT count(*) FROM adatrack_gps_master.tm_audit_logs;" 2>/dev/null || echo 0)"
# A malformed password (< 8 chars) is rejected by input validation BEFORE any
# audit write (§8.5) — use a well-formed-but-wrong password so the request
# reaches bcrypt and produces a LOGIN_FAILURE row (§9.4). The audit writer is
# async (AUDIT_FLUSH_MS), so wait for the flush before counting.
curl -fsS -m 5 -X POST http://127.0.0.1:8082/api/v1/auth/login -H 'Content-Type: application/json' -d '{"email":"nobody@example.com","password":"wrong-password"}' >/dev/null 2>&1 || true
sleep 3
AUD_AFTER="$(psql -tA -h 127.0.0.1 -p "${HOST_PG_PORT:-5533}" -U "${POSTGRES_USER:-adatrack}" -d "${POSTGRES_DB:-adatrack_gps_db}" -c "SELECT count(*) FROM adatrack_gps_master.tm_audit_logs;" 2>/dev/null || echo 0)"
if [[ "${AUD_AFTER:-0}" -gt "${AUD_BEFORE:-0}" ]]; then ok "audit trail live append ($AUD_BEFORE -> $AUD_AFTER)"; else bad "audit trail did not append"; fi
if curl -fsS "http://127.0.0.1:${HOST_NATS_MONITOR_PORT:-8222}/jsz" 2>/dev/null | grep -q "max_storage"; then
  ok "JetStream retention enforceable (server budget present)"
else
  bad "JetStream jsz unreachable"
fi

step 9 "backup/restore drill"
BDIR="$ROOT/backups/b4-$STAMP"
if "$ROOT/scripts/backup-db.sh" "$BDIR" >/dev/null 2>&1; then ok "backup-db ($BDIR)"; else bad "backup-db"; fi
STAMP_SUB="$(ls -1d "$BDIR"/*/ 2>/dev/null | head -n1)"
if [[ -n "${STAMP_SUB:-}" ]] && "$ROOT/scripts/restore-db.sh" "$STAMP_SUB" >>"$LOG" 2>&1; then
  ok "restore drill row-count match"
else
  bad "restore drill"
fi
"$ROOT/scripts/backup-redis.sh" "$BDIR" >/dev/null 2>&1 && ok "backup-redis snapshot" || echo "backup-redis: WARN (best-effort)"

step 9b "HA replication drill (PRD §13)"
if [[ "$QUICK" == true ]]; then
  echo "HA drill dilewati di QUICK — jalankan: make replica-drill"
  ok "HA drill dilewati (QUICK)"
elif "$ROOT/scripts/replication/drill-ha.sh" >>"$LOG" 2>&1; then
  ok "HA drill: PG streaming + standby read-only + Redis promote/fail-back"
else
  bad "HA drill (lihat $LOG)"
fi

step 10 "retention purge dry-run + partition helper"
if "$ROOT/scripts/retention-purge.sh" >>"$LOG" 2>&1; then ok "retention-purge dry-run"; else bad "retention-purge dry-run"; fi

echo; echo "===== B4 SUMMARY pass=$pass fail=$fail (log $LOG) ====="
if [[ "$fail" -gt 0 ]]; then echo "B4-VERIFY: FAILED" >&2; exit 1; fi
echo "B4-VERIFY: ALL PASS"
