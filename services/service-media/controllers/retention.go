package controllers

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"adatrack_gps/service-media/models"
)

// startRetention launches the periodic sweep (FR-8.7). The scheduler is
// cron-based (MEDIA_CLEANUP_CRON) unless MEDIA_RETENTION_SWEEP_SEC overrides it.
func (s *Service) startRetention(ctx context.Context) {
	spec, err := newRetentionScheduler(s.settings)
	if err != nil {
		slog.Error("service-media: retention scheduler disabled", "error", err)
		return
	}
	s.retention = spec

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		// A boot sweep keeps a restarted service converging without waiting for
		// the next scheduled run (idempotent: only expired rows are touched).
		s.sweepRetention(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if s.retention.due(now.UTC()) {
					s.sweepRetention(ctx)
				}
			}
		}
	}()
}

// sweepRetention expires objects and rows past their retention (FR-8.7): the
// object is deleted first so a failure keeps the row visible (retryable), then
// the catalog row is marked `expired`, then the deletion is audited as
// HARD_DELETE (§6.0.1/§11).
func (s *Service) sweepRetention(ctx context.Context) {
	companies, err := s.store.MediaCompanies(ctx)
	if err != nil {
		mediaCleanupErrors.WithLabelValues("list_companies").Inc()
		slog.Error("service-media: retention sweep could not list companies", "error", err)
		return
	}
	now := time.Now().UTC()
	pendingBefore := now.Add(-time.Duration(maxInt(s.settings.PendingTTLHours, 1)) * time.Hour)

	for _, cfg := range companies {
		if ctx.Err() != nil {
			return
		}
		candidates, cerr := s.store.ExpiredMediaEvents(ctx, cfg.CompanyCode, now, pendingBefore, 500)
		if cerr != nil {
			mediaCleanupErrors.WithLabelValues("list_candidates").Inc()
			slog.Warn("service-media: retention candidates failed", "company", cfg.CompanyCode, "error", cerr)
			continue
		}
		if len(candidates) == 0 {
			s.updateStorageObjects(ctx, cfg.CompanyCode)
			continue
		}

		expired := make([]int64, 0, len(candidates))
		for _, media := range candidates {
			if derr := s.storage.Delete(ctx, media.ObjectKey); derr != nil {
				mediaCleanupErrors.WithLabelValues("delete_object").Inc()
				slog.Error("service-media: object delete failed (row kept for retry)",
					"company", cfg.CompanyCode, "media_id", media.ID, "key", media.ObjectKey, "error", derr)
				continue
			}
			expired = append(expired, media.ID)
			mediaCleanupDeleted.Inc()
			s.auditRetention(ctx, cfg.CompanyCode, media)
		}
		if len(expired) == 0 {
			continue
		}
		if merr := s.store.MarkMediaExpired(ctx, cfg.CompanyCode, expired); merr != nil {
			mediaCleanupErrors.WithLabelValues("mark_expired").Inc()
			slog.Error("service-media: marking expired failed", "company", cfg.CompanyCode, "error", merr)
			continue
		}
		slog.Info("service-media: retention swept", "company", cfg.CompanyCode,
			"expired", len(expired), "retention_days", cfg.EffectiveRetention(s.settings.RetentionDays))
		s.updateStorageObjects(ctx, cfg.CompanyCode)
	}
}

// auditRetention records a retention deletion (HARD_DELETE per §6.0.1/§11).
func (s *Service) auditRetention(ctx context.Context, company string, media models.MediaEvent) {
	if s.auditor == nil {
		return
	}
	// Best-effort: the object is already gone; a failed audit row is retried and
	// dead-lettered by the Auditor (never silently dropped).
	_ = s.auditor.Write(ctx, AuditRow{
		Action:      ActionHardDelete,
		Outcome:     OutcomeSuccess,
		CompanyCode: company,
		EntityType:  "media_event",
		EntityID:    strconv.FormatInt(media.ID, 10),
		Reason:      "retention",
	})
}

// updateStorageObjects publishes the FR-8.8 storage_objects gauge for one tenant.
func (s *Service) updateStorageObjects(ctx context.Context, company string) {
	total, err := s.store.CountStoredObjects(ctx, company)
	if err != nil {
		return
	}
	storageObjects.WithLabelValues(s.settings.Bucket).Set(float64(total))
}
