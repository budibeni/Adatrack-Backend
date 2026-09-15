package controllers

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics of service-websocket (PRD §10.1: "service-websocket / api-vehicle").
var (
	wsConnectionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ws_connections_active",
		Help: "WebSocket connections currently open",
	})
	wsConnectionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ws_connections_total",
		Help: "WebSocket connections accepted since start",
	})
	wsConnectionsRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ws_connections_rejected_total",
		Help: "WebSocket handshakes rejected (reason: unauthorized|origin|capacity|upgrade)",
	}, []string{"reason"})
	wsBroadcastDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "ws_broadcast_duration_ms",
		Help:    "Fan-out latency of one live update to all matched subscribers",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 12),
	})
	wsMessageQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ws_message_queue_size",
		Help: "Total pending messages queued across all clients (drop-oldest applies at WS_MAX_QUEUE)",
	})
	wsMessagesSent = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ws_message_sent_total",
		Help: "Messages delivered per event type",
	}, []string{"event"})
	wsMessagesDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ws_message_dropped_total",
		Help: "Messages dropped because a client queue was full (drop-oldest, FR-5.4)",
	}, []string{"event"})
	wsSubscriptions = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ws_subscriptions_active",
		Help: "Vehicle subscriptions currently held by connected clients",
	})

	rbacCheckDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "rbac_check_duration_ms",
		Help:    "RBAC (tenant access + row-level vehicle) check latency in milliseconds",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 12),
	})
	rbacDenied = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rbac_denied_total",
		Help: "Authorization denials per reason (PRD §3.1)",
	}, []string{"reason"})

	httpErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_errors_total",
		Help: "HTTP responses with a 4xx/5xx status per error_code",
	}, []string{"status", "error_code"})

	tenantDBConnectionsActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tenant_db_connections_active",
		Help: "Active PostgreSQL connections per tenant pool",
	}, []string{"company_code"})
	tenantRoutingDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "tenant_routing_duration_ms",
		Help:    "company_code → tenant DB pool resolution latency in milliseconds",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
	})

	auditEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "audit_events_total",
		Help: "Audit rows written per action/outcome (PRD §9.4)",
	}, []string{"action", "outcome"})
	auditWriteErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "audit_write_errors_total",
		Help: "Audit persistence failures (retried, then dead-lettered — no silent drop)",
	})

	loginAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_login_attempts_total",
		Help: "Login attempts per outcome (success|failure|locked|rate_limited)",
	}, []string{"outcome"})
)

// RegisterMetrics registers the service-websocket collectors on the registry.
func RegisterMetrics(reg *prometheus.Registry) {
	if reg == nil {
		return
	}
	reg.MustRegister(wsConnectionsActive, wsConnectionsTotal, wsConnectionsRejected,
		wsBroadcastDuration, wsMessageQueueSize, wsMessagesSent, wsMessagesDropped,
		wsSubscriptions, rbacCheckDuration, rbacDenied, httpErrors,
		tenantDBConnectionsActive, tenantRoutingDuration,
		auditEvents, auditWriteErrors, loginAttempts, liveStateErrors)
}

// observeRBAC records an RBAC check latency in milliseconds.
func observeRBAC(start time.Time) {
	rbacCheckDuration.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
}

// observeBroadcast records a fan-out latency in milliseconds.
func observeBroadcast(start time.Time) {
	wsBroadcastDuration.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
}

// observeTenantRoute records a tenant pool resolution latency in milliseconds.
func observeTenantRoute(start time.Time) {
	tenantRoutingDuration.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
}
