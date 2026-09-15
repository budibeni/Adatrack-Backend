#!/bin/bash
# ============================================================================
# PostgreSQL bootstrap 02b — reference seed (wilayah Indonesia)
# ============================================================================
# Executed by the docker entrypoint after 02_master_setup.sql. Applies every
# generated seed file (countries → provinces → cities → districts → subdistricts)
# into the master schema. All seed statements are idempotent
# (INSERT ... ON CONFLICT DO NOTHING), so re-running is always safe.
#
# The files are generated artifacts (see their headers for provenance) and are
# mounted read-only at /db/seed/reference.
# ============================================================================
set -euo pipefail

MASTER_SCHEMA="${MASTER_DB_NAME:-adatrack_gps_master}"
SEED_DIR="${SEED_DIR:-/db/seed/reference}"

echo "02b-seed-reference: schema=${MASTER_SCHEMA} dir=${SEED_DIR}"

if [[ ! -d "$SEED_DIR" ]]; then
  echo "02b-seed-reference: ${SEED_DIR} not mounted — skipping reference seed" >&2
  exit 0
fi

shopt -s nullglob
for f in $(ls -1 "$SEED_DIR"/*.sql | sort); do
  echo "02b-seed-reference: applying $(basename "$f")"
  PGOPTIONS="-c search_path=${MASTER_SCHEMA}" \
    psql -v ON_ERROR_STOP=1 --no-psqlrc -X -q -d "$POSTGRES_DB" -f "$f"
done

echo "02b-seed-reference: done"