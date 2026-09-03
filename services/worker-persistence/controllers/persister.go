package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/dialect"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-persistence/models"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	mu          sync.Mutex
	pending     []models.TelemetryRow
	pendingFuel []models.TelemetryRow // B5a: fuel-only rows → fuel_logs
	flushCh     chan struct{}
	natsCli     *internal.NATSClient
	// tenantMgr resolves company_code → company DB pool (worker-persistence
	// task #6 / PRD §6.2 dynamic DB routing).
	tenantMgr *tenant.Manager
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	// publishErrFn is the indirection used by handlers so unit tests can
	// capture telemetry.error publications without a live NATS connection.
	publishErrFn = publishError
	// companyDBFn is the indirection used by insert paths so unit tests can
	// stub the per-company DB pool (sqlmock) without a live tenant manager.
	companyDBFn = resolveCompanyDB
)

// --- Metrics (PRD §8.1 worker-persistence) ---
var (
	messagesProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "messages_processed_total",
		Help: "Telemetry messages processed per company",
	}, []string{"company_code"})
	batchInsertSize = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "batch_insert_size",
		Help:    "Records per batch insert",
		Buckets: prometheus.ExponentialBuckets(10, 2, 8), // 10..2560
	})
	batchInsertErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "batch_insert_errors_total",
		Help: "Failed batch inserts per company",
	}, []string{"company_code"})
	retryAttempts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "retry_attempts_total",
		Help: "Insert retry attempts",
	})
	tenantRoutingDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "tenant_routing_duration_ms",
		Help:    "company_code → DB pool resolution latency (ms)",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
	})
	fuelRowsPositionless = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fuel_rows_positionless_total",
		Help: "B5a fuel-only rows routed to fuel_logs (no telemetry_logs entry, no silent drop)",
	})
)

// RegisterMetrics registers worker-persistence specific collectors.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(messagesProcessed, batchInsertSize, batchInsertErrors, retryAttempts, tenantRoutingDuration, fuelRowsPositionless)
}

// Configure binds the TenantManager + NATS clients (called once by main).
func Configure(mgr *tenant.Manager, nats *internal.NATSClient) {
	tenantMgr = mgr
	natsCli = nats
	ctx, cancel = context.WithCancel(context.Background())
	flushCh = make(chan struct{}, 1)
}

// Start subscribes to telemetry.raw.> (queue group "persistence") and starts
// the periodic flush loop. Returns the NATS subscription for shutdown.
func Start() (*nats.Subscription, error) {
	go flusher()
	return natsCli.Subscribe(natsCli.Subject("raw", ">"), "persistence", handleMsg)
}

// Stop performs a final drain of any buffered rows and waits for batches to settle.
func Stop() {
	if cancel != nil {
		cancel()
	}
	mu.Lock()
	snapshot := pending
	pending = nil
	snapshotFuel := pendingFuel
	pendingFuel = nil
	mu.Unlock()
	if len(snapshot) > 0 {
		insertBatch(snapshot)
	}
	if len(snapshotFuel) > 0 {
		insertFuelBatch(snapshotFuel)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("all batches settled")
	case <-time.After(models.BatchWait):
		slog.Warn("timed out waiting for batches to settle")
	}
}

// handleMsg buffers an incoming telemetry message.
func handleMsg(msg *nats.Msg) error {
	var t models.TelemetryMessage
	if err := json.Unmarshal(msg.Data, &t); err != nil {
		slog.Error("failed to unmarshal telemetry", "error", err, "subject", msg.Subject)
		publishErrFn("", msg.Data)
		return nil
	}
	if t.Timestamp <= 0 {
		t.Timestamp = time.Now().Unix()
	}
	row := models.TelemetryRow{
		IMEI:        t.IMEI,
		CompanyCode: t.CompanyCode,
		VehicleID:   t.VehicleID,
		EventTS:     time.Unix(t.Timestamp, 0),
		Lat:         t.Lat,
		Lon:         t.Lon,
		Speed:       t.Speed,
		Heading:     t.Heading,
		Satellites:  t.Satellites,
		HDOP:        t.HDOP,
		Battery:     t.Battery,
		ACC:         t.ACC,
		FuelLevel:   t.FuelLevel,
		FuelVolume:  t.FuelVolume,
		FuelTempC:   t.FuelTempC,
	}
	if row.CompanyCode == "" {
		row.CompanyCode = "default" // fallback sebelum company terdaftar (PRD §6)
	}
	// B5a: fuel-only message (fuel reading present, no GPS fix) → fuel_logs.
	// Position rows keep flowing to telemetry_logs even when they carry a
	// merged fuel field (live merge path), so the flag is explicit here.
	if row.FuelLevel != nil && row.Lat == 0 && row.Lon == 0 && row.Speed == 0 {
		row.IsFuelOnly = true
	}

	mu.Lock()
	if row.IsFuelOnly {
		pendingFuel = append(pendingFuel, row)
	} else {
		pending = append(pending, row)
		// B5a dual-write: frame berposisi YANG membawa fuel (mis. Teltonika
		// AVL IO 86) juga masuk fuel_logs — telemetry_logs tidak punya kolom
		// fuel, tanpa ini pembacaan BBM protokol ber-GPS hilang dari history.
		if row.FuelLevel != nil {
			pendingFuel = append(pendingFuel, row)
		}
	}
	full := len(pending) >= models.MaxBatchSize || len(pendingFuel) >= models.MaxBatchSize
	mu.Unlock()

	if full {
		select {
		case flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// flusher drains the pending buffer on a fixed interval (batch of 500 OR
// FlushInterval, whichever happens first). Unlike a poke-only periodicFlush,
// this goroutine also consumes the flushCh poke so a full batch OR the timer
// actually reaches insertBatch at runtime (B4 load-test fix: telemetry_logs
// must be written continuously, not only at graceful shutdown).
func flusher() {
	tick := time.NewTicker(models.FlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-flushCh: // batch became full (>= MaxBatchSize)
			drainPending()
		case <-tick.C:
			mu.Lock()
			nonEmpty := len(pending) > 0 || len(pendingFuel) > 0
			mu.Unlock()
			if nonEmpty {
				drainPending()
			}
		case <-ctx.Done():
			return
		}
	}
}

// drainPending snapshots both buffers and hands them to the insert paths.
func drainPending() {
	mu.Lock()
	if len(pending) == 0 && len(pendingFuel) == 0 {
		mu.Unlock()
		return
	}
	rows := pending
	pending = []models.TelemetryRow{}
	fuelRows := pendingFuel
	pendingFuel = []models.TelemetryRow{}
	mu.Unlock()
	insertBatch(rows)
	insertFuelBatch(fuelRows)
}

// insertBatch persists rows into the company telemetry_logs table with retry +
// error publishing. Rows are grouped per company_code and each group is inserted
// into its own company DB (dynamic tenant routing, PRD §6.2).
func insertBatch(rows []models.TelemetryRow) {
	if len(rows) == 0 {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		groups := GroupByCompany(rows)
		for code, group := range groups {
			insertCompanyBatch(code, group)
		}
	}()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// insertFuelBatch (B5a) persists fuel-only rows into company fuel_logs with
// retry + error publishing — same tenant routing pattern as insertBatch.
func insertFuelBatch(rows []models.TelemetryRow) {
	if len(rows) == 0 {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		groups := GroupByCompany(rows)
		for code, group := range groups {
			insertFuelCompanyBatch(code, group)
		}
	}()
}

// insertFuelCompanyBatch inserts a group of fuel-only rows into
// adatrack_gps_{code}.fuel_logs. Same retry/backoff/error-publish semantics as
// insertCompanyBatch. Positionless readings never enter telemetry_logs; the
// fuel_rows_positionless_total counter keeps the path observable (no silent drop).
func insertFuelCompanyBatch(companyCode string, rows []models.TelemetryRow) {
	start := time.Now()
	db, err := companyDBFn(companyCode)
	tenantRoutingDuration.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	if err != nil {
		batchInsertErrors.WithLabelValues(companyCode).Inc()
		slog.Error("tenant routing failed for fuel batch; rows moved to telemetry.error",
			"company", companyCode, "rows", len(rows), "error", err)
		for _, r := range rows {
			publishErrFn(r.IMEI, []byte("tenant:routing:fuel"))
		}
		return
	}

	columns := []string{
		"vehicle_id", "imei", "company_code", "fuel_level", "fuel_volume", "fuel_temp_c", "timestamp",
	}
	values := make([][]interface{}, len(rows))
	for i, r := range rows {
		values[i] = []interface{}{
			r.VehicleID,
			r.IMEI,
			strings.ToLower(companyCode),
			r.FuelLevel,  // *float64 → NULL bila nil
			r.FuelVolume, // *float64 → NULL bila nil
			r.FuelTempC,  // *float64 → NULL bila nil
			r.EventTS,    // DATETIME (parseTime=true)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= models.MaxRetries; attempt++ {
		_, lastErr = internal.BatchInsertDB(db, dialect.Current(), "fuel_logs", columns, values,
			internal.MySQLInsertDuration, internal.MySQLInsertErrors)
		if lastErr == nil {
			fuelRowsPositionless.Add(float64(len(rows)))
			messagesProcessed.WithLabelValues(companyCode).Add(float64(len(rows)))
			slog.Info("fuel batch inserted", "rows", len(rows), "company", companyCode)
			return
		}
		if attempt < models.MaxRetries && isTransientError(lastErr) {
			retryAttempts.Inc()
			delay := models.BaseDelay * time.Duration(1<<uint(attempt))
			slog.Warn("fuel batch insert failed, retrying", "company", companyCode,
				"attempt", attempt+1, "delay", delay, "error", lastErr)
			time.Sleep(delay)
			continue
		}
		break
	}

	// Retries exhausted -> publish each failed record to telemetry.error.<IMEI>.
	batchInsertErrors.WithLabelValues(companyCode).Inc()
	slog.Error("fuel batch insert failed after retries", "company", companyCode,
		"rows", len(rows), "error", lastErr)
	payload := []byte("batch:fail:fuel")
	for _, r := range rows {
		publishErrFn(r.IMEI, payload)
	}
}

// GroupByCompany buckets rows by CompanyCode (case-insensitive), preserving
// insertion order. Empty codes fall back to "default" (PRD §6 fallback DB).
func GroupByCompany(rows []models.TelemetryRow) map[string][]models.TelemetryRow {
	out := make(map[string][]models.TelemetryRow)
	for _, r := range rows {
		code := strings.ToUpper(strings.TrimSpace(r.CompanyCode))
		if code == "" {
			code = "default"
		}
		out[code] = append(out[code], r)
	}
	return out
}

// insertCompanyBatch inserts a group of rows into adatrack_gps_{code}.telemetry_logs.
// Uses retry + exponential backoff; after retries are exhausted every failed row
// is published to telemetry.error.<IMEI>.
func insertCompanyBatch(companyCode string, rows []models.TelemetryRow) {
	start := time.Now()
	db, err := companyDBFn(companyCode)
	tenantRoutingDuration.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	if err != nil {
		batchInsertErrors.WithLabelValues(companyCode).Inc()
		slog.Error("tenant routing failed; rows moved to telemetry.error", "company", companyCode, "rows", len(rows), "error", err)
		for _, r := range rows {
			publishErrFn(r.IMEI, []byte("tenant:routing"))
		}
		return
	}

	columns := []string{
		"vehicle_id", "imei", "company_code", "latitude", "longitude",
		"speed", "heading", "altitude", "acc_status", "battery_level", "timestamp",
	}
	values := make([][]interface{}, len(rows))
	for i, r := range rows {
		values[i] = []interface{}{
			r.VehicleID,
			r.IMEI,
			strings.ToLower(companyCode),
			r.Lat,
			r.Lon,
			r.Speed,
			r.Heading,
			0, // altitude (tidak diparse di protokol awal)
			boolToInt(r.ACC), // acc_status
			r.Battery,
			r.EventTS, // DATETIME (parseTime=true)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= models.MaxRetries; attempt++ {
		_, lastErr = internal.BatchInsertDB(db, dialect.Current(), "telemetry_logs", columns, values,
			internal.MySQLInsertDuration, internal.MySQLInsertErrors)
		if lastErr == nil {
			batchInsertSize.Observe(float64(len(rows)))
			messagesProcessed.WithLabelValues(companyCode).Add(float64(len(rows)))
			slog.Info("batch inserted", "rows", len(rows), "company", companyCode)
			return
		}
		if attempt < models.MaxRetries && isTransientError(lastErr) {
			retryAttempts.Inc()
			delay := models.BaseDelay * time.Duration(1<<uint(attempt))
			slog.Warn("batch insert failed, retrying", "company", companyCode,
				"attempt", attempt+1, "delay", delay, "error", lastErr)
			time.Sleep(delay)
			continue
		}
		break
	}

	// Retries exhausted -> publish each failed record to telemetry.error.<IMEI>.
	batchInsertErrors.WithLabelValues(companyCode).Inc()
	slog.Error("batch insert failed after retries", "company", companyCode,
		"rows", len(rows), "error", lastErr)
	payload := []byte("batch:fail")
	for _, r := range rows {
		publishErrFn(r.IMEI, payload)
	}
}

// resolveCompanyDB resolves a company_code to its pre-warmed pool. Unknown
// companies (no master.companies row / inactive) fail fast. A nil manager
// (unit tests / misconfiguration) also fails fast — rows then flow to
// telemetry.error instead of panicking the worker.
func resolveCompanyDB(companyCode string) (*sql.DB, error) {
	if tenantMgr == nil {
		return nil, errors.New("tenant manager not configured")
	}
	return tenantMgr.DB(companyCode)
}

// publishError publishes a message to telemetry.error.<IMEI>.
func publishError(imei string, payload []byte) {
	last := "unknown"
	if imei != "" {
		last = imei
	}
	subject := natsCli.Subject("error", last)
	if err := natsCli.Publish(subject, payload); err != nil {
		slog.Error("failed to publish error message", "error", err, "subject", subject)
	}
}

// IsTransientError mirrors shared transient-error detection for local retries.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	e := strings.ToLower(err.Error())
	patterns := []string{
		"connection refused", "connection reset", "timeout",
		"too many connections", "lock wait timeout", "deadlock",
		"server has gone away",
	}
	for _, p := range patterns {
		if strings.Contains(e, p) {
			return true
		}
	}
	return false
}

// isTransientError is a package-local alias kept for compatibility with tests.
func isTransientError(err error) bool {
	return IsTransientError(err)
}
