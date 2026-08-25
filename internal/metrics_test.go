package internal

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestGetRegistryReturnsNonNil(t *testing.T) {
	reg := GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry() returned nil")
	}
}

// TestRegistryContainsAllMetricFamilies verifies all expected metric families
// are registered by touching each vector first (GaugeVec/CounterVec children
// only appear in Gather after WithLabelValues is called).
func TestRegistryContainsAllMetricFamilies(t *testing.T) {
	reg := GetRegistry()

	// Touch all vector metrics so their children are created.
	HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Inc()
	HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(0.01)
	WSMessagesTotal.WithLabelValues("vehicle.update", "send").Inc()
	WSConnectionActive.WithLabelValues("global").Set(1)
	WSMessageDuration.WithLabelValues("vehicle_update").Observe(0.01)
	NATSMessagesPublished.WithLabelValues("telemetry.raw.1").Inc()
	NATSMessagesConsumed.WithLabelValues("telemetry.raw.*", "persistence").Inc()
	NATPendingMessages.WithLabelValues("telemetry.raw.1").Set(1)
	MySQLInsertDuration.WithLabelValues("telemetry_logs").Observe(0.01)
	MySQLInsertErrors.WithLabelValues("telemetry_logs").Inc()
	RedisOperations.WithLabelValues("GET", "success").Inc()
	RedisOperationDuration.WithLabelValues("GET").Observe(0.01)
	RBACCheckDuration.WithLabelValues("token_validation").Observe(0.01)
	RBACDenialsTotal.WithLabelValues("vehicle", "no_access").Inc()
	Goroutines.Set(42)
	MemoryAllocated.Set(1024)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	expected := []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"ws_messages_total",
		"ws_connections_active",
		"ws_message_duration_seconds",
		"nats_messages_published_total",
		"nats_messages_consumed_total",
		"nats_pending_messages",
		"mysql_insert_duration_seconds",
		"mysql_insert_errors_total",
		"redis_operations_total",
		"redis_operation_duration_seconds",
		"rbac_check_duration_seconds",
		"rbac_denials_total",
		"adatrack_goroutines",
		"adatrack_memory_allocated_bytes",
	}

	found := make(map[string]bool)
	for _, mf := range mfs {
		found[mf.GetName()] = true
	}

	for _, name := range expected {
		if !found[name] {
			t.Errorf("expected metric %q to be registered", name)
		}
	}
}

func TestRegisterMetricsOnFreshRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	if err := RegisterMetrics(reg); err != nil {
		t.Fatalf("RegisterMetrics failed: %v", err)
	}
	// Gather should succeed on the fresh registry.
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather failed: %v", err)
	}
}

func TestCounterAndHistogramAndGaugeWork(t *testing.T) {
	reg := GetRegistry()
	HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Inc()
	HTTPRequestsTotal.WithLabelValues("POST", "/test", "201").Add(5)
	HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(0.05)
	Goroutines.Set(42)
	MemoryAllocated.Set(1024 * 1024)

	// Gather and verify the http_requests_total metric was recorded.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "http_requests_total" {
			for _, m := range mf.GetMetric() {
				b := strings.Builder{}
				for _, l := range m.GetLabel() {
					b.WriteString(l.GetName() + "=" + l.GetValue() + " ")
				}
				if strings.Contains(b.String(), "GET") || strings.Contains(b.String(), "POST") {
					found = true
				}
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Error("expected HTTP request metrics to be recorded")
	}
}
