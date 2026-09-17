#!/bin/bash
set -e

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <tenant_code> <tenant_name>"
    exit 1
fi

CODE=$1
NAME=$2

# Get SuperAdmin token
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login -d '{"email":"superadmin@adatrack.local","password":"SuperAdmin@123"}' | grep -o '"access_token":"[^"]*' | grep -o '[^"]*$')

if [ -z "$TOKEN" ]; then
    echo "Failed to login as SuperAdmin"
    exit 1
fi

# Provision Tenant
curl -s -X POST http://localhost:8080/api/v1/companies \
    -H "Authorization: Bearer $TOKEN" \
    -d "{\"code\":\"$CODE\",\"name\":\"$NAME\"}"

echo -e "\nProvisioned tenant: $CODE"
