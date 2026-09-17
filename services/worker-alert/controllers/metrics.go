package controllers

import "github.com/prometheus/client_golang/prometheus"

// Metrics (PRD §10.1 worker-alert).
var (
	alertsRaised = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alerts_raised_total",
		Help: "Alerts persisted and published, per type, severity and company",
	}, []string{"type", "severity", "company"})

	alertsDeduped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alerts_deduped_total",
		Help: "Alert triggers suppressed by the dedup window / open-alert guard",
	}, []string{"type", "company"})

	alertsResolved = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alerts_resolved_total",
		Help: "Alerts resolved automatically (e.g. OFFLINE vehicles reporting again)",
	}, []string{"type", "company"})

	notificationsSent = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "notifications_sent_total",
		Help: "Notification deliveries recorded in td_notifications",
	}, []string{"channel", "status"})

	sosEscalations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "alerts_sos_escalations_total",
		Help: "SOS escalation notifications re-sent after SOS_ESCALATION_MINUTES",
	})

	alertPersistErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "alerts_persist_errors_total",
		Help: "Failed th_alerts writes (never silently dropped — logged as well)",
	})

	alertEngineLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "alert_engine_latency_seconds",
		Help:    "End-to-end latency of one telemetry evaluation",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
	})

	fuelReads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fuel_readings_total",
		Help: "Fuel telemetry messages evaluated by the alert engine (B5a)",
	}, []string{"company"})
)

// RegisterMetrics registers the worker-alert collectors.
func RegisterMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	reg.MustRegister(alertsRaised, alertsDeduped, alertsResolved, notificationsSent,
		sosEscalations, alertPersistErrors, alertEngineLatency, fuelReads)
}
