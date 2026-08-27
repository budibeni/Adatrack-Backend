package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
)

// alertMetrics bundles the PRD §8.1-style counters/histograms for B3.
type alertMetrics struct {
	created    *prometheus.CounterVec // alerts_created_total{company,type,severity}
	errors     *prometheus.CounterVec // alerts_errors_total{company,op}
	escalated  *prometheus.CounterVec // sos_escalations_total{company}
	tta        prometheus.Histogram   // sos_time_to_acknowledge_seconds
	notifySent *prometheus.CounterVec // notifications_sent_total{company,channel,status}
	published  *prometheus.CounterVec // nats_alerts_published_total{subject}
	fuelAccSup *prometheus.CounterVec // alerts_fuel_acc_suppressed_total{company}
}

// newAlertMetrics builds and registers the B3 metric family on the given
// registry (the same registry /metrics serves, unlike prometheus defaults).
func newAlertMetrics(reg *prometheus.Registry) *alertMetrics {
	m := &alertMetrics{
		created: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "alerts_created_total",
			Help: "Alert rows inserted per company/type/severity (PRD §8.1 / B3)",
		}, []string{"company", "type", "severity"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "alerts_errors_total",
			Help: "worker-alert processing errors per company/operation",
		}, []string{"company", "op"}),
		escalated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sos_escalations_total",
			Help: "Automatic SOS escalations emitted per company (B3)",
		}, []string{"company"}),
		tta: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sos_time_to_acknowledge_seconds",
			Help:    "Time-to-Acknowledge for SOS alerts (acknowledged_at - created_at)",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s .. ~68min
		}),
		notifySent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifications_sent_total",
			Help: "Notification dispatch results per company/channel/status (B3 notifikasi)",
		}, []string{"company", "channel", "status"}),
		published: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nats_alerts_published_total",
			Help: "NATS alert/notification messages published by worker-alert",
		}, []string{"subject"}),
		fuelAccSup: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "alerts_fuel_acc_suppressed_total",
			Help: "FUEL_DROP suppressed by strict ACC gate (FUEL_DROP_REQUIRE_ACC=true), per company (B5a FR-7.6)",
		}, []string{"company"}),
	}
	if reg != nil {
		reg.MustRegister(m.created, m.errors, m.escalated, m.tta, m.notifySent, m.published, m.fuelAccSup)
	}
	return m
}

// incCreated bumps alerts_created_total.
func (m *alertMetrics) incCreated(company, typ, severity string) {
	if m != nil {
		m.created.WithLabelValues(company, typ, severity).Inc()
	}
}

// incError bumps alerts_errors_total.
func (m *alertMetrics) incError(company, op string) {
	if m != nil {
		m.errors.WithLabelValues(company, op).Inc()
	}
}

// incPublished bumps nats_alerts_published_total.
func (m *alertMetrics) incPublished(subject string) {
	if m != nil {
		m.published.WithLabelValues(subject).Inc()
	}
}

// incFuelACCSuppressed bumps alerts_fuel_acc_suppressed_total.
func (m *alertMetrics) incFuelACCSuppressed(company string) {
	if m != nil {
		m.fuelAccSup.WithLabelValues(company).Inc()
	}
}
