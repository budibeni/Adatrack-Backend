package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ingestion_tcp_active_connections",
		Help: "Current number of active TCP connections",
	}, []string{"protocol"})

	TotalConnections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_tcp_total_connections",
		Help: "Total number of accepted TCP connections",
	}, []string{"protocol"})

	BytesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_tcp_bytes_received_total",
		Help: "Total bytes received over TCP",
	}, []string{"protocol"})

	ConnectionErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_tcp_connection_errors_total",
		Help: "Total connection errors or rate limits hit",
	}, []string{"protocol", "type"})
)
