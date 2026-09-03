package controllers

// store_db_test.go (B4 coverage 2026-08-31): menguji implementasi companyStore
// repos.go / store_geofence.go / store_notify_sos.go / store_routes.go memakai
// go-sqlmock — menutup jalur SELECT/INSERT/UPDATE tanpa butuh infra DB nyata.

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"ajb_gps/internal/dialect"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockStore(t *testing.T) (*companyStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &companyStore{code: "DEV001", db: db, ro: tenant.NewSingleRouter(db)}, mock
}

// ---------------------------------------------------------------------------
// repos.go — VehicleByIMEI
// ---------------------------------------------------------------------------

func TestVehicleByIMEI_Found(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT id, COALESCE").WithArgs("864201040512345").
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(7, "active"))
	id, status, err := s.VehicleByIMEI("864201040512345")
	if err != nil || id != 7 || status != "active" {
		t.Fatalf("VehicleByIMEI = (%d,%q,%v)", id, status, err)
	}
}

func TestVehicleByIMEI_NoRowsUnknown(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT id, COALESCE").WithArgs("1").
		WillReturnError(sql.ErrNoRows)
	_, _, err := s.VehicleByIMEI("1")
	if !errors.Is(err, ErrUnknownVehicle) {
		t.Fatalf("expected ErrUnknownVehicle, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// repos.go — InsertAlert (dialect-aware RETURNING vs LastInsertId)
// ---------------------------------------------------------------------------

func TestInsertAlert_PostgresReturning(t *testing.T) {
	dialect.Set(dialect.Postgres)
	s, mock := newMockStore(t)
	// Input severity 'warning' dinormalisasi → 'medium' utk enum alerts.
	mock.ExpectQuery("INSERT INTO alerts").WithArgs(7, "SOS", "medium", "desc", "open", nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	id, err := s.InsertAlert(models.AlertRecord{VehicleID: 7, AlertType: "SOS", Severity: "warning", Description: "desc", Status: "open"})
	if err != nil || id != 42 {
		t.Fatalf("InsertAlert = (%d,%v)", id, err)
	}
}

func TestInsertAlert_MySQLLastInsertID(t *testing.T) {
	dialect.Set(dialect.MySQL)
	defer dialect.Set(dialect.Postgres)
	s, mock := newMockStore(t)
	// Input severity 'warning' dinormalisasi → 'medium' utk enum alerts.
	mock.ExpectExec("INSERT INTO alerts").WithArgs(7, "SOS", "medium", "desc", "open", nil, nil).
		WillReturnResult(sqlmock.NewResult(42, 1))
	id, err := s.InsertAlert(models.AlertRecord{VehicleID: 7, AlertType: "SOS", Severity: "warning", Description: "desc", Status: "open"})
	if err != nil || id != 42 {
		t.Fatalf("InsertAlert mysql = (%d,%v)", id, err)
	}
}

// ---------------------------------------------------------------------------
// repos.go — FuelConfigFor / HasOpenAlert / SpeedConfigFor
// ---------------------------------------------------------------------------

func TestFuelConfigFor_VehicleSpecificWins(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT vehicle_id, drop_threshold").
		WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id", "drop_threshold", "refuel_threshold", "window_seconds", "enabled"}).
			AddRow(7, 15, 10, 300, true))
	cfg, ok, err := s.FuelConfigFor(7)
	if err != nil || !ok || cfg.VehicleID == nil || *cfg.VehicleID != 7 || cfg.DropThreshold != 15 {
		t.Fatalf("FuelConfigFor = %+v ok=%v err=%v", cfg, ok, err)
	}
}

func TestFuelConfigFor_None(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT vehicle_id, drop_threshold").WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id", "drop_threshold", "refuel_threshold", "window_seconds", "enabled"}))
	_, ok, err := s.FuelConfigFor(7)
	if err != nil || ok {
		t.Fatalf("expected no config, got ok=%v err=%v", ok, err)
	}
}

func TestHasOpenAlert(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT COUNT").WithArgs(7, "OFFLINE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	open, err := s.HasOpenAlert(7, "OFFLINE")
	if err != nil || !open {
		t.Fatalf("HasOpenAlert = (%v,%v), want (true,nil)", open, err)
	}
}

func TestSpeedConfigFor_Global(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT vehicle_id, speed_limit_kmh").WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id", "speed_limit_kmh", "grace_margin"}).
			AddRow(nil, 80, 10))
	cfg, ok, err := s.SpeedConfigFor(7)
	if err != nil || !ok || cfg.VehicleID != nil || cfg.SpeedLimitKMH != 80 || cfg.GraceMargin != 10 {
		t.Fatalf("SpeedConfigFor = %+v ok=%v err=%v", cfg, ok, err)
	}
}
// ---------------------------------------------------------------------------
// store_geofence.go — ActiveGeofences
// ---------------------------------------------------------------------------

func TestActiveGeofences_Rows(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT g.id, g.name, g.area_type").
		WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "area_type", "coordinates", "radius_meters", "boundary_points"}).
			AddRow(1, "Depot", "circle", `{"type":"Point","coordinates":[106.8456,-6.2088]}`, 800, "[]"))
	defs, err := s.ActiveGeofences(7)
	if err != nil || len(defs) != 1 {
		t.Fatalf("ActiveGeofences = %+v err=%v", defs, err)
	}
	if defs[0].CenterLat != -6.2088 || defs[0].RadiusMeters != 800 {
		t.Fatalf("circle parsed wrong: %+v", defs[0])
	}
}

func TestActiveGeofences_Empty(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT g.id, g.name, g.area_type").WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "area_type", "coordinates", "radius_meters", "boundary_points"}))
	defs, err := s.ActiveGeofences(7)
	if err != nil || len(defs) != 0 {
		t.Fatalf("ActiveGeofences empty = %+v err=%v", defs, err)
	}
}

// ---------------------------------------------------------------------------
// store_notify_sos.go
// ---------------------------------------------------------------------------

func TestEnabledPreferences_EmptyInput(t *testing.T) {
	s, _ := newMockStore(t)
	out, err := s.EnabledPreferences(nil)
	if err != nil || out != nil {
		t.Fatalf("empty input must return nil,nil got %+v,%v", out, err)
	}
}

func TestEnabledPreferences_Rows(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT user_id, alert_type, channel").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "alert_type", "channel", "min_severity", "delivery_config"}).
			AddRow(1, "speed", "websocket", "info", "{}"))
	prefs, err := s.EnabledPreferences([]string{"speed"})
	if err != nil || len(prefs) != 1 || prefs[0].UserID != 1 || prefs[0].Channel != "websocket" {
		t.Fatalf("EnabledPreferences = %+v err=%v", prefs, err)
	}
}

func TestVehicleUserIDs(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT user_id FROM user_vehicles").WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(1).AddRow(2))
	ids, err := s.VehicleUserIDs(7)
	if err != nil || len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("VehicleUserIDs = %v err=%v", ids, err)
	}
}

func TestRecordNotification(t *testing.T) {
	s, mock := newMockStore(t)
	// 9 placeholder: user_id, alert_id, company_code, channel, alert_type, subject, body, status, error_message.
	mock.ExpectExec("INSERT INTO notifications").
		WithArgs(1, 9, "DEV001", "websocket", "speed", "subject", "body", "sent", nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := s.RecordNotification(1, 9, "websocket", "speed", "subject", "body", "sent", ""); err != nil {
		t.Fatalf("RecordNotification: %v", err)
	}
}

func TestNullIfEmptyAndDeliveryExpr(t *testing.T) {
	if v := nullIfEmpty(""); v != nil {
		t.Fatalf("nullIfEmpty('') = %v", v)
	}
	if v := nullIfEmpty("x"); v != "x" {
		t.Fatalf("nullIfEmpty('x') = %v", v)
	}
	if deliveryDefaultExpr(dialect.Postgres) != `'{}'::jsonb` {
		t.Fatalf("pg default = %q", deliveryDefaultExpr(dialect.Postgres))
	}
	if deliveryDefaultExpr(dialect.MySQL) != `JSON_OBJECT()` {
		t.Fatalf("mysql default = %q", deliveryDefaultExpr(dialect.MySQL))
	}
}

func TestOpenSOSOlderThan(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT id, vehicle_id, created_at").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "vehicle_id", "created_at", "acknowledged_at"}).
			AddRow(11, 7, time.Now().Add(-time.Hour), nil))
	list, err := s.OpenSOSOlderThan(time.Now().Add(-time.Minute))
	if err != nil || len(list) != 1 || list[0].ID != 11 {
		t.Fatalf("OpenSOSOlderThan = %+v err=%v", list, err)
	}
}

func TestGetAlert(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT id, vehicle_id, alert_type").
		WithArgs(5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "vehicle_id", "alert_type", "severity", "description", "status", "vehicle_lat", "vehicle_lon", "created_at"}).
			AddRow(5, 7, "SOS", "critical", "desc", "open", -6.2, 106.8, time.Now()))
	a, err := s.GetAlert(5)
	if err != nil || a.AlertType != "SOS" || a.VehicleLat == nil || *a.VehicleLat != -6.2 {
		t.Fatalf("GetAlert = %+v err=%v", a, err)
	}
}

// ---------------------------------------------------------------------------
// store_routes.go
// ---------------------------------------------------------------------------

func TestLoadActiveAssignments(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery("SELECT ra.id, ra.route_id").
		WillReturnRows(sqlmock.NewRows([]string{"ra.id", "ra.route_id", "r.name", "ra.vehicle_id", "v.imei", "ra.driver_user_id", "ra.status", "ra.started_at", "r.waypoints", "r.estimated_duration_sec"}).
			AddRow(3, 1, "Rute-1", 7, "864201040512345", 4, "in_progress", time.Now(), `[{"lat":-6.2,"lon":106.8}]`, 1800))
	list, err := s.LoadActiveAssignments()
	if err != nil || len(list) != 1 || list[0].RouteName != "Rute-1" || len(list[0].Waypoints) != 1 {
		t.Fatalf("LoadActiveAssignments = %+v err=%v", list, err)
	}
}

func TestUpdateAssignmentStatus_Branches(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec("UPDATE route_assignments SET status='in_progress'").
		WithArgs(1).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateAssignmentStatus(1, "in_progress"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE route_assignments SET status='completed'").
		WithArgs(2).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateAssignmentStatus(2, "completed"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE route_assignments SET status=\\?").
		WithArgs("delayed", 3).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateAssignmentStatus(3, "delayed"); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAssignmentDeviation(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec("UPDATE route_assignments SET deviation_meters").
		WithArgs(150.0, 1).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateAssignmentDeviation(1, 150.0); err != nil {
		t.Fatal(err)
	}
}