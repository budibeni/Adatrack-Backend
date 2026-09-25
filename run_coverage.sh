#!/bin/bash
export PATH=$PATH:/usr/local/go/bin
echo "" > coverage.txt
for d in internal services/api-vehicle services/foundation-check services/ingestion-tcp services/service-websocket services/worker-alert services/worker-live services/worker-persistence; do
    cd $d
    go test -coverprofile=profile.out ./... > /dev/null 2>&1
    if [ -f profile.out ]; then
        cat profile.out >> ../../coverage.txt
        rm profile.out
    fi
    cd - > /dev/null
done
# Clean up duplicate "mode: set" lines
grep -v "mode: set" coverage.txt > coverage_clean.txt
echo "mode: set" > final_coverage.out
cat coverage_clean.txt >> final_coverage.out
go tool cover -func=final_coverage.out | tail -n 1
