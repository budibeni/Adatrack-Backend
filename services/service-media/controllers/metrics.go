package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

// Media metrics (PRD §10.1 / FR-8.8).
var (
	mediaUploads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_uploads_total",
		Help: "Media objects accepted at ingest, per company and media type",
	}, []string{"company_code", "media_type"})

	mediaUploadBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_upload_bytes_total",
		Help: "Bytes of media accepted at ingest, per company",
	}, []string{"company_code"})

	mediaPresigned = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_presigned_total",
		Help: "Presigned URLs issued, per purpose (upload|read)",
	}, []string{"purpose"})

	mediaCleanupDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "media_cleanup_deleted_total",
		Help: "Objects deleted by the retention sweep (FR-8.7)",
	})

	storageObjects = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_objects",
		Help: "Catalog rows holding a stored object (sampled, per bucket)",
	}, []string{"bucket"})

	mediaCleanupErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_cleanup_errors_total",
		Help: "Retention sweep failures, per stage",
	}, []string{"stage"})

	mediaEventsPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_events_published_total",
		Help: "media.event.<company> frames published to the WebSocket fan-out (FR-8.5)",
	}, []string{"result"})

	rbacDenied = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_rbac_denied_total",
		Help: "Requests denied by the service-media auth/RBAC layer, per reason",
	}, []string{"reason"})

	httpErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_errors_total",
		Help: "HTTP error responses, per status and error_code",
	}, []string{"status", "error_code"})

	auditWriteErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "media_audit_write_errors_total",
		Help: "tm_audit_logs write failures (retried then dead-lettered)",
	})
)

// RegisterMetrics registers the service-media collectors.
func RegisterMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	reg.MustRegister(mediaUploads, mediaUploadBytes, mediaPresigned, mediaCleanupDeleted,
		storageObjects, mediaCleanupErrors, mediaEventsPublished, rbacDenied, httpErrors,
		auditWriteErrors)
}

// countHTTPError feeds `http_errors_total{status,error_code}`.
func (s *Service) countHTTPError(status int, code string) {
	httpErrors.WithLabelValues(strconv.Itoa(status), code).Inc()
}

// denyRequest answers an auth/RBAC failure with the PRD §8.1 envelope.
func (s *Service) denyRequest(c *gin.Context, err error, reason string) {
	apiErr := asAPIError(err)
	rbacDenied.WithLabelValues(reason).Inc()
	s.countHTTPError(apiErr.Status, apiErr.Code)
	respondError(c, apiErr)
	c.Abort()
}

// currentIdentity returns the resolved identity of the request.
func currentIdentity(c *gin.Context) (*tenantIdentity, bool) {
	v, ok := c.Get(ctxIdentity)
	if !ok {
		return nil, false
	}
	identity, ok := v.(*tenantIdentity)
	return identity, ok
}

// currentClaims returns the verified JWT claims of the request.
func currentClaims(c *gin.Context) (*Claims, bool) {
	v, ok := c.Get(ctxClaims)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*Claims)
	return claims, ok
}

// requireWrite allows Admin/Manager to mutate media rows (PRD §8.2).
func (s *Service) requireWrite() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			s.denyRequest(c, errUnauthorized("missing identity"), "missing_identity")
			return
		}
		switch identity.role {
		case RoleAdmin, RoleManager:
			c.Next()
		default:
			s.denyRequest(c, errForbidden(CodeForbidden,
				"only Admin or Manager may modify media records"), "write_role")
		}
	}
}

// requireAdmin restricts soft delete / restore to the tenant Admin (FR-8.9).
func (s *Service) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			s.denyRequest(c, errUnauthorized("missing identity"), "missing_identity")
			return
		}
		if identity.role != RoleAdmin {
			s.denyRequest(c, errForbidden(CodeForbidden,
				"only Admin may delete or restore media records"), "admin_role")
			return
		}
		c.Next()
	}
}

// requireTenantScope rejects platform tokens on media routes (PRD §3.1).
func (s *Service) requireTenantScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if identity, ok := currentIdentity(c); ok && identity.platform {
			s.denyRequest(c, errForbidden(CodePlatformScope,
				"platform identity cannot access tenant media resources"), "platform_scope")
			return
		}
		c.Next()
	}
}

// requirePasswordRotated blocks business endpoints until the FR-5.5 default
// password has been changed (same contract as service-websocket).
func (s *Service) requirePasswordRotated() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			respondError(c, errUnauthorized("unauthenticated"))
			c.Abort()
			return
		}
		if identity.mustChangePassword {
			respondError(c, errForbidden(CodePasswordChangeNeeded,
				"password change required before using this endpoint"))
			c.Abort()
			return
		}
		c.Next()
	}
}

// ingestRateLimitMiddleware bounds the HMAC ingest tier per client IP BEFORE the
// signature is checked: verifying the signature needs the body, so an
// unauthenticated flood must be rejected as cheaply as possible (PRD §8.4 +
// audit finding 2026-09-22). A Redis failure is fail-closed (503) like the JWT
// limiter; `MEDIA_INGEST_RATE_LIMIT=0` disables the limiter.
func (s *Service) ingestRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.settings.IngestRateLimit <= 0 || s.settings.IngestRateWindow <= 0 || s.kv == nil {
			c.Next()
			return
		}
		key := "adatrack_gps:media:ingest:" + c.ClientIP()
		n, err := s.kv.Incr(c.Request.Context(), key)
		if err != nil {
			respondError(c, errUnavailable("rate limiter unavailable"))
			c.Abort()
			return
		}
		if n == 1 {
			if err := s.kv.Expire(c.Request.Context(), key, s.settings.IngestRateWindow); err != nil {
				respondError(c, errUnavailable("rate limiter unavailable"))
				c.Abort()
				return
			}
		}
		if n > int64(s.settings.IngestRateLimit) {
			s.countHTTPError(http.StatusTooManyRequests, CodeRateLimited)
			rbacDenied.WithLabelValues("ingest_rate_limit").Inc()
			respondError(c, errRateLimited("ingest rate limit exceeded"))
			c.Abort()
			return
		}
		c.Next()
	}
}

// apiRateLimitMiddleware enforces PRD §8.4 on the JWT-authenticated routes.
func (s *Service) apiRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.settings.APIRateLimit <= 0 || s.settings.APIRateWindow <= 0 || s.kv == nil {
			c.Next()
			return
		}
		key := "adatrack_gps:media:api:" + c.ClientIP()
		if claims, ok := currentClaims(c); ok {
			key = "adatrack_gps:media:api:user:" + strconv.FormatInt(claims.UserID, 10)
		}
		n, err := s.kv.Incr(c.Request.Context(), key)
		if err != nil {
			respondError(c, errUnavailable("rate limiter unavailable"))
			c.Abort()
			return
		}
		if n == 1 {
			if err := s.kv.Expire(c.Request.Context(), key, s.settings.APIRateWindow); err != nil {
				respondError(c, errUnavailable("rate limiter unavailable"))
				c.Abort()
				return
			}
		}
		if n > int64(s.settings.APIRateLimit) {
			s.countHTTPError(http.StatusTooManyRequests, CodeRateLimited)
			respondError(c, errRateLimited("rate limit exceeded"))
			c.Abort()
			return
		}
		c.Next()
	}
}
