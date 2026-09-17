package controllers

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/internal"
)

// Context keys for values shared between middleware and handlers.
const (
	ctxRequestID = "request_id"
	ctxClaims    = "claims"
	ctxIdentity  = "identity"
)

// requestIDMiddleware propagates `X-Request-ID` (PRD §9.4 correlation: the same
// id lands in the audit row and in every log line of the request).
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if id == "" || len(id) > 64 {
			if gen, err := randomToken(8); err == nil {
				id = gen
			} else {
				id = "req-" + itoa(time.Now().UnixNano())
			}
		}
		c.Set(ctxRequestID, id)
		c.Writer.Header().Set("X-Request-ID", id)
		c.Next()
	}
}

// requestID returns the correlation id of the current request.
func requestID(c *gin.Context) string {
	if v, ok := c.Get(ctxRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// recoveryMiddleware converts a panic into a 500 with the PRD §8.1 envelope
// (no stack trace ever reaches the client).
func recoveryMiddleware() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		slog.Error("service-websocket: panic recovered",
			"path", c.FullPath(), "request_id", requestID(c), "panic", recovered)
		respondError(c, errInternal("internal error"))
		c.Abort()
	})
}

// securityHeadersMiddleware applies the PRD §9.3 hardening headers.
func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		c.Next()
	}
}

// corsMiddleware implements the dashboard allowlist (PRD §9.3) and answers
// pre-flight requests. Only the allowlisted origins are ever echoed back.
func (s *Service) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && s.settings.OriginAllowed(origin) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// accessLogMiddleware emits one structured JSON log line per request and feeds
// the shared HTTP metrics (PRD §10.1).
func accessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method, "path", route, "status", status,
			"duration_ms", time.Since(start).Milliseconds(), "request_id", requestID(c),
			"ip", c.ClientIP(),
		}
		switch {
		case status >= http.StatusInternalServerError:
			slog.Error("request failed", attrs...)
		case status >= http.StatusBadRequest:
			slog.Warn("request rejected", attrs...)
		default:
			slog.Info("request", attrs...)
		}

		if internal.HTTPRequestsTotal != nil {
			internal.HTTPRequestsTotal.WithLabelValues("service-websocket", c.Request.Method, route,
				itoa(int64(status))).Inc()
		}
		if internal.HTTPRequestDuration != nil {
			internal.HTTPRequestDuration.WithLabelValues("service-websocket", c.Request.Method, route).
				Observe(time.Since(start).Seconds())
		}
	}
}

// extractToken reads the bearer token from the Authorization header, falling
// back to the `token` query parameter (the documented WebSocket handshake —
// browsers cannot set headers on `new WebSocket()`; PRD §8.3).
func extractToken(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return strings.TrimSpace(parts[1])
		}
		return ""
	}
	return strings.TrimSpace(c.Query("token"))
}

// authenticate validates the JWT (signature, issuer, expiry, revocation), loads
// the auth authority row from master `tm_users`, resolves the tenant role +
// row-level grants and puts both on the context (PRD §8.6 request flow).
func (s *Service) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw == "" {
			s.denyRequest(c, nil, errUnauthorized("missing bearer token"), "missing_token")
			return
		}
		claims, err := s.auth.ParseAccessToken(raw)
		if err != nil {
			s.denyRequest(c, nil, err, "invalid_token")
			return
		}

		revoked, rerr := s.auth.Revoked(c.Request.Context(), claims)
		if rerr != nil {
			respondError(c, errUnavailable("token revocation state unavailable"))
			c.Abort()
			return
		}
		if revoked {
			respondError(c, s.auth.RevokedTokenError(c.Request.Context(), claims,
				c.ClientIP(), c.Request.UserAgent(), requestID(c)))
			c.Abort()
			return
		}

		user, uerr := s.store.UserByID(c.Request.Context(), claims.UserID)
		if uerr != nil {
			respondError(c, errUnavailable("authentication backend unavailable"))
			c.Abort()
			return
		}
		if user == nil || !user.IsActive {
			s.denyRequest(c, claims, NewAPIError(401, CodeAccountInactive, "account is inactive"), "inactive_account")
			return
		}

		identity, ierr := s.auth.resolveIdentity(c.Request.Context(), user)
		if ierr != nil {
			s.denyRequest(c, claims, ierr, "rbac_denied")
			return
		}

		c.Set(ctxClaims, claims)
		c.Set(ctxIdentity, identity)
		c.Next()
	}
}

// requireTenantScope rejects a platform token on tenant routes with
// `403 PLATFORM_SCOPE` (PRD §3.1 platform tier).
func (s *Service) requireTenantScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			respondError(c, errUnauthorized("unauthenticated"))
			c.Abort()
			return
		}
		if identity.user.IsPlatform() {
			s.denyRequest(c, nil, errForbidden(CodePlatformScope,
				"platform credentials cannot access tenant endpoints"), "platform_scope")
			return
		}
		c.Next()
	}
}

// requirePlatformOnly rejects a tenant identity on platform routes with
// `403 PLATFORM_ONLY` (PRD §3.1, FR-5.5/FR-5.6).
func (s *Service) requirePlatformOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			respondError(c, errUnauthorized("unauthenticated"))
			c.Abort()
			return
		}
		if !identity.user.IsPlatform() {
			s.denyRequest(c, nil, errForbidden(CodePlatformOnly,
				"endpoint is restricted to the platform (SuperAdmin) tier"), "platform_only")
			return
		}
		c.Next()
	}
}

// requirePasswordRotated blocks every business endpoint while the account still
// carries the FR-5.5 default password (`403 PASSWORD_CHANGE_REQUIRED`).
func requirePasswordRotated() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			respondError(c, errUnauthorized("unauthenticated"))
			c.Abort()
			return
		}
		if identity.user.MustChangePassword {
			respondError(c, errForbidden(CodePasswordChangeNeeded,
				"password change required before using this endpoint"))
			c.Abort()
			return
		}
		c.Next()
	}
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

// bodyLimitMiddleware caps the request body (PRD §8.5 rule 4).
func (s *Service) bodyLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.settings.MaxBodyBytes > 0 && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.settings.MaxBodyBytes)
		}
		c.Next()
	}
}

// apiRateLimitMiddleware enforces PRD §8.4 (100 requests / minute / user). The
// identity of an authenticated request is used as the key; unauthenticated
// requests (login/refresh) are counted per IP.
func (s *Service) apiRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.settings.APIRateLimit <= 0 || s.settings.APIRateWindow <= 0 {
			c.Next()
			return
		}
		key := "adatrack_gps:auth:api:" + c.ClientIP()
		if claims, ok := currentClaims(c); ok {
			key = "adatrack_gps:auth:api:user:" + itoa(claims.UserID)
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

// denyRequest audits an access denial (ACCESS_DENIED, PRD §9.4) and answers with
// the PRD §8.1 error envelope. Denials are fail-closed only in the sense that the
// request is never served; the audit row itself is best-effort buffered.
func (s *Service) denyRequest(c *gin.Context, claims *Claims, err error, reason string) {
	apiErr := asAPIError(err)
	rbacDenied.WithLabelValues(reason).Inc()

	row := AuditRow{
		Action:         ActionAccessDenied,
		Outcome:        OutcomeDenied,
		ActorIP:        c.ClientIP(),
		ActorUserAgent: c.Request.UserAgent(),
		EntityType:     "endpoint",
		EntityID:       c.Request.Method + " " + c.FullPath(),
		Reason:         apiErr.Code + ": " + apiErr.Message,
		RequestID:      requestID(c),
	}
	if claims != nil {
		row.ActorUserID = claims.UserID
		row.ActorEmail = claims.Email
		row.ActorRole = claims.Role
		row.CompanyCode = claims.CompanyCode
	}
	if s.auditor != nil {
		s.auditor.Record(row)
	}

	s.countHTTPError(apiErr.Status, apiErr.Code)
	respondError(c, apiErr)
	c.Abort()
}

// countHTTPError feeds `http_errors_total{status,error_code}` (PRD §10.1).
func (s *Service) countHTTPError(status int, code string) {
	httpErrors.WithLabelValues(itoa(int64(status)), code).Inc()
}
