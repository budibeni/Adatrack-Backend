package controllers

// route_handler_test.go (B4 coverage 2026-08-31): checkRoute (state machine
// not_started→in_progress→delayed/completed + deviation), reloadRoutes,
// handleTelemetry (tenant resolution → checks), publishAlert nil-safe.

import (
	"testing"

	"ajb_gps/worker-alert/models"
)

func TestCheckRoute_NotStartedToInProgressAndDeviation(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	wps := []models.Waypoint{{Lat: -6.2000, Lon: 106.8000}, {Lat: -6.1000, Lon: 106.9000}}
	ra := &models.RouteAssignment{
		AssignmentID: 1, RouteID: 1, RouteName: "Rute-A", VehicleID: 7,
		IMEI: "1", Status: "not_started", Waypoints: wps,
	}
	wa.routesMu.Lock()
	wa.routes["DEV001|1"] = ra
	wa.routesMu.Unlock()

	// Gerakan pertama di dekat waypoint pertama (dalam 200m) → in_progress, no deviation.
	wa.checkRoute(f, "DEV001", tm("1", nil, 40, -6.2010, 106.8010, 0), 7)
	if ra.Status != "in_progress" {
		t.Fatalf("route status setelah gerak = %q, want in_progress", ra.Status)
	}
	if len(f.inserts) != 0 {
		t.Fatalf("no deviation expected yet, got %d", len(f.inserts))
	}

	// Posisi jauh dari waypoint → ROUTE_DEVIATION.
	wa.checkRoute(f, "DEV001", tm("1", nil, 40, -8.0000, 110.0000, 0), 7)
	if len(f.inserts) != 1 || f.inserts[0].AlertType != models.AlertTypeRouteDev {
		t.Fatalf("expected ROUTE_DEVIATION, got %+v", f.inserts)
	}

	// Dalam threshold waypoint terakhir → completed.
	wa.checkRoute(f, "DEV001", tm("1", nil, 5, -6.1010, 106.9010, 0), 7)
	if ra.Status != "completed" {
		t.Fatalf("route status = %q, want completed", ra.Status)
	}
}

func TestCheckRoute_NoLatLonNoOp(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	wa.checkRoute(f, "DEV001", tm("1", nil, 0, 0, 0, 0), 7) // no position
	if len(f.inserts) != 0 {
		t.Fatalf("no position must not trigger route, got %d", len(f.inserts))
	}
}

func TestCheckRoute_NotAssignedNoOp(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	wa.checkRoute(f, "DEV001", tm("unassigned", nil, 40, -6.18, 106.78, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("unassigned vehicle must not trigger route, got %d", len(f.inserts))
	}
}

func TestReloadRoutes_NoCompanies(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.reloadRoutes() // tenant manager zero-value → Companies() kosong → aman
}

func TestPublishAlert_NilNATSIsSafe(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.nac = nil
	rec := models.AlertRecord{ID: 1, VehicleID: 7, AlertType: models.AlertTypeSOS, Severity: "critical", Status: "open"}
	wa.publishAlert("DEV001", "1", rec, tm("1", nil, 0, -6.2, 106.8, 0)) // nac nil guarded
}

func TestPublishCaptureRequest_NilNATSIsSafe(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.nac = nil
	rec := models.AlertRecord{ID: 1, VehicleID: 7, AlertType: models.AlertTypeSOS, Severity: "critical", Status: "open"}
	wa.publishCaptureRequest("DEV001", "1", rec, tm("1", nil, 0, 0, 0, 0))
}

func TestHandleTelemetry_UnknownCompanyStore(t *testing.T) {
	wa, _ := newTestWA(t)
	msg := &natsMsg{Data: []byte(`{"imei":"999999999999999","company_code":"NOPE22"}`)}
	wa.handleTelemetry(msg) // newStore("NOPE22") → error → log+metric, tidak panic
}

func TestHandleTelemetry_InvalidJSON(t *testing.T) {
	wa, _ := newTestWA(t)
	msg := &natsMsg{Subject: "telemetry.raw.123", Data: []byte(`not json`)}
	wa.handleTelemetry(msg) // publishError memakai wa.nac=nil → safe
}

func TestHandleTelemetry_EmptyIMEIDropped(t *testing.T) {
	wa, _ := newTestWA(t)
	msg := &natsMsg{Data: []byte(`{"company_code":"DEV001"}`)}
	wa.handleTelemetry(msg) // IMEI kosong → return nil langsung
}