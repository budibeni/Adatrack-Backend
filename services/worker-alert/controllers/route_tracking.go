package controllers

import (
	"fmt"
	"log/slog"
	"time"

	"ajb_gps/worker-alert/models"
)

// routeDeviationThreshold is the max allowed distance (meters) from the
// planned waypoint polyline before ROUTE_DEVIATION fires
// (env ROUTE_DEVIATION_THRESHOLD_M, default 200 m).
func (wa *WorkerAlert) routeDeviationThreshold() float64 {
	if v := envFloat("ROUTE_DEVIATION_THRESHOLD_M", 200); v > 0 {
		return v
	}
	return 200
}

// refreshRoutes periodically reloads active assignments per company into the
// in-memory tracking map keyed by "<company>|<imei>".
func (wa *WorkerAlert) refreshRoutes() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-wa.ctx.Done():
			return
		case <-tick.C:
			wa.reloadRoutes()
		}
	}
}

// reloadRoutes rebuilds the tracking map from every active tenant.
func (wa *WorkerAlert) reloadRoutes() {
	next := make(map[string]*models.RouteAssignment)
	for _, c := range companiesFn(wa.tm) {
		if !c.IsActive {
			continue
		}
		st, err := newStoreFn(wa, c.Code)
		if err != nil {
			continue
		}
		list, err := st.LoadActiveAssignments()
		if err != nil {
			slog.Error("load route assignments failed", "company", c.Code, "error", err)
			wa.metrics.incError(c.Code, "routes_load")
			continue
		}
		for i := range list {
			next[c.Code+"|"+list[i].IMEI] = &list[i]
		}
	}
	wa.routesMu.Lock()
	wa.routes = next
	wa.routesMu.Unlock()
}

// checkRoute advances the assignment state machine for one telemetry sample.
func (wa *WorkerAlert) checkRoute(s store, company string, tm models.TelemetryMessage, vehicleID uint64) {
	if tm.Lat == 0 && tm.Lon == 0 {
		return
	}
	key := company + "|" + tm.IMEI
	wa.routesMu.RLock()
	ra, ok := wa.routes[key]
	wa.routesMu.RUnlock()
	if !ok || len(ra.Waypoints) == 0 {
		return
	}

	threshold := wa.routeDeviationThreshold()

	// 1) not_started → in_progress pada gerakan pertama.
	if ra.Status == "not_started" && tm.Speed > 0 {
		if err := s.UpdateAssignmentStatus(ra.AssignmentID, "in_progress"); err != nil {
			slog.Error("route start update failed", "company", company, "assignment_id", ra.AssignmentID, "error", err)
			wa.metrics.incError(company, "routes_update")
		} else {
			now := time.Now()
			ra.Status = "in_progress"
			ra.StartedAt = &now
			slog.Info("route in progress", "company", company, "assignment_id", ra.AssignmentID, "imei", tm.IMEI)
		}
	}

	// 2) Deviation detection vs nearest waypoint.
	dist, _ := nearestDistance(tm.Lat, tm.Lon, ra.Waypoints)
	outside := dist > threshold
	devKey := key + "#" + fmt.Sprint(ra.AssignmentID)
	wa.routesMu.Lock()
	wasDeviating := wa.deviating[devKey]
	wa.deviating[devKey] = outside
	wa.routesMu.Unlock()

	if outside && !wasDeviating {
		if err := s.UpdateAssignmentDeviation(ra.AssignmentID, dist); err != nil {
			slog.Error("route deviation update failed", "company", company, "error", err)
		}
		rec := models.AlertRecord{
			VehicleID:   vehicleID,
			AlertType:   models.AlertTypeRouteDev,
			Severity:    "high",
			Description: fmt.Sprintf("Route '%s' deviation: %.0f m off course (limit %.0f m)", ra.RouteName, dist, threshold),
			Status:      models.AlertStatusOpen,
		}
		attachPos(&rec, tm)
		if id, err := s.InsertAlert(rec); err != nil {
			slog.Error("route deviation alert insert failed", "company", company, "error", err)
			wa.metrics.incError(company, "insert")
		} else {
			rec.ID = id
			wa.metrics.incCreated(company, rec.AlertType, normalizeSeverity("high"))
			wa.publishAlert(company, tm.IMEI, rec, tm)
			wa.notifyAlert(s, company, tm, rec)
			slog.Warn("route deviation alert created", "company", company, "alert_id", id,
				"imei", tm.IMEI, "distance_m", dist)
		}
	}

	// 3) Delayed ketika melebihi estimasi durasi.
	if ra.Status == "in_progress" && ra.EstimatedSec != nil && ra.StartedAt != nil &&
		time.Since(*ra.StartedAt) > time.Duration(*ra.EstimatedSec)*time.Second {
		if err := s.UpdateAssignmentStatus(ra.AssignmentID, "delayed"); err != nil {
			slog.Error("route delay update failed", "company", company, "error", err)
		} else {
			ra.Status = "delayed"
			slog.Warn("route delayed", "company", company, "assignment_id", ra.AssignmentID, "imei", tm.IMEI)
		}
	}

	// 4) Completed ketika mencapai waypoint terakhir dalam threshold.
	last := ra.Waypoints[len(ra.Waypoints)-1]
	if (ra.Status == "in_progress" || ra.Status == "delayed") &&
		haversineMeters(tm.Lat, tm.Lon, last.Lat, last.Lon) <= threshold {
		if err := s.UpdateAssignmentStatus(ra.AssignmentID, "completed"); err != nil {
			slog.Error("route complete update failed", "company", company, "error", err)
		} else {
			ra.Status = "completed"
			slog.Info("route completed", "company", company, "assignment_id", ra.AssignmentID, "imei", tm.IMEI)
		}
	}
}

// nearestDistance returns the minimum distance (m) to any waypoint + index.
func nearestDistance(lat, lon float64, wps []models.Waypoint) (float64, int) {
	minD := -1.0
	idx := -1
	for i, w := range wps {
		d := haversineMeters(lat, lon, w.Lat, w.Lon)
		if idx < 0 || d < minD {
			minD, idx = d, i
		}
	}
	return minD, idx
}
