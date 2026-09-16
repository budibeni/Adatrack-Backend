#!/bin/bash
COMPANY_CODE=$1
if [ -z "$COMPANY_CODE" ]; then
    echo "Usage: $0 <company_code>"
    exit 1
fi
echo "Provisioning tenant schema for $COMPANY_CODE..."
# In a real scenario, this would call an API or run a sql script via docker exec.
