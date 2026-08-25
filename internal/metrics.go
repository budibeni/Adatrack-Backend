package internal

import "github.com/prometheus/client_golang/prometheus"

// Package-level metric variables. Initialized in RegisterMetrics().
var (
	// HTTP request metrics
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	// WebSocket metrics
	WSMessagesTotal    *prometheus.CounterVec
	WSConnectionActive *prometheus.GaugeVec
	WSMessageDuration  *prometheus.HistogramVec

	// NATS metrics
	NATSMessagesPublished *prometheus.CounterVec
	NATSMessagesConsumed  *prometheus.CounterVec
	NATPendingMessages    *prometheus.GaugeVec

	// MySQL metrics
	MySQLInsertDuration *prometheus.HistogramVec
	MySQLInsertErrors   *prometheus.CounterVec

	// Redis metrics
	RedisOperations        *prometheus.CounterVec
	RedisOperationDuration *prometheus.HistogramVec

	// RBAC metrics (service-websocket, PRD §8.1)
	RBACCheckDuration *prometheus.HistogramVec
	RBACDenialsTotal  *prometheus.CounterVec

	// Goroutine and memory metrics
	Goroutines      prometheus.Gauge
	MemoryAllocated prometheus.Gauge
)

// RegisterMetrics initializes all Prometheus metrics and registers them
// with the given registry. Must be called before metrics are used.
func RegisterMetrics(registry *prometheus.Registry) error {
	// HTTP metrics
	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests received",
	}, []string{"method", "endpoint", "status"})

	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~5s
	}, []string{"method", "endpoint"})

	// WebSocket metrics
	WSMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ws_messages_total",
		Help: "Total number of WebSocket messages sent",
	}, []string{"topic", "direction"})

	WSConnectionActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ws_connections_active",
		Help: "Number of active WebSocket connections",
	}, []string{"topic"})

	WSMessageDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ws_message_duration_seconds",
		Help:    "WebSocket message processing duration in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 10),
	}, []string{"topic"})

	// NATS metrics
	NATSMessagesPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nats_messages_published_total",
		Help: "Total number of NATS messages published",
	}, []string{"subject"})

	NATSMessagesConsumed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nats_messages_consumed_total",
		Help: "Total number of NATS messages consumed",
	}, []string{"subject", "queue_group"})

	NATPendingMessages = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nats_pending_messages",
		Help: "Number of pending NATS messages",
	}, []string{"subject"})

	// MySQL metrics
	MySQLInsertDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mysql_insert_duration_seconds",
		Help:    "MySQL insert operation duration in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 10),
	}, []string{"table"})

	MySQLInsertErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mysql_insert_errors_total",
		Help: "Total number of MySQL insert errors",
	}, []string{"table"})

	// Redis metrics
	RedisOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "redis_operations_total",
		Help: "Total number of Redis operations",
	}, []string{"command", "status"})

	RedisOperationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "redis_operation_duration_seconds",
		Help:    "Redis operation duration in seconds",
		Buckets: prometheus.ExponentialBuckets(0.0005, 2, 12),
	}, []string{"command"})

	// RBAC metrics (PRD §8.1: rbac_check_duration_ms)
	RBACCheckDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "rbac_check_duration_seconds",
		Help:    "Duration of RBAC permission checks in seconds",
		Buckets: prometheus.ExponentialBuckets(0.0001, 2, 10),
	}, []string{"action"})

	RBACDenialsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rbac_denials_total",
		Help: "Total number of authorization denials",
	}, []string{"action", "reason"})

	// Goroutine and memory metrics
	// NOTE: nama memakai prefix adatrack_* agar tidak bentrok dengan metrik
	// standar Go runtime collector (go_goroutines, go_memory_allocated_bytes)
	// yang diregistrasi setiap service via prometheus.NewGoCollector().
	Goroutines = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "adatrack_goroutines",
		Help: "Number of goroutines currently running",
	})

	MemoryAllocated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "adatrack_memory_allocated_bytes",
		Help: "Number of bytes allocated and still in use",
	})

	// Register all metrics
	registry.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		WSMessagesTotal,
		WSConnectionActive,
		WSMessageDuration,
		NATSMessagesPublished,
		NATSMessagesConsumed,
		NATPendingMessages,
		MySQLInsertDuration,
		MySQLInsertErrors,
		RedisOperations,
		RedisOperationDuration,
		RBACCheckDuration,
		RBACDenialsTotal,
		Goroutines,
		MemoryAllocated,
	)

	return nil
}

// GetRegistry returns a new Prometheus registry with all adatrack metrics registered.
func GetRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)
	return registry
}
