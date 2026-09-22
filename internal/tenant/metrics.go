package tenant

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Tenant metrics (PRD §10.1): tenant_resolution_duration_ms,
// tenant_lookup_errors_total, company_db_pool_count.
var (
	resolutionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "tenant_resolution_duration_ms",
		Help:    "IMEI → tenant resolution latency in milliseconds",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 12),
	})

	lookupErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tenant_lookup_errors_total",
		Help: "IMEI lookups that failed (unknown/disabled device — anti-spoofing, FR-1.4)",
	})

	companyPoolCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "company_db_pool_count",
		Help: "Number of pre-warmed company database pools",
	})

	cacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tenant_cache_hits_total",
		Help: "IMEI lookups served from the Redis cache",
	})

	cacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tenant_cache_misses_total",
		Help: "IMEI lookups that had to hit the master database",
	})

	// Read/write split (PRD §13): where reads were served from, whether the
	// per-tenant replica is usable, and how often a replica read had to fall
	// back to the primary.
	dbReadQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "db_read_queries_total",
		Help: "Read queries by route: replica (split) or primary (§13)",
	}, []string{"company_code", "route"})

	dbReplicaUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "db_replica_up",
		Help: "1 when the per-tenant read replica is usable, 0 otherwise (§13)",
	}, []string{"company_code"})

	dbReplicaFallbacks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "db_replica_fallbacks_total",
		Help: "Replica read failures that were retried once on the primary (§13)",
	})
)

// RegisterMetrics registers the tenant collectors on a service registry.
// Safe to call once per process; a nil registry is ignored (unit tests).
func RegisterMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	reg.MustRegister(resolutionDuration, lookupErrors, companyPoolCount, cacheHits, cacheMisses,
		dbReadQueries, dbReplicaUp, dbReplicaFallbacks)
}

// observeResolution records a resolution latency (milliseconds).
func observeResolution(d time.Duration) {
	resolutionDuration.Observe(float64(d.Microseconds()) / 1000.0)
}

// incLookupError counts a rejected/unknown IMEI lookup.
func incLookupError() { lookupErrors.Inc() }
