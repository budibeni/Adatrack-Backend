package controllers

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// ---------------------------------------------------------------------------
// B5b media metrics (FR-8.8):
//   media_uploads_total{company_code,media_type}
//   media_upload_bytes_total{company_code}
//   media_presigned_total{company_code}
//   media_cleanup_deleted_total
//   media_ingest_errors_total{company_code,reason}
//   storage_objects{bucket}
// Registered once (sync.Once) on internal.GetRegistry() so /metrics of this
// service exposes them alongside the standard Go/http metrics.
// ---------------------------------------------------------------------------

var (
	mediaMetricsOnce sync.Once

	mediaUploadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_uploads_total",
		Help: "Media objects ingested per company/type",
	}, []string{"company_code", "media_type"})

	mediaUploadBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_upload_bytes_total",
		Help: "Total bytes stored to object storage per company",
	}, []string{"company_code"})

	mediaPresignedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_presigned_total",
		Help: "Presigned URLs issued per company",
	}, []string{"company_code"})

	mediaCleanupDeletedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "media_cleanup_deleted_total",
		Help: "Media objects deleted by the daily retention job",
	})

	mediaIngestErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_ingest_errors_total",
		Help: "Ingest failures per company/reason (no silent drop)",
	}, []string{"company_code", "reason"})

	storageObjectsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_objects",
		Help: "Number of objects currently in each bucket",
	}, []string{"bucket"})
)

// ensureMediaMetrics registers the media metric collectors once per process
// onto the caller's Prometheus registry — the one that backs the /metrics HTTP
// handler (see main.go getRegistry()). internal.GetRegistry() creates a fresh
// registry per call, so media metrics MUST be registered on the same registry
// the handler was built from, otherwise they never appear on /metrics.
func ensureMediaMetrics(reg *prometheus.Registry) {
	if reg == nil {
		return
	}
	mediaMetricsOnce.Do(func() {
		reg.MustRegister(
			mediaUploadsTotal,
			mediaUploadBytesTotal,
			mediaPresignedTotal,
			mediaCleanupDeletedTotal,
			mediaIngestErrorsTotal,
			storageObjectsGauge,
		)
	})
}

// RegisterMediaMetrics is the exported hook for main.go to register the media
// metrics onto the served registry.
func RegisterMediaMetrics(reg *prometheus.Registry) {
	ensureMediaMetrics(reg)
}
