package controllers

import (
	"os"
	"testing"

	"ajb_gps/internal"

	"github.com/prometheus/client_golang/prometheus"
)

// TestMain ensures shared Prometheus metrics are registered before tests run.
func TestMain(m *testing.M) {
	_ = internal.RegisterMetrics(prometheus.NewRegistry())
	os.Exit(m.Run())
}
