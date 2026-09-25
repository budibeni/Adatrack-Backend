package controllers

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// registerAnalyticsRoutes wires §1.1 heatmap, §1.6 reports/analytics and §1.5
// safety score:
//
//	GET  /api/v1/heatmap                 (cached density cells)
//	POST /api/v1/heatmap/rebuild          (Admin: recompute from history)
//	GET  /api/v1/reports/trips            (trip summary of a window)
//	GET  /api/v1/reports/violations       (B8 violations per type/severity)
//	GET  /api/v1/safety/scores            (B8 driver scores)
func (s *Service) registerAnalyticsRoutes(group *gin.RouterGroup) {
	heatmap := group.Group("/heatmap")
	heatmap.GET("", s.handleHeatmap)
	heatmap.POST("/rebuild", s.requireAdmin(), s.handleRebuildHeatmap)

	reports := group.Group("/reports")
	reports.GET("/trips", s.handleTripReport)
	reports.GET("/violations", s.handleViolationReport)

	safety := group.Group("/safety")
	safety.GET("/scores", s.handleSafetyScores)
}

// analyticsWindow parses the `from`/`to` query bounds (RFC3339); the default is
// the last 7 days. It rejects a reversed window instead of silently swapping it.
func analyticsWindow(c *gin.Context) (time.Time, time.Time, *APIError) {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -7)
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, errValidation("invalid to timestamp",
				map[string]string{"to": "must be RFC3339"})
		}
		to = parsed
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, errValidation("invalid from timestamp",
				map[string]string{"from": "must be RFC3339"})
		}
		from = parsed
	}
	if from.After(to) {
		return from, to, errValidation("invalid window", map[string]string{"from": "must be before to"})
	}
	return from, to, nil
}

// analyticsLimit parses a bounded `limit` (default fallback, hard maximum 5000).
func analyticsLimit(c *gin.Context, fallback int) (int, *APIError) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 || parsed > 5000 {
		return 0, errValidation("invalid limit", map[string]string{"limit": "must be between 1 and 5000"})
	}
	return parsed, nil
}

// handleHeatmap implements GET /api/v1/heatmap (read cached cells only).
func (s *Service) handleHeatmap(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	limit, lerr := analyticsLimit(c, 1000)
	if lerr != nil {
		respondError(c, lerr)
		return
	}
	cells, err := s.enterprise.HeatmapCells(c.Request.Context(), identity.companyCode, limit)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, cells, nil)
}

// handleRebuildHeatmap implements POST /api/v1/heatmap/rebuild (Admin): the
// density cache is recomputed from telemetry history for the given window/cell.
func (s *Service) handleRebuildHeatmap(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	from, to, werr := analyticsWindow(c)
	if werr != nil {
		respondError(c, werr)
		return
	}
	cellSize := 0.01
	if raw := strings.TrimSpace(c.Query("cell")); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 || parsed > 1 {
			respondError(c, errValidation("invalid cell size",
				map[string]string{"cell": "must be a number between 0 and 1 (degrees)"}))
			return
		}
		cellSize = parsed
	}
	rows, err := s.enterprise.RebuildHeatmap(c.Request.Context(), identity.companyCode, from, to, cellSize)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"cells_written": rows, "from": from, "to": to, "cell": cellSize}, nil)
}

// handleTripReport implements GET /api/v1/reports/trips (Analysis → Reports).
func (s *Service) handleTripReport(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	from, to, werr := analyticsWindow(c)
	if werr != nil {
		respondError(c, werr)
		return
	}
	report, err := s.enterprise.TripReport(c.Request.Context(), identity.companyCode, from, to)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, report, nil)
}

// handleViolationReport implements GET /api/v1/reports/violations.
func (s *Service) handleViolationReport(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	from, to, werr := analyticsWindow(c)
	if werr != nil {
		respondError(c, werr)
		return
	}
	rows, err := s.enterprise.ViolationReport(c.Request.Context(), identity.companyCode, from, to)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, rows, nil)
}

// handleSafetyScores implements GET /api/v1/safety/scores.
func (s *Service) handleSafetyScores(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	from, to, werr := analyticsWindow(c)
	if werr != nil {
		respondError(c, werr)
		return
	}
	limit, lerr := analyticsLimit(c, 500)
	if lerr != nil {
		respondError(c, lerr)
		return
	}
	scores, err := s.enterprise.SafetyScores(c.Request.Context(), identity.companyCode, from, to, limit)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, scores, nil)
}
