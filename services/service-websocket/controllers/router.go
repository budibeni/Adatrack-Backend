package controllers

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// buildRouter wires the middleware chain and the canonical PRD §8.2/§8.3 routes.
//
// Request flow (PRD §8.6): request-id → recovery → security headers → CORS →
// access log → body limit → [route group] rate limit → JWT verify (+revocation)
// → tenant resolution (company_code from the TOKEN, never from the body) →
// RBAC (role + row-level) → handler.
func (s *Service) buildRouter() *gin.Engine {
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(
		requestIDMiddleware(),
		recoveryMiddleware(),
		securityHeadersMiddleware(),
		s.corsMiddleware(),
		accessLogMiddleware(),
		s.bodyLimitMiddleware(),
	)
	engine.HandleMethodNotAllowed = true
	engine.NoRoute(func(c *gin.Context) {
		s.countHTTPError(http.StatusNotFound, CodeRouteNotFound)
		respondError(c, errNotFound(CodeRouteNotFound, "route not found"))
	})
	engine.NoMethod(func(c *gin.Context) {
		s.countHTTPError(http.StatusMethodNotAllowed, CodeMethodNotAllowed)
		respondError(c, NewAPIError(http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"method not allowed for this route"))
	})

	// --- operations (no auth, PRD §8.2 "ops") ------------------------------
	engine.GET("/healthz", s.handleHealthz)
	engine.GET("/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
	})
	s.mountMetrics(engine)

	api := engine.Group("/api/v1")

	// --- public authentication (PRD §8.2, rate-limited 5/15m on login) -----
	auth := api.Group("/auth")
	auth.POST("/login", s.apiRateLimitMiddleware(), s.handleLogin)
	auth.POST("/refresh", s.apiRateLimitMiddleware(), s.handleRefresh)
	auth.POST("/logout", s.authenticate(), s.apiRateLimitMiddleware(), s.handleLogout)

	// --- platform tier (SuperAdmin, context `default`) — PRD §3.1 ----------
	platform := api.Group("",
		s.authenticate(), s.apiRateLimitMiddleware(), s.requirePlatformOnly())
	platform.POST("/companies", s.handleCreateCompany)
	platform.POST("/users", s.handleCreateUser)

	// --- tenant tier (JWT + RBAC + row-level) ------------------------------
	tenant := api.Group("",
		s.authenticate(), s.apiRateLimitMiddleware(), s.requireTenantScope(), requirePasswordRotated())
	tenant.GET("/vehicles", s.handleListVehicles)
	tenant.GET("/vehicles/:id", s.handleVehicleDetail)
	tenant.GET("/vehicles/:id/history", s.handleVehicleHistory)

	// --- real-time WebSocket (PRD §8.3) ------------------------------------
	engine.GET("/ws/v1/adatrack",
		s.authenticate(), requirePasswordRotated(), s.handleWS)

	return engine
}

// mountMetrics exposes /metrics when a registry was supplied (PRD §14.2: the
// healthz/metrics port of service-websocket is 8082, the same listener).
func (s *Service) mountMetrics(engine *gin.Engine) {
	if s.registry == nil {
		engine.GET("/metrics", func(c *gin.Context) {
			c.Status(http.StatusNotFound)
		})
		return
	}
	engine.GET("/metrics", gin.WrapH(promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})))
}

// handleHealthz reports readiness: PostgreSQL master + tenant pools, Redis and
// NATS (PRD §10.2). A critical dependency failure returns 503.
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
	if ready, ok := s.store.(ReadinessStore); ok {
		if err := ready.Readiness(ctx); err != nil {
			checks["postgres"] = "error: " + err.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks["postgres"] = "ok"
			checks["postgres_master"] = "ok"
			checks["tenant_pools"] = "ok"
		}
	} else if pg, ok := s.PostgresStore(); ok {
		if err := pg.Master().Ping(ctx); err != nil {
			checks["postgres_master"] = "error: " + err.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks["postgres_master"] = "ok"
		}
		if err := pg.TenantHealth(ctx); err != nil {
			checks["tenant_pools"] = "error: " + err.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks["tenant_pools"] = "ok"
		}
	}
	if s.nats != nil {
		if !s.nats.IsConnected() {
			checks["nats"] = "error: " + errNATS.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks["nats"] = "ok"
		}
	}

	body := gin.H{"status": "ok", "checks": checks, "connections": s.hub.ActiveConnections(),
		"time": time.Now().UTC().Format(time.RFC3339)}
	if status != http.StatusOK {
		body["status"] = "unavailable"
	}
	c.JSON(status, body)
}
