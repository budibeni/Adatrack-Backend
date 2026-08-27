package controllers

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
)

// RetentionRunner runs the FR-8.7 daily retention job: delete object-store
// objects older than per-company retention_days and mark media_events
// status='expired'. Execution follows MEDIA_CLEANUP_CRON (default "0 3 * * *").
type RetentionRunner struct {
	cfg    *internal.Config
	tm     *tenant.Manager
	store  storageStore
	hour   int
	minute int
}

// storageStore is the minimal store surface the runner needs (nil-safe for tests).
type storageStore interface {
	Delete(ctx context.Context, bucket, key string) error
}

// NewRetentionRunner builds the runner, parsing the cron hour/minute.
func NewRetentionRunner(cfg *internal.Config, tm *tenant.Manager, store storageStore) *RetentionRunner {
	h, m := parseDailyCron(cfg.Media.CleanupCron)
	return &RetentionRunner{cfg: cfg, tm: tm, store: store, hour: h, minute: m}
}

// parseDailyCron extracts (hour, minute) from a "M H * * *" cron spec.
func parseDailyCron(spec string) (int, int) {
	fields := strings.Fields(strings.TrimSpace(spec))
	if len(fields) < 2 {
		return 3, 0
	}
	h, errH := strconv.Atoi(fields[1])
	m, errM := strconv.Atoi(fields[0])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 3, 0
	}
	return h, m
}

// Start launches the background loop until ctx is cancelled.
func (r *RetentionRunner) Start(ctx context.Context) {
	go func() {
		slog.Info("media retention runner started", "cron_hour", r.hour, "minute", r.minute)
		for {
			now := time.Now()
			next := nextOClock(now, r.hour, r.minute)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
				r.runOnce(ctx)
			}
		}
	}()
}

// nextOClock returns the next occurrence of hh:mm (today if still pending).
func nextOClock(now time.Time, h, m int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// runOnce deletes expired objects for every company and marks them expired.
func (r *RetentionRunner) runOnce(ctx context.Context) {
	if r.tm == nil {
		return
	}
	codes := listCompanyCodes(ctx, r.tm)
	for _, code := range codes {
		r.cleanupCompany(ctx, strings.ToLower(code))
	}
}

// cleanupCompany prunes one company's expired media in batches.
func (r *RetentionRunner) cleanupCompany(ctx context.Context, company string) {
	db, err := r.tm.DB(company)
	if err != nil {
		slog.Warn("media retention: company pool unavailable", "company", company, "error", err)
		return
	}
	st := newCompanyStore(company, db, r.tm)

	cfg, err := CompanyMediaConfig(company)
	retentionDays := 30
	if err == nil && cfg != nil && cfg.RetentionDays > 0 {
		retentionDays = cfg.RetentionDays
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	const batch = 200
	for {
		items, err := st.ExpiredMediaEntries(cutoff, batch)
		if err != nil {
			slog.Error("media retention: query expired failed", "company", company, "error", err)
			return
		}
		if len(items) == 0 {
			return
		}
		for _, it := range items {
			if r.store != nil {
				if derr := r.store.Delete(ctx, it.Bucket, it.ObjectKey); derr != nil {
					slog.Error("media retention: delete object failed", "error", derr,
						"company", company, "key", it.ObjectKey)
					continue
				}
			}
			if serr := st.SetMediaStatus(it.ID, "expired"); serr != nil {
				slog.Error("media retention: mark expired failed", "error", serr,
					"company", company, "media_id", it.ID)
				continue
			}
			mediaCleanupDeletedTotal.Inc()
		}
		if len(items) < batch {
			return
		}
	}
}

// listCompanyCodes returns company codes from master companies (fallback: none).
func listCompanyCodes(ctx context.Context, tm *tenant.Manager) []string {
	db := tm.Master()
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT code FROM companies WHERE is_active = true`)
	if err != nil {
		slog.Error("media retention: list companies failed", "error", err)
		return nil
	}
	defer rows.Close()
	out := make([]string, 0, 8)
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}
