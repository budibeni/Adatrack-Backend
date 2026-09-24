package controllers

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/internal/geo"
	"adatrack_gps/service-websocket/models"
)

// handleVehiclePlayback implements `GET /api/v1/vehicles/{id}/playback`
// (B7.4 + B7.3, PRD §5.9.3): the positions of the window are reduced with
// Ramer–Douglas–Peucker and enriched with the offline reverse-geocoded address
// so the client can replay the route without losing its shape.
func (s *Service) handleVehiclePlayback(c *gin.Context) {
	identity, ok := currentIdentity(c)
	if !ok {
		respondError(c, errUnauthorized("unauthenticated"))
		return
	}
	if s.playback == nil || s.geocoder == nil {
		respondError(c, errUnavailable("playback is not available"))
		return
	}
	id, apiErr := parseIDParam(c, "id")
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	from, to, apiErr := s.parseTimeRange(c, 24*time.Hour)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	tolerance, apiErr := s.parsePlaybackTolerance(c)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	start := time.Now()
	vehicle, err := s.store.VehicleByID(c.Request.Context(), identity.user.CompanyCode, id, false)
	if err != nil {
		s.respondStoreError(c, err)
		return
	}
	observeRBAC(start)
	if vehicle == nil {
		respondError(c, errNotFound(CodeVehicleNotFound, "vehicle "+itoa(id)+" not found"))
		return
	}
	if !identity.canAccessVehicle(vehicle.ID) {
		s.denyRequest(c, nil, errForbidden(CodeUnauthorizedVehicle,
			"vehicle "+itoa(id)+" is not assigned to this user"), "vehicle")
		return
	}

	limit := s.playbackMaxPoints()
	positions, err := s.playback.VehiclePlayback(c.Request.Context(), PlaybackQuery{
		CompanyCode: identity.user.CompanyCode,
		VehicleID:   vehicle.ID,
		From:        from,
		To:          to,
		Limit:       limit + 1, // one extra row detects truncation
	})
	if err != nil {
		s.respondStoreError(c, err)
		return
	}
	truncated := len(positions) > limit
	if truncated {
		positions = positions[:limit]
	}

	respondOK(c, s.buildPlayback(c, vehicle, from, to, tolerance, positions, truncated), nil)
}

// buildPlayback reduces the route and geocodes the surviving points.
func (s *Service) buildPlayback(c *gin.Context, vehicle *models.Vehicle, from, to time.Time,
	tolerance float64, positions []models.Position, truncated bool) models.PlaybackResponse {
	response := models.PlaybackResponse{
		VehicleID:   vehicle.ID,
		IMEI:        vehicle.IMEI,
		From:        from.UTC().Format(time.RFC3339),
		To:          to.UTC().Format(time.RFC3339),
		ToleranceM:  tolerance,
		TotalPoints: len(positions),
		Truncated:   truncated,
	}
	if len(positions) == 0 {
		response.Points = []models.PlaybackPoint{}
		return response
	}

	// Route length is measured on the ORIGINAL sequence: the reduction must not
	// change the reported distance (only the rendering).
	points := make([]geo.Point, len(positions))
	for i := range positions {
		points[i] = geo.Point{Lat: positions[i].Lat, Lon: positions[i].Lon}
	}
	var distanceKM float64
	for i := 1; i < len(points); i++ {
		distanceKM += geo.DistanceKM(points[i-1], points[i])
	}
	response.DistanceKM = math.Round(distanceKM*1000) / 1000

	keep := geo.ReduceIndices(points, tolerance)
	response.ReturnedPoints = len(keep)
	response.ReductionPercent = math.Round((1-float64(len(keep))/float64(len(positions)))*10000) / 100

	geocodeAll := s.geocodeMaxPoints()
	out := make([]models.PlaybackPoint, 0, len(keep))
	for idx, posIdx := range keep {
		pos := positions[posIdx]
		point := models.PlaybackPoint{
			Timestamp: pos.Timestamp,
			Lat:       pos.Lat,
			Lon:       pos.Lon,
			Speed:     pos.Speed,
			Heading:   pos.Heading,
			Battery:   pos.Battery,
			ACC:       pos.ACC,
		}
		// The endpoints always carry an address (start/end of the route); the
		// intermediate points are geocoded while the cap allows it (B7.3).
		if idx == 0 || idx == len(keep)-1 || len(keep) <= geocodeAll {
			if addr, resolved := s.geocoder.Reverse(c.Request.Context(), pos.Lat, pos.Lon); resolved {
				point.Address = addr.String()
				point.City = addr.City
				point.Province = addr.Province
			}
		}
		out = append(out, point)
	}
	response.Points = out
	return response
}

// parsePlaybackTolerance validates the optional `tolerance_m` query parameter.
func (s *Service) parsePlaybackTolerance(c *gin.Context) (float64, *APIError) {
	raw := strings.TrimSpace(c.Query("tolerance_m"))
	if raw == "" {
		return s.settings.PlaybackToleranceM, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, errValidation("tolerance_m must be a non-negative number",
			map[string]string{"tolerance_m": "invalid"})
	}
	if max := s.settings.PlaybackMaxToleranceM; max > 0 && value > max {
		return 0, errValidation(fmt.Sprintf("tolerance_m must be <= %g", max),
			map[string]string{"tolerance_m": "too large"})
	}
	return value, nil
}

// playbackMaxPoints resolves the response cap (settings, with a safe default).
func (s *Service) playbackMaxPoints() int {
	if s.settings.PlaybackMaxPoints > 0 {
		return s.settings.PlaybackMaxPoints
	}
	return 20000
}

// handleReverseGeocode implements `GET /api/v1/geocode/reverse?lat=&lon=`
// (B7.3): the single-coordinate entry point of the offline resolver (map click,
// alert detail, ...). It never fails on an unresolvable coordinate — the response
// carries `resolved=false` instead.
func (s *Service) handleReverseGeocode(c *gin.Context) {
	if _, ok := currentIdentity(c); !ok {
		respondError(c, errUnauthorized("unauthenticated"))
		return
	}
	if s.geocoder == nil {
		respondError(c, errUnavailable("geocoding is not available"))
		return
	}
	lat, apiErr := parseCoordinate(c, "lat", 90)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	lon, apiErr := parseCoordinate(c, "lon", 180)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	start := time.Now()
	addr, resolved := s.geocoder.Reverse(c.Request.Context(), lat, lon)
	if !resolved {
		slog.Debug("service-websocket: reverse geocode unresolved",
			"lat", lat, "lon", lon, "request_id", requestID(c))
	}
	observeRBAC(start)
	respondOK(c, models.ReverseGeocodeResponse{
		Lat: lat, Lon: lon,
		Address: Payload(addr, resolved),
	}, nil)
}

// parseCoordinate validates one `lat`/`lon` query parameter.
func parseCoordinate(c *gin.Context, name string, bound float64) (float64, *APIError) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0, errValidation(name+" is required", map[string]string{name: "required"})
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < -bound || value > bound {
		return 0, errValidation(fmt.Sprintf("%s must be a number in [-%g, %g]", name, bound, bound),
			map[string]string{name: "invalid"})
	}
	return value, nil
}

// geocodeMaxPoints resolves how many reduced points receive an address.
func (s *Service) geocodeMaxPoints() int {
	if s.settings.GeocodeMaxPoints > 0 {
		return s.settings.GeocodeMaxPoints
	}
	return 500
}
