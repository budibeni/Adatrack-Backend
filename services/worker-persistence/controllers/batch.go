package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/worker-persistence/models"
)

// handleMessage decodes a payload and appends it to the batch buffer.
func (p *Persister) handleMessage(msg *nats.Msg) error {
	var t models.TelemetryMessage
	if err := json.Unmarshal(msg.Data, &t); err != nil {
		slog.Error("persistence: invalid telemetry payload", "subject", msg.Subject, "error", err)
		deadLettered.Inc()
		p.publishError("", []byte("decode:"+err.Error()))
		return nil
	}
	if t.IMEI == "" || t.CompanyCode == "" {
		// Without tenant context the row cannot be routed: dead-letter it so the
		// loss is visible (never a silent drop, FR-4.3).
		slog.Warn("persistence: message without tenant context", "subject", msg.Subject)
		deadLettered.Inc()
		p.publishError(t.IMEI, []byte("tenant:missing"))
		return nil
	}
	if t.HasFuel() {
		// B5a FR-7.4: every fuel-bearing packet (with or without position) is
		// persisted to td_fuel_logs. Positioned rows keep flowing to
		// th_telemetry_logs as well (below); fuel-only rows stop here.
		p.mu.Lock()
		p.fuelPending = append(p.fuelPending, models.ToFuelRow(t))
		fuelSize := len(p.fuelPending)
		p.mu.Unlock()
		fuelRowsPending.Set(float64(fuelSize))
		if fuelSize >= p.cfg.Persistence.BatchSize {
			p.poke()
		}
	}
	if models.Positionless(t) {
		// Heartbeat/fuel-only packets carry no position: they belong to the live
		// state and td_fuel_logs — not to th_telemetry_logs (FR-3.4).
		if !t.HasFuel() {
			positionlessRows.Inc()
		}
		return nil
	}

	row := models.ToRow(t)
	p.mu.Lock()
	p.pending = append(p.pending, row)
	size := len(p.pending)
	pendingRows.Set(float64(size))
	p.mu.Unlock()

	if size >= p.cfg.Persistence.BatchSize {
		p.poke()
	}
	return nil
}

// flush snapshots both buffers and persists them per company.
func (p *Persister) flush() {
	p.mu.Lock()
	if len(p.pending) == 0 && len(p.fuelPending) == 0 {
		p.mu.Unlock()
		return
	}
	rows := p.pending
	p.pending = make([]models.Row, 0, p.cfg.Persistence.BatchSize)
	pendingRows.Set(0)
	fuel := p.fuelPending
	p.fuelPending = nil
	fuelRowsPending.Set(0)
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.persist(companyGroups(rows))
		if len(fuel) > 0 {
			p.persistFuel(fuelGroups(fuel))
		}
	}()
}

// fuelGroups groups fuel rows by company code (one INSERT per tenant schema).
func fuelGroups(rows []models.FuelRow) map[string][]models.FuelRow {
	groups := make(map[string][]models.FuelRow)
	for _, r := range rows {
		groups[r.CompanyCode] = append(groups[r.CompanyCode], r)
	}
	return groups
}

// persistFuel writes every fuel company group with retry + backoff (FR-3.4).
func (p *Persister) persistFuel(groups map[string][]models.FuelRow) {
	for company, rows := range groups {
		p.persistFuelCompany(company, rows)
	}
}

// persistFuelCompany inserts one td_fuel_logs batch into the tenant schema.
func (p *Persister) persistFuelCompany(company string, rows []models.FuelRow) {
	routingStart := time.Now()
	pool, err := p.resolveCompanyDB(company)
	tenantRoutingDuration.Observe(float64(time.Since(routingStart).Microseconds()) / 1000.0)
	if err != nil {
		slog.Error("persistence: fuel tenant routing failed; rows dead-lettered",
			"company", company, "rows", len(rows), "error", err)
		batchInsertErrors.WithLabelValues(company).Inc()
		for _, r := range rows {
			deadLettered.Inc()
			p.publishError(r.IMEI, []byte("tenant:routing"))
		}
		return
	}

	values := make([][]any, len(rows))
	for i, r := range rows {
		values[i] = r.Values()
	}

	backoff := p.cfg.Persistence.Backoff
	maxRetries := p.cfg.Persistence.RetryMax
	if maxRetries <= 0 {
		maxRetries = 3
	}

	err = internal.RetryWithBackoff(p.ctx, backoff, maxRetries, func(ctx context.Context) error {
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, ierr := internal.BatchInsert(attemptCtx, pool, models.FuelTableName, models.FuelInsertColumns, values)
		if ierr != nil && internal.IsTransientError(ierr) {
			retryAttempts.Inc()
			slog.Warn("persistence: transient fuel insert failure, retrying",
				"company", company, "rows", len(rows), "error", ierr)
		}
		return ierr
	})
	if err == nil {
		batchInsertSize.Observe(float64(len(rows)))
		messagesProcessed.WithLabelValues(company).Add(float64(len(rows)))
		slog.Debug("persistence: fuel batch inserted", "company", company, "rows", len(rows))
		return
	}

	batchInsertErrors.WithLabelValues(company).Inc()
	slog.Error("persistence: fuel batch insert failed after retries",
		"company", company, "rows", len(rows), "error", err)
	for _, r := range rows {
		deadLettered.Inc()
		p.publishError(r.IMEI, []byte("fuel_batch:fail"))
	}
}

// persist writes every company group with retry + backoff (FR-3.4 step 5) and
// dead-letters the rows of a group whose retries are exhausted (step 6).
func (p *Persister) persist(groups map[string][]models.Row) {
	for company, rows := range groups {
		p.persistCompany(company, rows)
	}
}

// persistCompany resolves the tenant pool and inserts one batch.
func (p *Persister) persistCompany(company string, rows []models.Row) {
	routingStart := time.Now()
	pool, err := p.resolveCompanyDB(company)
	tenantRoutingDuration.Observe(float64(time.Since(routingStart).Microseconds()) / 1000.0)
	if err != nil {
		// Unknown tenant: the rows can never be written — dead-letter them with a
		// clear reason instead of retrying forever.
		slog.Error("persistence: tenant routing failed; rows dead-lettered",
			"company", company, "rows", len(rows), "error", err)
		batchInsertErrors.WithLabelValues(company).Inc()
		for _, r := range rows {
			deadLettered.Inc()
			p.publishError(r.IMEI, []byte("tenant:routing"))
		}
		return
	}

	values := make([][]any, len(rows))
	for i, r := range rows {
		values[i] = r.Values()
	}

	backoff := p.cfg.Persistence.Backoff
	maxRetries := p.cfg.Persistence.RetryMax
	if maxRetries <= 0 {
		maxRetries = 3
	}

	var lastErr error
	err = internal.RetryWithBackoff(p.ctx, backoff, maxRetries, func(ctx context.Context) error {
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, ierr := internal.BatchInsert(attemptCtx, pool, models.TableName, models.InsertColumns, values)
		if ierr != nil && internal.IsTransientError(ierr) {
			retryAttempts.Inc()
			slog.Warn("persistence: transient insert failure, retrying",
				"company", company, "rows", len(rows), "error", ierr)
		}
		return ierr
	})
	if err == nil {
		batchInsertSize.Observe(float64(len(rows)))
		messagesProcessed.WithLabelValues(company).Add(float64(len(rows)))
		slog.Debug("persistence: batch inserted", "company", company, "rows", len(rows))
		return
	}
	lastErr = err

	batchInsertErrors.WithLabelValues(company).Inc()
	slog.Error("persistence: batch insert failed after retries",
		"company", company, "rows", len(rows), "error", lastErr)

	// FR-3.4 step 6: publish every failed row to telemetry.error.<IMEI>.
	payload := []byte("batch:fail")
	for _, r := range rows {
		deadLettered.Inc()
		p.publishError(r.IMEI, payload)
	}
}

// defaultResolveCompanyDB routes a company code to its pre-warmed pool.
func (p *Persister) defaultResolveCompanyDB(companyCode string) (*internal.DBPool, error) {
	if p.tenants == nil {
		return nil, errors.New("tenant manager not configured")
	}
	return p.tenants.DB(companyCode)
}

// defaultPublishError publishes a failed row to `telemetry.error.<IMEI>`.
func (p *Persister) defaultPublishError(imei string, payload []byte) {
	if p.nats == nil {
		return
	}
	subject := p.nats.Subject("error", orUnknown(imei))
	if err := p.nats.Publish(subject, payload); err != nil {
		slog.Error("persistence: failed to publish error message", "subject", subject, "error", err)
	}
}

// orUnknown substitutes a subject-safe IMEI when the message had none.
func orUnknown(imei string) string {
	if imei == "" {
		return "unknown"
	}
	return imei
}
