package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// setupRouter wires the REST routes for service-media (B5b, Module 8).
func setupRouter() http.Handler {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())
	r.Use(httpMetricsMiddleware())
	r.Use(securityHeadersMiddleware())

	r.GET("/healthz", healthzHandler)
	r.GET("/metrics", gin.WrapH(metricsHTTP))

	api := r.Group("/api/v1")

	// Ingest endpoint — HMAC-SHA256 per-company (FR-8.1), NOT JWT.
	api.POST("/media/events", ingestMediaHandler)
	// Finalize a JSON-flow upload (presigned PUT done) — HMAC per-company.
	api.POST("/media/events/:id/complete", mediaCompleteHandler)

	// RBAC REST (FR-8.4) — validated against the shared JWT (interop B2/B3).
	authed := api.Group("")
	authed.Use(requireAuth())
	{
		authed.GET("/media", mediaListHandler)
		authed.GET("/media/:id", mediaDetailHandler)
		authed.GET("/media/:id/url", mediaURLHandler)
		authed.DELETE("/media/:id", mediaDeleteHandler)
	}

	return r
}

// Router returns the configured http.Handler (used by main).
func Router() http.Handler {
	return setupRouter()
}
