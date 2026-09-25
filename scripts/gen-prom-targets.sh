#!/bin/bash
TARGET_DIR="./deployments/prometheus/targets"
mkdir -p $TARGET_DIR
cat << 'JSON' > $TARGET_DIR/adatrack-services.json
[
  {
    "targets": ["node-exporter:9100", "cadvisor:8080", "postgres-exporter:9187"],
    "labels": {
      "env": "local"
    }
  }
]
JSON
echo "Prometheus targets generated at $TARGET_DIR/adatrack-services.json"
