package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// buildRouter wires the middleware chain and the PRD §8.2 fleet routes.
//
// Request flow: request-id → recovery → security headers → CORS → access log →
// body limit → [group] JWT verify (+ revocation) → rate limit → tenant scope →
// password rotation → RBAC (role + row-level) → handler.
func (s *Service) buildRouter() *gin.Engine {
	engine := gin.New()
	engine.Use(
		requestIDMiddleware(),
		recoveryMiddleware(),
		securityHeadersMiddleware(),
		s.corsMiddleware(),
		accessLogMiddleware("api-vehicle"),
		s.bodyLimitMiddleware(),
	)
	engine.HandleMethodNotAllowed = true
	engine.NoRoute(func(c *gin.Context) {
		s.countHTTPError(http.StatusNotFound, CodeEndpointNotFound)
		respondError(c, errNotFound(CodeEndpointNotFound, "route not found"))
	})
	engine.NoMethod(func(c *gin.Context) {
		s.countHTTPError(http.StatusMethodNotAllowed, CodeMethodNotAllowed)
		respondError(c, NewAPIError(http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"method not allowed for this route"))
	})

	// --- operations (no auth, PRD §10.2) ------------------------------------
	engine.GET("/healthz", s.handleHealthz)
	engine.GET("/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
	})
	s.mountMetrics(engine)

	api := engine.Group("/api/v1")

	// FR-9.3 public location share: UNAUTHENTICATED by design — the token itself
	// carries the tenant, so it must be mounted BEFORE the tenant tier.
	s.registerPublicShareRoute(api)

	// --- tenant tier (JWT + RBAC + row-level, PRD §3.1/§8.2) ----------------
	tenant := api.Group("",
		s.authenticate(), s.apiRateLimitMiddleware(), s.requireTenantScope(),
		s.requirePasswordRotated())
	// B11: every mutation behind this point writes the mandatory audit trail
	// (PRD §9.4). Mounted last so it wraps the handlers and can read the final
	// status code, after the actor/tenant have been resolved.
	tenant.Use(s.auditMutationMiddleware())

	vehicles := tenant.Group("/vehicles")
	vehicles.GET("", s.handleListVehicles)
	vehicles.POST("", s.requireWrite(), s.handleCreateVehicle)
	vehicles.GET("/:id", s.requireVehicleAccess(), s.handleVehicleDetail)
	vehicles.PATCH("/:id", s.requireVehicleAccess(), s.requireWrite(), s.handleUpdateVehicle)
	vehicles.DELETE("/:id", s.requireVehicleAccess(), s.requireAdmin(), s.handleDeleteVehicle)
	vehicles.POST("/:id/restore", s.requireVehicleAccess(), s.requireAdmin(), s.handleRestoreVehicle)

	// B8 downlink commands: the request/ACK audit trail of a vehicle. Writing a
	// command needs write access on that exact vehicle (row-level RBAC).
	vehicles.POST("/:id/commands", s.requireVehicleAccess(), s.requireWrite(), s.handleCreateVehicleCommand)
	vehicles.GET("/:id/commands", s.requireVehicleAccess(), s.handleListVehicleCommands)

	geofences := tenant.Group("/geofences")
	geofences.GET("", s.handleListGeofences)
	geofences.POST("", s.requireWrite(), s.handleCreateGeofence)
	geofences.GET("/:id", s.handleGeofenceDetail)
	geofences.PATCH("/:id", s.requireWrite(), s.handleUpdateGeofence)
	geofences.DELETE("/:id", s.requireAdmin(), s.handleDeleteGeofence)
	geofences.POST("/:id/restore", s.requireAdmin(), s.handleRestoreGeofence)

	routes := tenant.Group("/routes")
	routes.GET("", s.handleListRoutes)
	routes.POST("", s.requireWrite(), s.handleCreateRoute)
	routes.GET("/:id", s.handleRouteDetail)
	routes.PATCH("/:id", s.requireWrite(), s.handleUpdateRoute)
	routes.DELETE("/:id", s.requireAdmin(), s.handleDeleteRoute)
	routes.POST("/:id/restore", s.requireAdmin(), s.handleRestoreRoute)
	routes.GET("/:id/assignments", s.handleListAssignments)
	routes.POST("/:id/assignments", s.requireWrite(), s.handleCreateAssignment)
	routes.PATCH("/:id/assignments/:assignmentId", s.requireWrite(), s.handlePatchAssignment)
	routes.DELETE("/:id/assignments/:assignmentId", s.requireAdmin(), s.handleDeleteAssignment)

	speed := tenant.Group("/speed-configs")
	speed.GET("", s.handleListSpeedConfigs)
	speed.POST("", s.requireWrite(), s.handleCreateSpeedConfig)
	speed.GET("/:id", s.handleSpeedConfigDetail)
	speed.PATCH("/:id", s.requireWrite(), s.handleUpdateSpeedConfig)
	speed.DELETE("/:id", s.requireAdmin(), s.handleDeleteSpeedConfig)
	speed.POST("/:id/restore", s.requireAdmin(), s.handleRestoreSpeedConfig)

	fuel := tenant.Group("/fuel-configs")
	fuel.GET("", s.handleListFuelConfigs)
	fuel.POST("", s.requireWrite(), s.handleCreateFuelConfig)
	fuel.GET("/:id", s.handleFuelConfigDetail)
	fuel.PATCH("/:id", s.requireWrite(), s.handleUpdateFuelConfig)
	fuel.DELETE("/:id", s.requireAdmin(), s.handleDeleteFuelConfig)
	fuel.POST("/:id/restore", s.requireAdmin(), s.handleRestoreFuelConfig)

	vehicles.GET("/:id/fuel/history", s.requireVehicleAccess(), s.handleVehicleFuelHistory)

	alerts := tenant.Group("/alerts")
	alerts.GET("", s.handleListAlerts)
	alerts.POST("/:id/acknowledge", s.handleAcknowledgeAlert)
	alerts.POST("/:id/resolve", s.handleResolveAlert)

	// B11 governance: the tenant slice of the append-only audit trail (§9.4).
	// Reading history is itself audited (AUDIT_LOGS_VIEWED).
	auditLogs := tenant.Group("/audit-logs")
	auditLogs.GET("", s.requireAdmin(), s.handleListAuditLogs)

	// --- B12 enterprise & industry modules (additive, PRD §5.10) -------------
	// Every table-driven enterprise resource gets list/get/create/update/delete/
	// restore with RBAC + soft delete + audit; the bespoke modules (menu registry,
	// integrations, share, heatmap/reports/safety, group mapping) are wired below.
	for _, spec := range enterpriseResources {
		s.registerEnterpriseRoutes(tenant, spec)
	}
	s.registerGroupMemberRoutes(tenant)
	s.registerAccessGovernanceRoutes(tenant)
	s.registerIntegrationRoutes(tenant)
	s.registerShareRoutes(tenant)
	s.registerAnalyticsRoutes(tenant)

	return engine
}

// mountMetrics exposes /metrics when a registry was supplied (PRD §14.2).
func (s *Service) mountMetrics(engine *gin.Engine) {
	if s.registry == nil {
		engine.GET("/metrics", func(c *gin.Context) { c.Status(http.StatusNotFound) })
		return
	}
	engine.GET("/metrics", gin.WrapH(promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})))
}

// handleHealthz reports readiness: Redis (revocation + rate limit) and
// PostgreSQL master + tenant pools (PRD §10.2). Failures return 503.
func (s *Service) handleHealthz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	checks := map[string]string{}
	status := http.StatusOK

	if s.kv != nil {
		if err := s.kv.Ping(ctx); err != nil {
			checks["redis"] = "error: " + err.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks["redis"] = "ok"
		}
	}
	if s.tenants != nil {
		if err := s.tenants.Health(ctx); err != nil {
			checks["postgres"] = "error: " + err.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks["postgres"] = "ok"
			checks["postgres_master"] = "ok"
			checks["tenant_pools"] = "ok"
		}
	}

	body := gin.H{"status": "ok", "checks": checks, "time": time.Now().UTC().Format(time.RFC3339)}
	if status != http.StatusOK {
		body["status"] = "degraded"
	}
	c.JSON(status, body)
}
