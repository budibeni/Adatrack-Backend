package controllers

import (
	"ajb_gps/internal"

	"github.com/gin-gonic/gin"
)

// securityHeadersMiddleware applies hardened HTTP security headers
// (CSP, HSTS, X-Frame-Options, anti-XSS) — Phase B4.2 (GAP #12).
func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		internal.ApplySecurityHeaders(c.Writer)
		c.Next()
	}
}
