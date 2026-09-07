#!/usr/bin/env python3
"""Validate generated Grafana dashboard: structure + metric cross-check."""
import json, re, pathlib

HERE = pathlib.Path(__file__).resolve().parent
DASH = HERE / "dashboards" / "fleet-core.json"
BACKEND = HERE.parent.parent

d = json.loads(DASH.read_text())
panels = d["panels"]
ids = [x["id"] for x in panels]
assert len(ids) == len(set(ids)), "duplicate panel ids"
rows = [p for p in panels if p["type"] == "row"]
data = [p for p in panels if p["type"] != "row"]


def ok_grid(gp: dict) -> bool:
    return (gp["x"] >= 0 and gp["y"] >= 0 and gp["w"] > 0 and gp["h"] > 0)


for x in panels:

    assert ok_grid(x["gridPos"]), x["title"]
    if x["type"] != "row":
        assert x["datasource"] is None, x["title"]
        assert len(x["targets"]) >= 1, x["title"]

exprs = [t["expr"] for p in data for t in p["targets"]]
cands = set()
SKIP = {"rate","sum","avg","max","min","count","by","le","clamp_min",
         "histogram_quantile","increase","reset","time","last","scalar",
         "prometheus","label_values","true","false","null","e9","1e","3e"}
for e in exprs:

    for m in re.finditer(r"([A-Za-z_:][A-Za-z0-9_:]*)\s*(\{|\[|$)", e):
        n = m.group(1)
        if n.startswith("$") or n in SKIP:
            continue
        cands.add(n)

src = set()
for p in BACKEND.rglob("*.go"):
    t = p.read_text(errors="ignore")
    src |= set(re.findall(r'Name:\s*"([A-Za-z0-9_:]+)"', t))
    src |= set(re.findall(r'=\s*"([A-Za-z0-9_:]+)"', t))

KNOWN = {"up", "go_goroutines", "go_memstats_alloc_bytes",
          "node_cpu_seconds_total", "node_memory_MemAvailable_bytes",
          "node_memory_MemTotal_bytes", "container_cpu_usage_seconds_total",
          "container_memory_working_set_bytes",
          "adatrack:slo_availability_ratio_5m",
          "adatrack:slo_availability_ratio_30d",
          "adatrack:slo_error_budget_remaining",
          "nats_pending_messages", "company_db_pool_count",
          "mysql_pool_connections_active"}
missing = sorted(n for n in cands if n not in src and n not in KNOWN and not n.endswith("_bucket"))
print(f"rows={len(rows)} data={len(data)} total={len(panels)} unique_ids={len(set(ids))}")
print("templating:", [t["name"] for t in d["templating"]["list"]])
print("missing metric tokens:", missing if missing else "NONE")
assert not missing, "dashboard references unexported metrics"
print("ALL CHECKS PASSED")
