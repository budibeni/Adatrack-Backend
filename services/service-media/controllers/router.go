package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// buildRouter wires the middleware chain and the PRD §8.2 media routes.
//
// Request flow: request-id → recovery → security headers → CORS → access log →
// body limit → [JWT group] authenticate → rate limit → tenant scope → password
// rotation → RBAC (role + row-level) → handler. The ingest routes are guarded by
// the per-company HMAC handshake instead (FR-8.1).
func (s *Service) buildRouter() *gin.Engine {
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
		s.countHTTPError(http.StatusNotFound, CodeEndpointNotFound)
		respondError(c, errNotFound(CodeEndpointNotFound, "route not found"))
	})
	engine.NoMethod(func(c *gin.Context) {
		s.countHTTPError(http.StatusMethodNotAllowed, CodeMethodNotAllowed)
		respondError(c, NewAPIError(http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"method not allowed for this route"))
	})

	// --- operations (no auth, PRD §10.2/§14.2) ------------------------------
	engine.GET("/healthz", s.handleHealthz)
	engine.GET("/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
	})
	s.mountMetrics(engine)

	api := engine.Group("/api/v1")

	// --- ingest tier (HMAC X-Signature per company, FR-8.1) -----------------
	// Rate limit dulu (murah, per IP) baru verifikasi HMAC (butuh buffer body).
	ingest := api.Group("/media/events", s.ingestRateLimitMiddleware(), s.requireHMAC())
	ingest.POST("", s.handleIngestEvent)
	ingest.POST("/:id/complete", s.handleCompleteEvent)

	// --- tenant tier (JWT + RBAC + row-level, PRD §3.1/§8.2) ----------------
	tenant := api.Group("", s.authenticate(), s.apiRateLimitMiddleware(),
		s.requireTenantScope(), s.requirePasswordRotated())
	tenant.GET("/media", s.handleListMedia)
	tenant.GET("/media/:id", s.handleMediaDetail)
	tenant.GET("/media/:id/url", s.handleMediaURL)
	tenant.DELETE("/media/:id", s.requireAdmin(), s.handleDeleteMedia)
	tenant.POST("/media/:id/restore", s.requireAdmin(), s.handleRestoreMedia)

	return engine
}

// mountMetrics exposes /metrics on the main listener (PRD §14.2).
func (s *Service) mountMetrics(engine *gin.Engine) {
	if s.registry == nil {
		engine.GET("/metrics", func(c *gin.Context) { c.Status(http.StatusNotFound) })
		return
	}
	engine.GET("/metrics", gin.WrapH(promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})))
}

// handleHealthz reports readiness: object storage + PostgreSQL pools + Redis +
// NATS (PRD §10.2 / FR-8.8). A failure returns 503.
func (s *Service) handleHealthz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	checks := map[string]string{}
	status := http.StatusOK
	fail := func(name string, err error) {
		checks[name] = "error: " + err.Error()
		status = http.StatusServiceUnavailable
	}

	if err := s.storage.Health(ctx); err != nil {
		fail("object_storage", err)
	} else {
		checks["object_storage"] = "ok"
	}
	if s.kv != nil {
		if err := s.kv.Ping(ctx); err != nil {
			fail("redis", err)
		} else {
			checks["redis"] = "ok"
		}
	}
	if s.store != nil {
		if err := s.store.Master().Ping(ctx); err != nil {
			fail("postgres_master", err)
		} else {
			checks["postgres_master"] = "ok"
		}
		if err := s.store.TenantHealth(ctx); err != nil {
			fail("tenant_pools", err)
		} else {
			checks["tenant_pools"] = "ok"
		}
	}
	if s.nats != nil {
		if !s.nats.IsConnected() {
			fail("nats", fmt.Errorf("not connected"))
		} else {
			checks["nats"] = "ok"
		}
	}

	body := gin.H{"status": "ok", "checks": checks, "time": time.Now().UTC().Format(time.RFC3339)}
	if status != http.StatusOK {
		body["status"] = "degraded"
	}
	c.JSON(status, body)
}
