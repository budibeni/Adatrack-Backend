#!/bin/bash
set -e

COMPANY_CODE=$1
if [ -z "$COMPANY_CODE" ]; then
    echo "Usage: $0 <company_code>"
    exit 1
fi

# Convert to lowercase
LOWER_CODE=$(echo "$COMPANY_CODE" | tr '[:upper:]' '[:lower:]')
SCHEMA_NAME="adatrack_gps_$LOWER_CODE"

echo "Provisioning tenant schema: $SCHEMA_NAME"

docker exec -i adatrack_postgres_local psql -U adatrack_admin -d adatrack_gps_master -c "CREATE SCHEMA IF NOT EXISTS $SCHEMA_NAME;"

# Clone from template (this requires pg_dump/restore or a specific custom tool, 
# but for our dev bootstrap we can just apply the template script and change search_path via sed).
cat ./database/init-pg/02_company_template.sql \
    | sed "s/adatrack_gps_template/$SCHEMA_NAME/g" \
    | docker exec -i adatrack_postgres_local psql -U adatrack_admin -d adatrack_gps_master

echo "Tenant $SCHEMA_NAME successfully provisioned!"
