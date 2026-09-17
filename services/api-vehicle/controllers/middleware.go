package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/internal"
)

// Context keys shared between middleware and handlers.
const (
	ctxRequestID = "request_id"
	ctxClaims    = "claims"
	ctxIdentity  = "identity"
)

// requestIDMiddleware propagates `X-Request-ID` (PRD §9.4 correlation).
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if id == "" || len(id) > 64 {
			if gen, err := randomToken(8); err == nil {
				id = gen
			} else {
				id = "req-" + strconv.FormatInt(time.Now().UnixNano(), 10)
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

// randomToken returns n random bytes hex encoded.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// recoveryMiddleware converts a panic into a 500 with the PRD §8.1 envelope.
func recoveryMiddleware() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		slog.Error("api-vehicle: panic recovered",
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
		c.Next()
	}
}

// corsMiddleware implements the dashboard allowlist (PRD §9.3).
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
func accessLogMiddleware(service string) gin.HandlerFunc {
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
			internal.HTTPRequestsTotal.WithLabelValues(service, c.Request.Method, route,
				strconv.Itoa(status)).Inc()
		}
		if internal.HTTPRequestDuration != nil {
			internal.HTTPRequestDuration.WithLabelValues(service, c.Request.Method, route).
				Observe(time.Since(start).Seconds())
		}
	}
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
