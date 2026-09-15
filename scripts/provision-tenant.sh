#!/usr/bin/env bash
# ============================================================================
# provision-tenant.sh — create/refresh one tenant (PRD §6, FR-5.5)
# ============================================================================
# Steps (all idempotent — safe to re-run):
#   1. master.tm_companies row (B2B default, business_type configurable)
#   2. master.tm_vehicle_imei_map untouched (IMEIs are registered separately)
#   3. CREATE SCHEMA adatrack_gps_{code}
#   4. apply EVERY company migration (+ per-role menu access seed, tm_role_menu_access)
#   5. ledger verification per tenant
#
# Usage:
#   scripts/provision-tenant.sh <COMPANY_CODE> [Company Name] [b2b|b2c]
# Example:
#   scripts/provision-tenant.sh ACME "PT Acme Logistik" b2b
#
# NOTE: the tenant admin account (Admin@123, must_change_password) and the
# `POST /api/v1/companies` endpoint are delivered in B2 (FR-5.5); this script
# provisions the schema + master registry that B2 builds upon.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

if [[ $# -lt 1 ]]; then
  echo "usage: provision-tenant.sh <COMPANY_CODE> [Company Name] [b2b|b2c]" >&2
  exit 1
fi

CODE="$(echo "$1" | tr '[:lower:]' '[:upper:]' | tr -cd 'A-Z0-9_')"
NAME="${2:-$CODE Company}"
BUSINESS_TYPE="${3:-b2b}"

if [[ -z "$CODE" ]]; then
  echo "provision-tenant: company code must contain [A-Z0-9_]" >&2
  exit 1
fi
if [[ "$BUSINESS_TYPE" != "b2b" && "$BUSINESS_TYPE" != "b2c" ]]; then
  echo "provision-tenant: business_type must be b2b|b2c" >&2
  exit 1
fi

load_variant_env "${COMPOSE_VARIANT:-local}"
"$ROOT/scripts/pg-wait.sh" 60 >&2

SCHEMA="${COMPANY_DB_PREFIX:-adatrack_gps_}$(echo "$CODE" | tr '[:upper:]' '[:lower:]')"

echo "provision-tenant: code=$CODE schema=$SCHEMA business_type=$BUSINESS_TYPE"

echo "provision-tenant: [1/4] master registry row (tm_companies)"
PGOPTIONS="-c search_path=${MASTER_SCHEMA}" psql_q -c "
INSERT INTO tm_companies (code, name, country_code, business_type, timezone, is_active, activated_at)
VALUES ('${CODE}', '${NAME}', 'ID', '${BUSINESS_TYPE}', 'Asia/Jakarta', TRUE, CURRENT_TIMESTAMP)
ON CONFLICT (code) DO UPDATE SET
  name = EXCLUDED.name,
  business_type = EXCLUDED.business_type,
  is_active = TRUE;"

echo "provision-tenant: [2/4] create schema"
psql_q -c "CREATE SCHEMA IF NOT EXISTS ${SCHEMA};"

echo "provision-tenant: [3/4] company migrations"
apply_dir "company:${SCHEMA}" "$SCHEMA" "$MIGRATIONS_COMPANY"

echo "provision-tenant: [4/4] ledger verification"
ledger_report "$SCHEMA"

echo "provision-tenant: done (${CODE})"