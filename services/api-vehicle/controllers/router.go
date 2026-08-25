package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// setupRouter wires the REST + metrics routes for api-vehicle (multi-tenant).
func setupRouter() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	loginLimiter = newFailureRateLimiter(appCfg.RateLimit.LoginMaxAttempts, appCfg.RateLimit.LoginWindow)
	apiLimiter = newAPIRateLimiter(appCfg.RateLimit.APIMaxPerMinute)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())
	r.Use(httpMetricsMiddleware())
	r.Use(securityHeadersMiddleware())

	// Health + metrics
	r.GET("/healthz", healthzHandler)
	r.GET("/metrics", gin.WrapH(metricsHTTP))

	api := r.Group("/api/v1")

	// Public: login (master DB auth, rate limit IP-based — GAP #12).
	api.POST("/auth/login", authLoginHandler)
	// B4 hardening: refresh (rotasi) + logout (revocation) — GAP #12.
	api.POST("/auth/refresh", authRefreshHandler)
	api.POST("/auth/logout", authLogoutHandler)

	// Protected: JWT → tenant routing → RBAC.
	authed := api.Group("")
	authed.Use(requireAuth())
	authed.Use(apiRateLimitMiddleware())
	{
		// Vehicles CRUD (migration 002).
		authed.GET("/vehicles", vehiclesListHandler)
		authed.POST("/vehicles", vehiclesCreateHandler)
		authed.GET("/vehicles/:id", vehicleDetailHandler)
		authed.PATCH("/vehicles/:id", vehiclesUpdateHandler)
		authed.DELETE("/vehicles/:id", vehiclesDeleteHandler)

		// User ↔ vehicle assignment (user_vehicles, migration 003).
		authed.GET("/vehicles/:id/users", vehicleUsersListHandler)
		authed.POST("/vehicles/:id/users", vehicleAssignUserHandler)
		authed.DELETE("/vehicles/:id/users/:userId", vehicleUnassignUserHandler)

		// Speed configs (migration 007) — worker-alert membaca config ini.
		authed.GET("/speed-configs", speedConfigsListHandler)
		authed.POST("/speed-configs", speedConfigsCreateHandler)
		authed.PATCH("/speed-configs/:id", speedConfigsUpdateHandler)
		authed.DELETE("/speed-configs/:id", speedConfigsDeleteHandler)

		// Geofence ↔ vehicle links (migration 006).
		authed.GET("/geofences/:id/vehicles", geofenceVehiclesListHandler)
		authed.POST("/geofences/:id/vehicles", geofenceVehiclesAddHandler)
		authed.DELETE("/geofences/:id/vehicles/:vehicleId", geofenceVehiclesRemoveHandler)

		// Routes + assignments (migrations 011/012) — worker-alert tracking.
		authed.GET("/routes", routesListHandler)
		authed.POST("/routes", routesCreateHandler)
		authed.GET("/routes/:id", routesDetailHandler)
		authed.PATCH("/routes/:id", routesUpdateHandler)
		authed.DELETE("/routes/:id", routesDeleteHandler)
		authed.POST("/routes/:id/assignments", routeAssignHandler)
		authed.PATCH("/routes/:id/assignments/:assignmentId", routeAssignmentStatusHandler)
		authed.DELETE("/routes/:id/assignments/:assignmentId", routeUnassignHandler)
	}

	return r
}

// Router returns the configured http.Handler for api-vehicle (used by main).
func Router() http.Handler {
	return setupRouter()
}