package tenant

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metricsRegisterer is the smallest interface we need to register metrics.
type metricsRegisterer interface {
	Register(prometheus.Collector) error
}

// tenantMetrics implements PRD §8.1 tenant metrics:
//   - tenant_resolution_duration_ms   (IMEI → company_code lookup latency)
//   - tenant_lookup_errors_total      (failed IMEI-to-tenant lookups)
//   - company_db_pool_count           (warmed company pools)
//   - mysql_pool_connections_active   (per company DB active connections)
type tenantMetrics struct {
	resolutionDuration prometheus.Histogram
	lookupErrors       prometheus.Counter
	poolCount          *prometheus.GaugeVec
	poolActive         *prometheus.GaugeVec
	readRoute          *prometheus.CounterVec // db_read_queries_total{company_code,route}
	replicaUp          *prometheus.GaugeVec   // db_replica_up{company_code}
}

// Names diambil dari PRD §8.1 (tenant metrics).
const (
	metricTenantResolutionDuration = "tenant_resolution_duration_ms"
	metricTenantLookupErrors       = "tenant_lookup_errors_total"
	metricCompanyDBPoolCount       = "company_db_pool_count"
	metricMySQLPoolActive          = "mysql_pool_connections_active"
	// Read/write split (B4 HA):
	metricDBReadQueries = "db_read_queries_total"
	metricDBReplicaUp   = "db_replica_up"
)

func newTenantMetrics(reg metricsRegisterer) *tenantMetrics {
	m := &tenantMetrics{
		resolutionDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    metricTenantResolutionDuration,
			Help:    "IMEI → company_code lookup latency in milliseconds",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 12), // 0.5ms … ~2s
		}),
		lookupErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: metricTenantLookupErrors,
			Help: "Failed IMEI-to-company lookups (unregistered devices)",
		}),
		poolCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricCompanyDBPoolCount,
			Help: "Number of active company database pools",
		}, []string{"company_code"}),
		poolActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricMySQLPoolActive,
			Help: "Active connections per company database",
		}, []string{"company_code"}),
		readRoute: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricDBReadQueries,
			Help: "Read-query routing decisions per company (route=replica|primary|primary_fallback)",
		}, []string{"company_code", "route"}),
		replicaUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricDBReplicaUp,
			Help: "1 when the tenant's read replica answered the last probe",
		}, []string{"company_code"}),
	}
	_ = reg.Register(m.resolutionDuration)
	_ = reg.Register(m.lookupErrors)
	_ = reg.Register(m.poolCount)
	_ = reg.Register(m.poolActive)
	_ = reg.Register(m.readRoute)
	_ = reg.Register(m.replicaUp)
	return m
}

func (m *tenantMetrics) observeResolution(d time.Duration) {
	if m != nil {
		m.resolutionDuration.Observe(float64(d.Microseconds()) / 1000.0) // ms
	}
}

func (m *tenantMetrics) incLookupErrors() {
	if m != nil {
		m.lookupErrors.Inc()
	}
}

// setCompany updates pool gauges for a single tenant: pool existence (1/0),
// active (in-use) connections, and total open connections.
func (m *tenantMetrics) setCompany(code string, poolCount, activeConns, openConns float64) {
	if m == nil {
		return
	}
	m.poolCount.WithLabelValues(code).Set(poolCount)
	m.poolActive.WithLabelValues(code).Set(activeConns)
	_ = openConns // reserved: total open connections already reflected by InUse metric set above
}

// incReadRoute counts one read-query routing decision for a tenant
// (route = replica | primary | primary_fallback).
func (m *tenantMetrics) incReadRoute(code, route string) {
	if m == nil {
		return
	}
	m.readRoute.WithLabelValues(code, route).Inc()
}

// setReplicaUp records the prober's view of a tenant's read replica (1/0).
func (m *tenantMetrics) setReplicaUp(code string, up bool) {
	if m == nil {
		return
	}
	v := 0.0
	if up {
		v = 1
	}
	m.replicaUp.WithLabelValues(code).Set(v)
}
