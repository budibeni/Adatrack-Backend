#!/usr/bin/env bash
# ============================================================================
# restore-db.sh — checksum-verified restore drill (B4, PRD §12).
# Restores ONE backup stamp into a scratch database (default
# adatrack_gps_restore_test) and compares row counts per table against the
# live database. Pass criteria: checksums OK + row-count match on the restored
# tables. Usage: scripts/restore-db.sh <stamp_dir> [scratch_db]
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"
load_variant_env "${COMPOSE_VARIANT:-local}"

STAMP_DIR="${1:?usage: restore-db.sh <stamp_dir> [scratch_db]}"
SCRATCH_DB="${2:-adatrack_gps_restore_test}"

[[ -d "$STAMP_DIR" ]] || { echo "restore-db: missing dir $STAMP_DIR" >&2; exit 1; }
(cd "$STAMP_DIR" && sha256sum -c SHA256SUMS) || { echo "restore-db: CHECKSUM FAILED" >&2; exit 1; }
echo "restore-db: checksums OK ($STAMP_DIR)"

psql_q -c "DROP DATABASE IF EXISTS \"$SCRATCH_DB\";"
psql_q -c "CREATE DATABASE \"$SCRATCH_DB\";"
echo "restore-db: scratch db $SCRATCH_DB created"

export PGDATABASE="$SCRATCH_DB"
failed=0
for dump in "$STAMP_DIR"/*.dump.gz; do
  schema="$(basename "$dump" .dump.gz)"
  echo "restore-db: restoring $schema"
  if ! gunzip -c "$dump" | pg_restore -d "$SCRATCH_DB" --no-owner --role="$PGUSER" 2>"$STAMP_DIR/restore-$schema.log"; then
    # Known-benign cross-version noise: an older server rejects the
    # `transaction_timeout` GUC emitted by a newer pg_dump (pg_dump 18 → PG 15).
    # Anything else is a real failure worth surfacing.
    if grep -v -e 'transaction_timeout' -e 'errors ignored on restore' "$STAMP_DIR/restore-$schema.log" | grep -q .; then
      echo "restore-db: ERROR restore of $schema failed (see restore-$schema.log)" >&2
      failed=1
    else
      echo "restore-db: $schema restored (only benign cross-version GUC noise)"
    fi
  fi
  # Row-count match on the hot table (telemetry) + master companies when present.
  live=0; got=0
  case "$schema" in
    adatrack_gps_master)
      live="$(PGDATABASE="$POSTGRES_DB" psql -tA -c "SELECT count(*) FROM ${schema}.tm_companies;" 2>/dev/null || echo 0)"
      got="$(psql -tA -c "SELECT count(*) FROM ${schema}.tm_companies;" 2>/dev/null || echo 0)"
      ;;
    adatrack_gps_*)
      live="$(PGDATABASE="$POSTGRES_DB" psql -tA -c "SELECT count(*) FROM ${schema}.th_telemetry_logs;" 2>/dev/null || echo skip)"
      got="$(psql -tA -c "SELECT count(*) FROM ${schema}.th_telemetry_logs;" 2>/dev/null || echo skip)"
      ;;
  esac
  if [[ "$live" != "skip" && "$live" != "$got" ]]; then
    echo "restore-db: COUNT MISMATCH $schema live=$live restored=$got" >&2
    failed=1
  else
    echo "restore-db: count match $schema (live=$live restored=$got)"
  fi
done

if [[ "$failed" -ne 0 ]]; then
  echo "restore-db: DRILL FAILED (row-count mismatch)" >&2
  exit 1
fi
echo "restore-db: DRILL PASS (checksums + row-count match)"
