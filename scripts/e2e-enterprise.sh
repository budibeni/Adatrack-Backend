#!/usr/bin/env bash
# ============================================================================
# e2e-enterprise.sh — end-to-end verification of the B12 enterprise surface
# plus the B11 audit trail (PRD §5.10 / §9.4).
# ============================================================================
# Closes audit gap A4 ("B12 hanya terbukti unit test + SQL"). This drives the
# REAL HTTP API against REAL PostgreSQL: login (service-websocket) → JWT interop
# (api-vehicle) → menu/module registry → enterprise CRUD (soft delete + restore)
# → share link (+ public unauthenticated resolve) → analytics → audit trail →
# RBAC negatives. Every check is a live request, not a unit test.
#
# Usage: scripts/e2e-enterprise.sh
# Prereqs: infra reachable (PostgreSQL/Redis/NATS) + migrations applied.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

VARIANT="${COMPOSE_VARIANT:-local}"
load_variant_env "$VARIANT"

COMPANY="${E2E_COMPANY:-DEV001}"
VEHICLE_ID="${E2E_VEHICLE_ID:-1}"
API="http://127.0.0.1${API_VEHICLE_HTTP_ADDR:-:8081}"
AUTH="http://127.0.0.1${HTTP_ADDR:-:8082}"
ADMIN_EMAIL="${E2E_ADMIN_EMAIL:-admin@dev001.io}"
ADMIN_PASSWORD="${E2E_ADMIN_PASSWORD:-Admin@123}"
DRIVER_EMAIL="${E2E_DRIVER_EMAIL:-driver@dev001.io}"
DRIVER_PASSWORD="${E2E_DRIVER_PASSWORD:-Admin@123}"

PASS=0
FAIL=0
ok()  { echo "[PASS] $1"; PASS=$((PASS + 1)); }
bad() { echo "[FAIL] $1"; FAIL=$((FAIL + 1)); }

# BODY_FILE matters: `code="$(req ...)"` runs req in a SUBSHELL, so a variable
# assignment inside it would be lost in the caller. Writing the response body to a
# file is the only reliable way to share it with jget()/the audit check.
BODY_FILE="$(mktemp /tmp/e2e-enterprise.XXXXXX)"
trap 'rm -f "$BODY_FILE"' EXIT

# req <method> <path> <token> [json-body] → echoes the HTTP status code and stores
# the response body in $BODY_FILE.
req() {
  local m="$1" p="$2" t="$3" b="${4-}" out
  local args=(-s -m 20 -X "$m" -w $'\n%{http_code}')
  [[ -n "$t" ]] && args+=(-H "Authorization: Bearer $t")
  [[ -n "$b" ]] && args+=(-H 'Content-Type: application/json' -d "$b")
  out="$(curl "${args[@]}" "$API$p" || true)"
  printf '%s' "${out%$'\n'*}" > "$BODY_FILE"
  printf '%s' "${out##*$'\n'}"
}

# jget <dotted.path> — reads $BODY_FILE, prints the value ("" when missing/null).
jget() {
  python3 -c '
import json,sys
try:
    with open(sys.argv[2]) as fh:
        cur=json.load(fh)
except Exception:
    cur={}
for k in sys.argv[1].split("."):
    if k=="":
        continue
    if isinstance(cur,list):
        try: cur=cur[int(k)]
        except Exception: cur=None
    elif isinstance(cur,dict):
        cur=cur.get(k)
    else:
        cur=None
    if cur is None:
        print(""); sys.exit(0)
print(cur)
' "$1" "$BODY_FILE"
}

login() { # email password → token
  curl -s -m 20 -X POST -H 'Content-Type: application/json' \
    -d "{\"email\":\"$1\",\"password\":\"$2\"}" "$AUTH/api/v1/auth/login" |
    python3 -c 'import json,sys
try: d=json.load(sys.stdin)
except Exception: d={}
data=d.get("data") or {}
print(data.get("token") or data.get("access_token") or "")'
}

expect() { # got want label
  if [[ "$1" == "$2" ]]; then ok "$3 (HTTP $1)"; else bad "$3 — got HTTP $1, want $2"; fi
}

echo "e2e-enterprise: variant=$VARIANT company=$COMPANY api=$API auth=$AUTH"
echo "e2e-enterprise: migrations"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null

# Dev/E2E saja: buang counter rate-limit login/API agar run berulang tidak 429
# (pola sama dengan scripts/e2e-fleet.sh).
if command -v redis-cli >/dev/null 2>&1; then
  for pat in 'adatrack_gps:auth:login:*' 'adatrack_gps:auth:api:*'; do
    while IFS= read -r key; do
      [[ -n "$key" ]] || continue
      redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" del "$key" >/dev/null 2>&1 || true
    done < <(redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" --scan --pattern "$pat" 2>/dev/null || true)
  done
  echo "e2e-enterprise: rate-limit counters cleared"
fi

echo "e2e-enterprise: starting pipeline services (host mode)"
"$ROOT/scripts/start-services.sh" up >/dev/null
for _ in $(seq 1 30); do
  curl -fsS "$API/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "$API/healthz" >/dev/null && echo "e2e-enterprise: api-vehicle ready"

# --- login (single auth authority = service-websocket; JWT interop) ----------
ADMIN_TOKEN="$(login "$ADMIN_EMAIL" "$ADMIN_PASSWORD")"
DRIVER_TOKEN="$(login "$DRIVER_EMAIL" "$DRIVER_PASSWORD")"
[[ -n "$ADMIN_TOKEN" ]] && ok "admin login → JWT issued" || bad "admin login failed"
[[ -n "$DRIVER_TOKEN" ]] && ok "driver login → JWT issued" || bad "driver login failed"

# --- B12 §1.2 access registry: menu + modules --------------------------------
code="$(req GET /api/v1/access/menu "$ADMIN_TOKEN")"
expect "$code" 200 "GET /access/menu (role-scoped menu)"
[[ -n "$(jget data.0.menu_id)" ]] && ok "menu payload is non-empty" || bad "menu payload empty"

code="$(req GET /api/v1/modules "$ADMIN_TOKEN")"
expect "$code" 200 "GET /modules (module registry + licences)"

# --- B12 §1.2 enterprise CRUD: drivers (create→get→patch→delete→restore) -----
SUFFIX="$(date +%s)"
code="$(req POST /api/v1/drivers "$ADMIN_TOKEN" \
  "{\"name\":\"E2E Driver $SUFFIX\",\"employee_code\":\"E2E-$SUFFIX\",\"phone\":\"+620000000\"}")"
expect "$code" 201 "POST /drivers (create)"
DRIVER_ID="$(jget data.id)"
[[ -n "$DRIVER_ID" ]] && ok "created driver id=$DRIVER_ID" || bad "create returned no id"

code="$(req GET "/api/v1/drivers/$DRIVER_ID" "$ADMIN_TOKEN")"
expect "$code" 200 "GET /drivers/{id}"

code="$(req PATCH "/api/v1/drivers/$DRIVER_ID" "$ADMIN_TOKEN" '{"phone":"+620000001"}')"
expect "$code" 200 "PATCH /drivers/{id} (partial update)"
[[ "$(jget data.phone)" == "+620000001" ]] && ok "PATCH persisted the new phone" || bad "PATCH did not persist"

code="$(req DELETE "/api/v1/drivers/$DRIVER_ID" "$ADMIN_TOKEN" '{"reason":"e2e"}')"
expect "$code" 200 "DELETE /drivers/{id} (soft delete)"
code="$(req GET "/api/v1/drivers/$DRIVER_ID" "$ADMIN_TOKEN")"
expect "$code" 404 "soft-deleted driver is hidden"

code="$(req POST "/api/v1/drivers/$DRIVER_ID/restore" "$ADMIN_TOKEN")"
expect "$code" 200 "POST /drivers/{id}/restore"
code="$(req GET "/api/v1/drivers/$DRIVER_ID" "$ADMIN_TOKEN")"
expect "$code" 200 "restored driver is visible again"

# --- B12 §1.2 groups (second resource → proves the registry, not a special) --
code="$(req POST /api/v1/groups "$ADMIN_TOKEN" "{\"name\":\"E2E Group $SUFFIX\",\"group_type\":\"driver\"}")"
expect "$code" 201 "POST /groups (registry-driven, second resource)"
GROUP_ID="$(jget data.id)"

# --- B12 §1.1 share link + PUBLIC unauthenticated resolve (FR-9.3) -----------
code="$(req POST /api/v1/share-links "$ADMIN_TOKEN" \
  "{\"label\":\"e2e $SUFFIX\",\"vehicle_ids\":[$VEHICLE_ID],\"ttl_minutes\":60}")"
expect "$code" 201 "POST /share-links (create)"
SHARE_ID="$(jget data.id)"
SHARE_TOKEN="$(jget data.token)"

if [[ -n "$SHARE_TOKEN" ]]; then
  pub_code="$(curl -s -m 20 -o /tmp/ee_pub -w '%{http_code}' "$API/api/v1/share/$SHARE_TOKEN" || true)"
  expect "$pub_code" 200 "GET /share/{token} PUBLIC (no Authorization header)"
  grep -q "$VEHICLE_ID" /tmp/ee_pub && ok "public payload carries the shared vehicle" || bad "public payload missing vehicle"
else
  bad "no share token in create response"
fi

code="$(req DELETE "/api/v1/share-links/$SHARE_ID" "$ADMIN_TOKEN" '{"reason":"e2e"}')"
expect "$code" 200 "DELETE /share-links/{id} (revoke)"
if [[ -n "$SHARE_TOKEN" ]]; then
  rev_code="$(curl -s -m 20 -o /dev/null -w '%{http_code}' "$API/api/v1/share/$SHARE_TOKEN" || true)"
  expect "$rev_code" 404 "revoked share link resolves to 404"
fi

# --- B12 §1.1/§1.5/§1.6 analytics --------------------------------------------
for ep in /api/v1/heatmap /api/v1/reports/trips /api/v1/reports/violations /api/v1/safety/scores /api/v1/integrations; do
  code="$(req GET "$ep" "$ADMIN_TOKEN")"
  expect "$code" 200 "GET $ep"
done

# --- B12 §1.6 CSV export (gap C3) -------------------------------------------
csv_status="$(curl -s -m 20 -D /tmp/ee_csv_hdr -o /tmp/ee_trips.csv -w '%{http_code}' \
  -H "Authorization: Bearer $ADMIN_TOKEN" "$API/api/v1/reports/trips/export")"
expect "$csv_status" 200 "GET /reports/trips/export (CSV attachment)"
if grep -qi 'content-type: text/csv' /tmp/ee_csv_hdr && grep -qi 'content-disposition: attachment' /tmp/ee_csv_hdr; then
  ok "CSV response carries text/csv + attachment headers"
else
  bad "CSV response headers are wrong"
fi
if grep -q 'trip_count' /tmp/ee_trips.csv; then
  ok "CSV body contains the header row"
else
  bad "CSV body is missing the header row"
fi

# --- B12 §1.8 integrations (exercises the text[] `events` column scan) --------
code="$(req POST /api/v1/integrations "$ADMIN_TOKEN" \
  "{\"name\":\"E2E Hook $SUFFIX\",\"kind\":\"webhook\",\"endpoint_url\":\"https://example.invalid/e2e\",\"events\":[\"alert.sos\",\"alert.geofence\"]}")"
expect "$code" 201 "POST /integrations (create, events[] array)"
INTEGRATION_ID="$(jget data.id)"

code="$(req GET /api/v1/integrations "$ADMIN_TOKEN")"
expect "$code" 200 "GET /integrations (list — reaches the text[] scan)"
if grep -q 'alert.sos' "$BODY_FILE"; then
  ok "integration events[] round-tripped through the store"
else
  bad "integration events[] missing from the list payload"
fi

code="$(req DELETE "/api/v1/integrations/$INTEGRATION_ID" "$ADMIN_TOKEN")"
expect "$code" 200 "DELETE /integrations/{id} (soft delete)"

# --- B11 §9.4 audit trail records the mutations above ------------------------
code="$(req GET '/api/v1/audit-logs?limit=100' "$ADMIN_TOKEN")"
expect "$code" 200 "GET /audit-logs (Admin)"
if grep -q 'DRIVER' "$BODY_FILE"; then
  ok "audit trail contains the DRIVER mutation (mandatory audit working)"
else
  bad "audit trail has no DRIVER entry"
fi

# --- RBAC negatives (deny by default) ----------------------------------------
code="$(req GET /api/v1/drivers "")"
expect "$code" 401 "no token → 401 (deny by default)"
code="$(req GET /api/v1/audit-logs "$DRIVER_TOKEN")"
expect "$code" 403 "driver token → 403 on /audit-logs (Admin-only)"
code="$(req POST /api/v1/drivers "$DRIVER_TOKEN" '{"name":"x"}')"
expect "$code" 403 "driver token → 403 on POST /drivers (write gated)"
code="$(req GET /api/v1/share-links "$DRIVER_TOKEN")"
expect "$code" 403 "driver token → 403 on /share-links (Admin-only)"

# --- cleanup: keep the tenant tidy (soft-delete what we created) -------------
req DELETE "/api/v1/drivers/$DRIVER_ID" "$ADMIN_TOKEN" '{"reason":"e2e cleanup"}' >/dev/null || true
req DELETE "/api/v1/groups/$GROUP_ID" "$ADMIN_TOKEN" '{"reason":"e2e cleanup"}' >/dev/null || true

echo
echo "e2e-enterprise summary: ${PASS}/$((PASS + FAIL)) checks passed"
[[ "$FAIL" -eq 0 ]]

