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
	// --- B8 downlink commands (PRD §21.2 row 1) ------------------------------
	commandsDispatched = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "device_commands_dispatched_total",
		Help: "Downlink commands by final dispatch outcome (sent/offline/failed/acked/timeout)",
	}, []string{"status"})
	commandsAcked = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "device_commands_acked_total",
		Help: "Downlink commands answered by the device, by device result (ok/error)",
	}, []string{"result"})
	commandsPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "device_commands_pending",
		Help: "Downlink commands awaiting a device reply",
	})
	devicesOnline = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "devices_online",
		Help: "Live device connections tracked by the downlink registry",
	})
	// unsupportedFrames counts frames a decoder recognised but cannot decode yet
	// (documented gaps such as the TK103 command matrix or Castel/Navigil
	// position payloads) — visible instead of silently dropped.
	unsupportedFrames = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_unsupported_frames_total",
		Help: "Frames recognised but not decodable yet, per protocol (B9 gap tracker)",
	}, []string{"protocol"})
	// unmappedDevices counts devices whose protocol identity cannot be resolved to
	// the IMEI allowlist (e.g. Navigil device ids without NAVIGIL_DEVICE_MAP) — the
	// explicit "still needs onboarding" signal instead of a silent drop.
	unmappedDevices = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_unmapped_devices_total",
		Help: "Frames from a device whose protocol identity has no IMEI mapping",
	}, []string{"protocol"})

	// castelCRCErrors counts inbound Castel frames whose CRC-16/CCITT-FALSE does not
	// match, i.e. evidence that a firmware variant uses another checksum rule.
	castelCRCErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ingestion_castel_crc_errors_total",
		Help: "Castel frames received with a mismatching CRC-16/CCITT-FALSE",
	})
)

// RegisterMetrics registers the ingestion collectors on a service registry.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(tcpConnectionsActive, tcpConnectionsTotal, tcpParseErrors, framesTotal,
		messagesPublished, rejectedTotal, natsPublishErrors, natsPublishDuration,
		backpressureDrops, backpressureWarnings, fuelReadingsTotal,
		commandsDispatched, commandsAcked, commandsPending, devicesOnline,
		unsupportedFrames, unmappedDevices, castelCRCErrors)
}
