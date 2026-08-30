package controllers

import (
	"net/http"
	"strconv"
	"time"

	"ajb_gps/internal"

	"github.com/gin-gonic/gin"
)

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
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Signature, X-Content-SHA256")
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

// securityHeadersMiddleware applies hardened HTTP security headers (B4.2).
func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		internal.ApplySecurityHeaders(c.Writer)
		c.Next()
	}
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