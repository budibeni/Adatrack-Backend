package internal

import (
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Shared metric variables (PRD §10.1). They are registered once per service by
// GetRegistry(); services add their own collectors on top via RegisterMetrics.
var (
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	NATSMessagesPublished *prometheus.CounterVec
	NATSMessagesConsumed  *prometheus.CounterVec
	NATPendingMessages    *prometheus.GaugeVec

	RedisOperations        *prometheus.CounterVec
	RedisOperationDuration *prometheus.HistogramVec

	PGInsertDuration *prometheus.HistogramVec
	PGInsertErrors   *prometheus.CounterVec
	PGPoolInUse      *prometheus.GaugeVec
	PGPoolOpen       *prometheus.GaugeVec

	// AudioWriteErrorsTotal counts audit/dead-letter write failures — alerting
	// on it is mandatory because "no silent drop" (§9.4, FR-4.3).
	DeadLetterTotal *prometheus.CounterVec

	Goroutines      prometheus.GaugeFunc
	MemoryAllocated prometheus.GaugeFunc
)

// RegisterSharedMetrics creates and registers the shared collectors.
func RegisterSharedMetrics(reg *prometheus.Registry) {
	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests handled",
	}, []string{"service", "method", "endpoint", "status"})

	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
	}, []string{"service", "method", "endpoint"})

	NATSMessagesPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nats_messages_published_total",
		Help: "NATS messages published per subject",
	}, []string{"subject"})

	NATSMessagesConsumed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nats_messages_consumed_total",
		Help: "NATS messages consumed per subject/queue group",
	}, []string{"subject", "queue_group"})

	NATPendingMessages = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nats_pending_messages",
		Help: "Pending NATS messages (stream backlog) at last sample",
	}, []string{"stream"})

	RedisOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "redis_operations_total",
		Help: "Redis operations executed",
	}, []string{"command", "status"})

	RedisOperationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "redis_operation_duration_seconds",
		Help:    "Redis operation latency in seconds",
		Buckets: prometheus.ExponentialBuckets(0.0005, 2, 12),
	}, []string{"command"})

	PGInsertDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "postgres_insert_duration_seconds",
		Help:    "PostgreSQL batch insert duration in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
	}, []string{"table"})

	PGInsertErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "postgres_insert_errors_total",
		Help: "PostgreSQL insert failures",
	}, []string{"table"})

	PGPoolInUse = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "postgres_pool_connections_in_use",
		Help: "PostgreSQL pool connections currently in use",
	}, []string{"pool"})

	PGPoolOpen = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "postgres_pool_connections_open",
		Help: "PostgreSQL pool connections currently open",
	}, []string{"pool"})

	DeadLetterTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "deadletter_total",
		Help: "Messages routed to a dead-letter/error subject (never silently dropped)",
	}, []string{"reason"})

	Goroutines = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "adatrack_goroutines",
		Help: "Number of goroutines currently running (leak detection, FR-4.4)",
	}, func() float64 { return float64(runtime.NumGoroutine()) })

	MemoryAllocated = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "adatrack_memory_allocated_bytes",
		Help: "Bytes allocated and still in use",
	}, func() float64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.HeapInuse)
	})

	reg.MustRegister(
		HTTPRequestsTotal, HTTPRequestDuration,
		NATSMessagesPublished, NATSMessagesConsumed, NATPendingMessages,
		RedisOperations, RedisOperationDuration,
		PGInsertDuration, PGInsertErrors, PGPoolInUse, PGPoolOpen,
		DeadLetterTotal,
		Goroutines, MemoryAllocated,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// GetRegistry returns an isolated Prometheus registry preloaded with the shared
// collectors + Go/process runtime metrics (PRD §10.1 "go_*" collectors).
func GetRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	RegisterSharedMetrics(reg)
	return reg
}
