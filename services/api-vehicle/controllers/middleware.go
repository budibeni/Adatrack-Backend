package controllers

import (
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"ajb_gps/internal"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// CORS (PRD §7 CORS_ALLOWED_ORIGINS)
// ---------------------------------------------------------------------------

// corsMiddleware allows configured origins; "*" opens for dev.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if originAllowed(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		} else if len(appCfg.HTTP.CORSOrigins) == 1 && appCfg.HTTP.CORSOrigins[0] == "*" {
			c.Header("Access-Control-Allow-Origin", "*")
		}
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Header("Access-Control-Max-Age", "86400")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// originAllowed reports whether an Origin header is part of the allowlist.
func originAllowed(origin string) bool {
	if origin == "" || len(appCfg.HTTP.CORSOrigins) == 0 {
		return false
	}
	for _, o := range appCfg.HTTP.CORSOrigins {
		if o == origin {
			return true
		}
	}
	return appCfg.HTTP.CORSOrigins[0] == "*"
}

// httpMetricsMiddleware records http_requests_total + duration (PRD §8.1).
func httpMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = "unknown"
		}
		internal.HTTPRequestsTotal.WithLabelValues(c.Request.Method, endpoint, strconv.Itoa(status)).Inc()
		internal.HTTPRequestDuration.WithLabelValues(c.Request.Method, endpoint).
			Observe(time.Since(start).Seconds())
	}
}

// --- API rate limiting (100 req/min per user) ---
type apiRateBucket struct {
	windowStart time.Time
	count       int
}

type apiRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*apiRateBucket
	max     int
}

func newAPIRateLimiter(max int) *apiRateLimiter {
	return &apiRateLimiter{buckets: make(map[string]*apiRateBucket), max: max}
}

func (l *apiRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.windowStart) >= time.Minute {
		l.buckets[key] = &apiRateBucket{windowStart: now, count: 1}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}

var apiLimiter *apiRateLimiter

func apiRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()
		if u, ok := loadAuthUser(c); ok {
			key = strconv.FormatUint(u.ID, 10)
		}
		if !apiLimiter.allow(key) {
			slog.Warn("api rate limited", "key", key, "path", c.FullPath())
			writeError(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "API rate limit exceeded (100 req/min)")
			c.Abort()
			return
		}
		c.Next()
	}
}
