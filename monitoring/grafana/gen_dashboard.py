#!/usr/bin/env python3
"""Generate the 'adatrack Core' Grafana dashboard (B4 polish).

Output: backend/monitoring/grafana/dashboards/fleet-core.json
Run: python3 gen_dashboard.py
"""
import json, os

OUT = os.path.join(os.path.dirname(__file__), "dashboards", "fleet-core.json")
# Datasource is provisioned as DEFAULT (isDefault: true) via
# provisioning/datasources/datasource.yml. Panels reference the default
# datasource with `null` (same as the original fleet-core.json) so import
# never breaks on a mismatch of auto-generated UIDs.
DS = None


def ds():
    return DS


def timeseries(title, expr, legend=None, unit=None, decimals=None, fill=15,
               y=None, h=7, x=0, w=12, thresholds=None, draw="line"):
    targets = [{
        "expr": expr,
        "legendFormat": legend or "{{measurement}}",
        "refId": "A",
    }]
    fcd = {
        "defaults": {
            "unit": unit or "short",
            "decimals": decimals if decimals is not None else 2,
            "lineWidth": 2,
            "fillOpacity": fill,
            "custom": {"drawStyle": draw, "lineInterpolation": "smooth"},
        },
        "overrides": [],
    }
    if thresholds:
        fcd["defaults"]["thresholds"] = {"mode": "absolute", "steps": thresholds}
    return {
        "id": None,
        "title": title,
        "type": "timeseries",
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "datasource": ds(),
        "targets": targets,
        "fieldConfig": fcd,
        "options": {
            "tooltip": {"mode": "single" if len(targets) == 1 else "multi", "sort": "none"},
            "legend": {"displayMode": "list" if len(targets) <= 6 else "table", "placement": "bottom"},
        },
        "pluginVersion": "11.1.0",
    }


def stat(title, expr, unit="short", decimals=0, thresholds=None, legend=None,
         y=None, h=5, x=0, w=4, graph="none", color_mode="value"):
    targets = [{"expr": expr, "instant": True, "refId": "A"}]
    if legend:
        targets[0]["legendFormat"] = legend
    fcd = {
        "defaults": {"unit": unit, "decimals": decimals, "mappings": [{"type": "value", "options": {}}]},
        "overrides": [],
    }
    if thresholds:
        fcd["defaults"]["thresholds"] = {"mode": "absolute", "steps": thresholds}
    return {
        "id": None,
        "title": title,
        "type": "stat",
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "datasource": ds(),
        "targets": targets,
        "fieldConfig": fcd,
        "options": {
            "colorMode": color_mode,
            "graphMode": graph,
            "justifyMode": "auto",
            "orientation": "auto",
            "reduceOptions": {"values": False, "calcs": ["lastNotNull"], "fields": ""},
            "textMode": "auto",
        },
        "pluginVersion": "11.1.0",
    }


def gauge(title, expr, unit="percent", decimals=1, minv=0, maxv=100,
          thresholds=None, y=None, h=6, x=0, w=6):
    fcd = {
        "defaults": {"unit": unit, "decimals": decimals, "min": minv, "max": maxv},
        "overrides": [],
    }
    if thresholds:
        fcd["defaults"]["thresholds"] = {"mode": "absolute", "steps": thresholds}
    return {
        "id": None,
        "title": title,
        "type": "gauge",
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "datasource": ds(),
        "targets": [{"expr": expr, "instant": True, "refId": "A"}],
        "fieldConfig": fcd,
        "options": {
            "reduceOptions": {"calcs": ["lastNotNull"], "values": False},
            "showThresholdLabels": False,
            "showThresholdMarkers": True,
        },
        "pluginVersion": "11.1.0",
    }


def row(title, y=None, h=1, x=0, w=24):
    return {
        "id": None,
        "title": title,
        "type": "row",
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "collapsed": False,
    }


panels = []
y = 0

# ---------- Section 1: SLO Overview ----------
panels.append(row("SLO: Availability & Error Budget", y=y)); y += 1

panels.append(stat(
    "Availability (30d)",
    "adatrack:slo_availability_ratio_30d * 100",
    unit="percent", decimals=3, y=y, x=0, w=4, h=5,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 99.9}, {"color": "red", "value": 99.0}],
))
panels.append(stat(
    "Availability (5m)",
    "adatrack:slo_availability_ratio_5m * 100",
    unit="percent", decimals=3, y=y, x=4, w=4, h=5,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 99.9}],
))
panels.append(gauge(
    "Error Budget Remaining",
    "adatrack:slo_error_budget_remaining * 100",
    unit="percent", decimals=2, minv=0, maxv=100, y=y, x=8, w=6, h=5,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 20}, {"color": "red", "value": 0}],
))
panels.append(stat(
    "Services Down",
    "sum(up{job=\"adatrack-services\"}==0)",
    unit="short", decimals=0, y=y, x=14, w=4, h=5,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
))
panels.append(stat(
    "Services Up (of 6)",
    "sum(up{job=\"adatrack-services\"}==1)",
    unit="short", decimals=0, y=y, x=18, w=4, h=5,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 5}],
))
panels.append(timeseries(
    "SLO Availability (5m) trend",
    "adatrack:slo_availability_ratio_5m * 100",
    legend="Availability %", unit="percent", decimals=3, y=y+5, h=6, x=0, w=12,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 99.9}],
))
panels.append(timeseries(
    "Error budget burn rate (5m)",
    "(1 - adatrack:slo_availability_ratio_5m) / 0.001",
    legend="Budget consumed (fraction)", unit="short", decimals=3, y=y+5, h=6, x=12, w=12,
))
y += 11

# ---------- Section 2: Telemetry Data Pipeline ----------
panels.append(row("Telemetry Data Pipeline (ingestion / persistence)", y=y)); y += 1

panels.append(timeseries(
    "Ingestion rate (packets/sec)",
    "rate(ingestion_packets_total{protocol=~\"$protocol\"}[$__rate_interval])",
    legend="{{protocol}}", unit="short", decimals=1, y=y, h=7, x=0, w=12,
))
panels.append(timeseries(
    "Parsed messages (msg/sec)",
    "rate(ingestion_parsed_messages_total{protocol=~\"$protocol\"}[$__rate_interval])",
    legend="{{protocol}}", unit="short", decimals=1, y=y, h=7, x=12, w=12,
))
panels.append(timeseries(
    "Rejected packets (rate)",
    "rate(ingestion_rejected_total[$__rate_interval])",
    legend="{{reason}}", unit="short", decimals=1, y=y+7, h=7, x=0, w=12,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 0.01}, {"color": "red", "value": 0.1}],
))
panels.append(timeseries(
    "Persistence processed (msg/sec)",
    "rate(messages_processed_total{company_code=~\"$company_code\"}[$__rate_interval])",
    legend="{{company_code}}", unit="short", decimals=1, y=y+7, h=7, x=12, w=12,
))
y += 14

# ---------- Section 3: Service Health / HTTP ----------
panels.append(row("Service Health (HTTP / API)", y=y)); y += 1

panels.append(stat(
    "HTTP 5xx rate",
    "sum(rate(http_requests_total{status=~\"5..\"}[5m])) / clamp_min(sum(rate(http_requests_total[5m])), 1)",
    unit="percent", decimals=2, y=y, x=0, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 1}, {"color": "red", "value": 5}],
))
panels.append(stat(
    "HTTP P99 latency",
    "histogram_quantile(0.99, sum by(le) (rate(http_request_duration_seconds_bucket[5m])))",
    unit="s", decimals=3, y=y, x=3, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 0.5}, {"color": "red", "value": 1}],
))
panels.append(stat(
    "Tenant resolution P99",
    "histogram_quantile(0.99, sum by(le) (rate(tenant_resolution_duration_ms_bucket[5m])))",
    unit="ms", decimals=1, y=y, x=6, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 500}],
))
panels.append(stat(
    "Tenant lookup err rate",
    "sum(rate(tenant_lookup_errors_total[5m])) / clamp_min(sum(rate(nats_messages_consumed_total[5m])), 1)",
    unit="percent", decimals=2, y=y, x=9, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 5}],
))
panels.append(timeseries(
    "HTTP request rate by endpoint",
    "sum by(endpoint, status) (rate(http_requests_total[30s]))",
    legend="{{endpoint}} ({{status}})", unit="short", decimals=1, y=y, h=6, x=12, w=12,
))
panels.append(timeseries(
    "HTTP P99 by endpoint",
    "histogram_quantile(0.99, sum by(le, endpoint) (rate(http_request_duration_seconds_bucket[5m])))",
    legend="{{endpoint}}", unit="s", decimals=3, y=y+6, h=6, x=0, w=12,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
))
panels.append(timeseries(
    "RBAC check P99 by action",
    "histogram_quantile(0.99, sum by(le, action) (rate(rbac_check_duration_seconds_bucket[5m])))",
    legend="{{action}}", unit="s", decimals=3, y=y+6, h=6, x=12, w=12,
))
y += 12

# ---------- Section 4: WebSocket ----------
panels.append(row("WebSocket / Real-time", y=y)); y += 1

panels.append(stat(
    "Active WS connections",
    "ws_connections_active",
    unit="short", decimals=0, y=y, x=0, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 4000}, {"color": "red", "value": 4750}],
))
panels.append(stat(
    "WS msgs sent (5m)",
    "increase(ws_messages_total{direction=\"send\"}[5m])",
    unit="short", decimals=0, y=y, x=3, w=3, h=6,
))
panels.append(stat(
    "WS msgs dropped (5m)",
    "increase(ws_messages_total{direction=\"dropped\"}[5m])",
    unit="short", decimals=0, y=y, x=6, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
))
panels.append(timeseries(
    "WS broadcast P99 latency",
    "histogram_quantile(0.99, sum by(le) (rate(ws_message_duration_seconds_bucket[5m])))",
    legend="P99", unit="s", decimals=3, y=y, h=6, x=9, w=7,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 0.5}, {"color": "red", "value": 1}],
))
panels.append(timeseries(
    "WS messages rate per topic",
    "rate(ws_messages_total{direction=\"send\"}[$__rate_interval])",
    legend="{{topic}}", unit="short", decimals=1, y=y, h=6, x=16, w=8,
))
y += 6

# ---------- Section 5: NATS ----------
panels.append(row("NATS Message Broker", y=y)); y += 1

panels.append(stat(
    "NATS pending (total)",
    "sum(nats_pending_messages)",
    unit="short", decimals=0, y=y, x=0, w=4, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 5000}, {"color": "red", "value": 9000}],
))
panels.append(timeseries(
    "NATS pending per subject",
    "nats_pending_messages",
    legend="{{subject}}", unit="short", decimals=0, y=y, h=6, x=4, w=8,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 5000}],
))
panels.append(timeseries(
    "NATS consumed (msg/sec per queue group)",
    "rate(nats_messages_consumed_total[$__rate_interval])",
    legend="{{queue_group}} ({{subject}})", unit="short", decimals=1, y=y, h=6, x=12, w=12,
))
panels.append(stat(
    "NATS publish P99",
    "histogram_quantile(0.99, sum by(le) (rate(nats_publish_duration_ms_bucket[5m])))",
    unit="ms", decimals=1, y=y+6, x=0, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 10}, {"color": "red", "value": 100}],
))
panels.append(timeseries(
    "NATS published (msg/sec)",
    "rate(nats_messages_published_total[$__rate_interval])",
    legend="{{subject}}", unit="short", decimals=1, y=y+6, h=6, x=3, w=9,
))
panels.append(timeseries(
    "NATS publish P99 by company",
    "histogram_quantile(0.99, sum by(le, company_code) (rate(nats_publish_duration_ms_bucket[5m])))",
    legend="{{company_code}}", unit="ms", decimals=1, y=y+6, h=6, x=12, w=12,
))
y += 12

# ---------- Section 6: DB / Persistence ----------
panels.append(row("Database & Persistence", y=y)); y += 1

panels.append(gauge(
    "Company DB pool usage (%)",
    "max by(company_code) (mysql_pool_connections_active) / 50 * 100",
    unit="percent", decimals=0, minv=0, maxv=100, y=y, x=0, w=6, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 80}, {"color": "red", "value": 90}],
))
panels.append(stat(
    "Batch insert P99",
    "histogram_quantile(0.99, sum by(le) (rate(mysql_insert_duration_seconds_bucket[5m])))",
    unit="s", decimals=3, y=y, x=6, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 1}, {"color": "red", "value": 10}],
))
panels.append(stat(
    "Batch insert errors (5m)",
    "increase(batch_insert_errors_total[5m])",
    unit="short", decimals=0, y=y, x=9, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
))
panels.append(stat(
    "Retry attempts (5m)",
    "increase(retry_attempts_total[5m])",
    unit="short", decimals=0, y=y, x=12, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 10}, {"color": "red", "value": 100}],
))
panels.append(timeseries(
    "Batch insert P99 by table",
    "histogram_quantile(0.99, sum by(le, table) (rate(mysql_insert_duration_seconds_bucket[5m])))",
    legend="{{table}}", unit="s", decimals=3, y=y, h=6, x=15, w=9,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 10}],
))
panels.append(timeseries(
    "DB read queries (per route)",
    "rate(db_read_queries_total{company_code=~\"$company_code\"}[$__rate_interval])",
    legend="{{company_code}} -> {{route}}", unit="short", decimals=1, y=y+6, h=6, x=0, w=12,
))
panels.append(timeseries(
    "DB replica up (per company)",
    "db_replica_up{company_code=~\"$company_code\"}",
    legend="{{company_code}}", unit="short", decimals=0, y=y+6, h=6, x=12, w=12,
    thresholds=[{"color": "red", "value": 0}, {"color": "green", "value": 1}],
))
y += 12

# ---------- Section 7: Redis / Live state ----------
panels.append(row("Redis & Live State", y=y)); y += 1

panels.append(stat(
    "Redis ops P99 (ms)",
    "histogram_quantile(0.99, sum by(le) (rate(redis_operation_duration_seconds_bucket[5m]))) * 1000",
    unit="ms", decimals=2, y=y, x=0, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 25}, {"color": "red", "value": 100}],
))
panels.append(stat(
    "Redis ops errors (5m)",
    "increase(redis_operations_total{status=\"error\"}[5m])",
    unit="short", decimals=0, y=y, x=3, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
))
panels.append(stat(
    "Vehicle state updates (5m)",
    "increase(vehicle_state_updates_total[5m])",
    unit="short", decimals=0, y=y, x=6, w=3, h=6,
))
panels.append(timeseries(
    "Redis operations (rate per command)",
    "rate(redis_operations_total[$__rate_interval])",
    legend="{{command}} ({{status}})", unit="short", decimals=1, y=y, h=6, x=9, w=15,
))
panels.append(timeseries(
    "Redis op P99 by command",
    "histogram_quantile(0.99, sum by(le, command) (rate(redis_operation_duration_seconds_bucket[5m]))) * 1000",
    legend="{{command}}", unit="ms", decimals=2, y=y+6, h=6, x=0, w=12,
))
panels.append(timeseries(
    "Redis batch size (avg/flush)",
    "redis_batch_size",
    legend="batch size", unit="short", decimals=0, y=y+6, h=6, x=12, w=12,
))
y += 12

# ---------- Section 8: Alerts / SOS / Notifications ----------
panels.append(row("Alerts, SOS & Notifications", y=y)); y += 1

panels.append(timeseries(
    "Alerts created (rate per type/severity)",
    "rate(alerts_created_total{company=~\"$company_code\"}[$__rate_interval])",
    legend="{{company}}/{{type}}/{{severity}}", unit="short", decimals=1, y=y, h=7, x=0, w=12,
))
panels.append(stat(
    "SOS escalations (5m)",
    "increase(sos_escalations_total[5m])",
    unit="short", decimals=0, y=y, x=12, w=3, h=7,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
))
panels.append(stat(
    "SOS TTA P95 (s)",
    "histogram_quantile(0.95, sum by(le) (rate(sos_time_to_acknowledge_seconds_bucket[5m])))",
    unit="s", decimals=1, y=y, x=15, w=3, h=7,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 60}, {"color": "red", "value": 300}],
))
panels.append(timeseries(
    "Notifications sent (rate per channel/status)",
    "rate(notifications_sent_total{company=~\"$company_code\"}[$__rate_interval])",
    legend="{{company}}/{{channel}}/{{status}}", unit="short", decimals=1, y=y+7, h=7, x=0, w=12,
))
panels.append(timeseries(
    "NATS alerts published (rate)",
    "rate(nats_alerts_published_total[$__rate_interval])",
    legend="{{subject}}", unit="short", decimals=1, y=y+7, h=7, x=12, w=12,
))
y += 14

# ---------- Section 9: Infrastructure ----------
panels.append(row("Infrastructure (Host / Container / Go runtime)", y=y)); y += 1

panels.append(timeseries(
    "Host CPU usage (%)",
    "100 - avg by(instance) (rate(node_cpu_seconds_total{mode=\"idle\"}[5m])) * 100",
    legend="{{instance}}", unit="percent", decimals=1, y=y, h=7, x=0, w=12,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 70}, {"color": "red", "value": 85}],
))
panels.append(timeseries(
    "Container CPU usage (%)",
    "rate(container_cpu_usage_seconds_total{container!=\"\"}[5m]) * 100",
    legend="{{container}}", unit="percent", decimals=1, y=y, h=7, x=12, w=12,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 70}, {"color": "red", "value": 85}],
))
panels.append(timeseries(
    "Host memory usage (%)",
    "(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) * 100",
    legend="{{instance}}", unit="percent", decimals=1, y=y+7, h=7, x=0, w=12,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 80}, {"color": "red", "value": 90}],
))
panels.append(timeseries(
    "Container memory (GB)",
    "container_memory_working_set_bytes{container!=\"\"} / 1e9",
    legend="{{container}}", unit="short", decimals=2, y=y+7, h=7, x=12, w=12,
))
panels.append(timeseries(
    "Go goroutines (per service)",
    "go_goroutines{job=\"adatrack-services\"}",
    legend="{{instance}}", unit="short", decimals=0, y=y+14, h=7, x=0, w=12,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 1000}, {"color": "red", "value": 2000}],
))
panels.append(timeseries(
    "Go heap allocated (bytes)",
    "go_memstats_alloc_bytes{job=\"adatrack-services\"}",
    legend="{{instance}}", unit="bytes", decimals=0, y=y+14, h=7, x=12, w=12,
))
y += 21

# ---------- Section 10: Fuel Sensor (B5a) ----------
panels.append(row("Fuel Sensor (B5a)", y=y)); y += 1

panels.append(timeseries(
    "Fuel readings (rate per protocol)",
    "rate(fuel_readings_total[$__rate_interval])",
    legend="{{protocol}}", unit="short", decimals=1, y=y, h=6, x=0, w=12,
))
panels.append(stat(
    "Fuel rows positionless (5m)",
    "increase(fuel_rows_positionless_total[5m])",
    unit="short", decimals=0, y=y, x=12, w=4, h=6,
))
panels.append(stat(
    "Fuel ACC-suppressed (5m)",
    "increase(alerts_fuel_acc_suppressed_total[5m])",
    unit="short", decimals=0, y=y, x=16, w=4, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 1}],
))
y += 7

# ---------- Section 10: Media (B5b) ----------
panels.append(row("Media Pipeline (B5b dashcam)", y=y)); y += 1

panels.append(stat(
    "Media uploads (5m)",
    "increase(media_uploads_total[5m])",
    unit="short", decimals=0, y=y, x=0, w=3, h=6,
))
panels.append(stat(
    "Media bytes uploaded (5m)",
    "increase(media_upload_bytes_total[5m])",
    unit="bytes", decimals=0, y=y, x=3, w=3, h=6,
))
panels.append(stat(
    "Presigned URLs (rate)",
    "rate(media_presigned_total[5m])",
    unit="short", decimals=2, y=y, x=6, w=3, h=6,
))
panels.append(stat(
    "Ingest errors (5m)",
    "increase(media_ingest_errors_total[5m])",
    unit="short", decimals=0, y=y, x=9, w=3, h=6,
    thresholds=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
))
panels.append(timeseries(
    "Storage objects per bucket",
    "storage_objects",
    legend="{{bucket}}", unit="short", decimals=0, y=y, h=6, x=12, w=6,
))
panels.append(timeseries(
    "Cleanup deleted (rate)",
    "rate(media_cleanup_deleted_total[$__rate_interval])",
    legend="deleted/sec", unit="short", decimals=1, y=y, h=6, x=18, w=6,
))

# normalize ids sequentially
for i, p in enumerate(panels):
    p["id"] = i + 1

dashboard = {
    "__inputs": [],
    "__requires": [
        {"type": "grafana", "id": "grafana", "name": "Grafana", "version": "11.1.0"},
        {"type": "datasource", "id": "prometheus", "name": "Prometheus", "version": "1.0.0"},
    ],
    "annotations": {
        "list": [
            {
                "builtIn": 0,
                "datasource": None,
                "enable": True, "hide": True, "iconColor": "rgba(0, 211, 255, 1)",
                "name": "Alerts",
                "target": {"limit": 100, "matchAny": False},
                "type": "dashboard",
            }
        ]
    },
    "description": (
        "Comprehensive observability dashboard for the adatrack GPS platform (Phase B4). "
        "Covers SLO availability (99.9%), the real-time telemetry data pipeline "
        "(ingestion -> NATS -> persistence -> Redis -> WebSocket), alert/notification "
        "lifecycle (incl. SOS TTA, fuel), media pipeline (B5b), and "
        "host/container/DB infrastructure."
    ),
    "editable": True,
    "fiscalYearStartMonth": 0,
    "graphTooltip": 0,
    "id": None,
    "links": [
        {
            "asDropdown": False, "newTab": False, "text": "Runbook",
            "tooltip": "docs/INCIDENT_RUNBOOK.md", "type": "url",
            "url": "https://github.com/adatrack-gps/ajb_gps/blob/main/docs/INCIDENT_RUNBOOK.md",
        },
        {
            "asDropdown": False, "newTab": False, "text": "HA Guide",
            "tooltip": "docs/HIGH_AVAILABILITY.md", "type": "url",
            "url": "https://github.com/adatrack-gps/ajb_gps/blob/main/docs/HIGH_AVAILABILITY.md",
        },
    ],
    "liveUrl": "",
    "panels": panels,
    "refresh": "30s",
    "schemaVersion": 40,
    "style": "dark",
    "tags": ["adatrack", "gps", "real-time", "slo", "monitoring", "b4"],
    "templating": {
        "list": [
            {
                "name": "service", "type": "custom", "label": "Service",
                "query": (
                    "ingestion-tcp,worker-persistence,worker-live,worker-alert,"
                    "service-websocket,api-vehicle,service-media"
                ),
                "current": {"selected": True, "text": "All", "value": "$__all"},
                "includeAll": True, "multi": True, "allValue": ".*",
                "refresh": 1, "hide": 0,
            },
            {
                "name": "company_code", "type": "query", "label": "Company / Tenant",
                "datasource": None,
                "query": "label_values(company_db_pool_count, company_code)",
                "current": {"selected": True, "text": "All", "value": "$__all"},
                "includeAll": True, "multi": True, "allValue": ".*",
                "refresh": 2, "hide": 0,
            },
            {
                "name": "protocol", "type": "query", "label": "Protocol",
                "datasource": None,
                "query": "label_values(ingestion_packets_total, protocol)",
                "current": {"selected": True, "text": "All", "value": "$__all"},
                "includeAll": True, "multi": True, "allValue": ".*",
                "refresh": 1, "hide": 0,
            },
        ]
    },
    "time": {"from": "now-24h", "to": "now"},
    "timepicker": {"refresh_intervals": ["30s", "1m", "5m", "15m", "1h"]},
    "timezone": "",
    "title": "adatrack Core",
    "uid": "adatrack-core",
    "version": 2,
}

os.makedirs(os.path.dirname(OUT), exist_ok=True)
with open(OUT, "w") as f:
    json.dump(dashboard, f, indent=2, sort_keys=False)
    f.write("\n")

print(f"Wrote {OUT} with {len(panels)} panels")