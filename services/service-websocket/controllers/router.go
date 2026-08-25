package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// setupRouter wires the REST + WebSocket routes (module service-websocket).
func setupRouter() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	loginLimiter = newFailureRateLimiter(appCfg.RateLimit.LoginMaxAttempts, appCfg.RateLimit.LoginWindow)
	apiLimiter = newAPIRateLimiter(appCfg.RateLimit.APIMaxPerMinute)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())
	r.Use(httpMetricsMiddleware())
	r.Use(securityHeadersMiddleware())

	// Readiness + metrics juga tersedia di server utama.
	r.GET("/healthz", func(c *gin.Context) {
		healthHandler(c.Writer, c.Request)
	})
	r.GET("/metrics", gin.WrapH(metricsHTTP))

	api := r.Group("/api/v1")

	// Public: login (rate limit IP-based, GAP #12).
	api.POST("/auth/login", authLoginHandler)
	// B4 hardening: refresh (rotasi) + logout (revocation) — GAP #12.
	api.POST("/auth/refresh", authRefreshHandler)
	api.POST("/auth/logout", authLogoutHandler)

	// Protected routes (JWT + RBAC).
	authed := api.Group("")
	authed.Use(requireAuth())
	authed.Use(apiRateLimitMiddleware())
	{
		// PLATFORM-only (governance PRD §6.1): registrasi tenant + akun
		// pertama tenant — hanya SuperAdmin konteks 'default'.
		authed.POST("/companies", companyCreateHandler)
		authed.POST("/users", userCreateHandler)

		authed.GET("/vehicles", vehiclesListHandler)
		authed.GET("/vehicles/:id", vehicleDetailHandler)
		authed.GET("/vehicles/:id/history", vehicleHistoryHandler)

		authed.GET("/geofences", geofencesListHandler)
		authed.POST("/geofences", geofencesCreateHandler)
		authed.GET("/geofences/:id", geofenceDetailHandler)
		authed.DELETE("/geofences/:id", geofenceDeleteHandler)

		authed.GET("/alerts", alertsListHandler)
		authed.PATCH("/alerts/:id/acknowledge", alertAckHandler)

		// Route management + live tracking (B3: disiplin driver).
		authed.GET("/routes", routesListHandler)
		authed.POST("/routes", routesCreateHandler)
		authed.GET("/routes/:id", routeDetailHandler)
		authed.PATCH("/routes/:id", routesUpdateHandler)
		authed.DELETE("/routes/:id", routeDeleteHandler)
		authed.GET("/routes/:id/track", routeTrackHandler)
	}

	// WebSocket: auth via ?token= atau header Authorization.
	api.GET("/ws", websocketHandler)

	return r
}
