package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"ajb_gps/internal/dialect"
	"ajb_gps/worker-alert/models"
)

// escalationMonitor periodically escalates OPEN SOS alerts that were not
// acknowledged within SOS_ESCALATION_MINUTES and records Time-to-Acknowledge
// for SOS alerts once they are acknowledged (B3).
func (wa *WorkerAlert) escalationMonitor() {
	threshold := wa.cfg.GetSOSEscalationMinutes()
	if threshold <= 0 {
		threshold = 2 * time.Minute
	}
	maxRounds := wa.cfg.GetSOSEscalationMax()
	if maxRounds <= 0 {
		maxRounds = 3
	}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-wa.ctx.Done():
			return
		case <-tick.C:
			for _, c := range companiesFn(wa.tm) {
				if !c.IsActive {
					continue
				}
				wa.escalateCompany(c.Code, threshold, maxRounds)
				wa.recordTTA(c.Code, threshold)
			}
		}
	}
}

// escalateCompany escalates every still-open SOS older than the ack window.
func (wa *WorkerAlert) escalateCompany(code string, threshold time.Duration, maxRounds int) {
	st, err := newStoreFn(wa, code)
	if err != nil {
		wa.metrics.incError(code, "escalation_pool")
		return
	}
	cutoff := time.Now().Add(-threshold)
	list, err := st.OpenSOSOlderThan(cutoff)
	if err != nil {
		slog.Error("list open SOS failed", "company", code, "error", err)
		wa.metrics.incError(code, "escalation_list")
		return
	}
	for _, entry := range list {
		count, err := wa.incrEscalation(code, entry.ID)
		if err != nil {
			slog.Error("escalation counter failed", "company", code, "alert_id", entry.ID, "error", err)
			wa.metrics.incError(code, "escalation_counter")
			continue
		}
		if count > maxRounds {
			continue // cap tercapai — berhenti meng-eskalasi alert ini
		}
		wa.metrics.escalated.WithLabelValues(code).Inc()
		wa.publishSOSEscalation(code, entry, count, threshold)
		slog.Warn("SOS alert escalated", "company", code, "alert_id", entry.ID,
			"vehicle_id", entry.VehicleID, "escalation", count)
	}
}

// incrEscalation bumps the per-alert counter in Redis (24h TTL) and returns it.
func (wa *WorkerAlert) incrEscalation(company string, alertID uint64) (int, error) {
	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()
	key := wa.sosEscalationKey(company, alertID)
	n, err := wa.redis.Client().Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if n == 1 { // set TTL hanya pada increment pertama
		wa.redis.Client().Expire(ctx, key, 24*time.Hour)
	}
	return int(n), nil
}

// recordTTA observes the Time-to-Acknowledge once per acknowledged SOS alert.
func (wa *WorkerAlert) recordTTA(code string, threshold time.Duration) {
	st, err := newStoreFn(wa, code)
	if err != nil {
		return
	}
	cs, ok := st.(*companyStore)
	if !ok {
		return
	}
	cutoff := time.Now().Add(-threshold)
	// Dialect-aware TTA seconds: MySQL TIMESTAMPDIFF vs PG epoch-diff
	// (PG-parity fix — TIMESTAMPDIFF is MySQL-only, SQLSTATE 42703 di PG).
	secExpr := "TIMESTAMPDIFF(SECOND, created_at, acknowledged_at)"
	if dialect.Current() == dialect.Postgres {
		secExpr = `(EXTRACT(EPOCH FROM (acknowledged_at - created_at)))::bigint`
	}
	rows, err := cs.ro.Query( // READ path (replica); TTA scan berkala
		`SELECT id, `+secExpr+`
		 FROM alerts
		 WHERE alert_type = 'SOS' AND status != 'open'
		   AND acknowledged_at IS NOT NULL AND created_at <= ?`, cutoff)
	if err != nil {
		return
	}
	type ttaRow struct {
		id  uint64
		sec int64
	}
	pending := make([]ttaRow, 0, 4)
	for rows.Next() {
		var r ttaRow
		if err := rows.Scan(&r.id, &r.sec); err == nil && r.sec >= 0 {
			pending = append(pending, r)
		}
	}
	rows.Close()

	for _, r := range pending {
		first, err := wa.markTTASeen(code, r.id)
		if err != nil || !first {
			continue
		}
		wa.metrics.tta.Observe(float64(r.sec))
		slog.Info("SOS acknowledged (TTA recorded)", "company", code, "alert_id", r.id, "tta_seconds", r.sec)
	}
}

// markTTASeen returns true the first time an alert id is recorded in the
// per-company TTA seen-set (Redis set, TTL 24 jam).
func (wa *WorkerAlert) markTTASeen(company string, alertID uint64) (bool, error) {
	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()
	key := wa.redisKeyPrefix() + lowerCode(company) + ":sos_tta_seen"
	added, err := wa.redis.Client().SAdd(ctx, key, alertID).Result()
	if err != nil {
		return false, err
	}
	if added == 1 {
		wa.redis.Client().Expire(ctx, key, 24*time.Hour)
	}
	return added == 1, nil
}

// lowerCode lowercases a company code (Redis key convention).
func lowerCode(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		out[i] = c
	}
	return string(out)
}

// publishSOSEscalation re-emits the alert on alert.sos.<company> with the new
// escalation level so downstream consumers can re-notify.
func (wa *WorkerAlert) publishSOSEscalation(company string, entry models.OpenSOSAlert, level int, threshold time.Duration) {
	if wa.nac == nil {
		return
	}
	imei := wa.imeiForVehicle(company, entry.VehicleID)
	payload := map[string]interface{}{
		"alert_id":     entry.ID,
		"vehicle_id":   entry.VehicleID,
		"imei":         imei,
		"company_code": company,
		"type":         "sos",
		"severity":     "critical",
		"status":       "open",
		"message": fmt.Sprintf("SOS not acknowledged within %s, escalation level %d",
			threshold.Round(time.Second), level),
		"escalation":  level,
		"received_at": time.Now().Unix(),
	}
	subject := wa.cfg.SubjectPlain("alert", "sos", lowerCode(company))
	data, _ := json.Marshal(payload)
	if err := wa.nac.Publish(subject, data); err != nil {
		slog.Error("failed to publish SOS escalation", "subject", subject, "error", err)
		wa.metrics.incError(company, "publish")
		return
	}
	wa.metrics.incPublished(subject)
}

// imeiForVehicle resolves the IMEI of a vehicle row (best effort).
func (wa *WorkerAlert) imeiForVehicle(company string, vehicleID uint64) string {
	st, err := newStoreFn(wa, company)
	if err != nil {
		return ""
	}
	cs, ok := st.(*companyStore)
	if !ok {
		return ""
	}
	var imei string
	_ = cs.ro.QueryRow(`SELECT imei FROM vehicles WHERE id = ?`, vehicleID).Scan(&imei) // READ path (replica)
	return imei
}

var _ = context.Background
