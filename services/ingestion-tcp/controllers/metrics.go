package controllers

import "github.com/prometheus/client_golang/prometheus"

// Ingestion metrics (PRD §10.1): tcp_connections_active/total,
// tcp_parse_errors_total, tenant_resolution_duration_ms (internal/tenant),
// tenant_lookup_errors_total (internal/tenant), nats_publish_duration_ms,
// nats_publish_errors_total, backpressure_drops_total, fuel_readings_total.
var (
	tcpConnectionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tcp_connections_active",
		Help: "Active device TCP connections",
	})
	tcpConnectionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tcp_connections_total",
		Help: "Total accepted device TCP connections since startup",
	})
	tcpParseErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tcp_parse_errors_total",
		Help: "Frame/payload parse failures per protocol",
	}, []string{"protocol"})
	framesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_frames_total",
		Help: "Protocol frames received per protocol and kind",
	}, []string{"protocol", "kind"})
	messagesPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_messages_published_total",
		Help: "Telemetry messages published to NATS per protocol",
	}, []string{"protocol"})
	rejectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_rejected_total",
		Help: "Rejected frames (unauthorised IMEI / parse error / no auth / max conn)",
	}, []string{"reason"})
	natsPublishErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nats_publish_errors_total",
		Help: "NATS publish failures per company",
	}, []string{"company_code"})
	natsPublishDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nats_publish_duration_ms",
		Help:    "NATS publish latency in milliseconds",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 12),
	}, []string{"subject"})
	backpressureDrops = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "backpressure_drops_total",
		Help: "Messages dropped because the stream is >90% full (FR-1.5)",
	}, []string{"stream"})
	backpressureWarnings = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "backpressure_warnings_total",
		Help: "Messages processed while the stream is >50% full (FR-1.5)",
	})
	fuelReadingsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fuel_readings_total",
		Help: "Fuel sensor readings decoded per protocol",
	}, []string{"protocol"})
)

// RegisterMetrics registers the ingestion collectors on a service registry.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(tcpConnectionsActive, tcpConnectionsTotal, tcpParseErrors, framesTotal,
		messagesPublished, rejectedTotal, natsPublishErrors, natsPublishDuration,
		backpressureDrops, backpressureWarnings, fuelReadingsTotal)
}
