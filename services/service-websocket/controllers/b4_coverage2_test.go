package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// B4 websocket (batch 2) — notify/media bridge, routes, reference cache, base.
// ---------------------------------------------------------------------------

func TestNotifyHandleFansOut(t *testing.T) {
	vehReg = newVehicleRegistry()
	appHub = newHub(100, 100)
	cl, _ := mkClient(t, appHub, "DEV001", true)

	payload := []byte(`{"alert_id":"55","vehicle_id":42,"imei":"864201040512345",
		"company_code":"DEV001","alert_type":"SOS","severity":"critical","status":"OPEN",
		"lat":-6.2,"lon":106.8,"triggered_at":1722000000}`)
	if err := notifyHandle(&nats.Msg{Subject: "notify.alert.42", Data: payload}); err != nil {
		t.Fatalf("notifyHandle: %v", err)
	}

	var ev models.AlertNotificationEvent
	if err := json.Unmarshal(awaitPayload(t, cl), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "ALERT_NOTIFICATION" || ev.Data.AlertID != "55" || ev.Data.VehicleID != 42 {
		t.Errorf("unexpected notify event: %+v", ev)
	}
}

func TestNotifyHandleGuards(t *testing.T) {
	vehReg = newVehicleRegistry()
	appHub = newHub(100, 10)
	cl, _ := mkClient(t, appHub, "DEV001", true)

	// Subject vehicle_id tidak valid → skip tanpa broadcast.
	if err := notifyHandle(&nats.Msg{Subject: "notify.alert.abc", Data: []byte(`{}`)}); err != nil {
		t.Fatalf("invalid vehicle id: %v", err)
	}
	if !noPayload(cl) {
		t.Fatal("no broadcast expected")
	}
	// Payload rusak → skip.
	if err := notifyHandle(&nats.Msg{Subject: "notify.alert.42", Data: []byte(`{bad`)}); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	if !noPayload(cl) {
		t.Fatal("no broadcast expected for bad payload")
	}
	// Tenant lain tidak menerima.
	other, _ := mkClient(t, appHub, "QA001", true)
	if err := notifyHandle(&nats.Msg{Subject: "notify.alert.42", Data: []byte(`{"company_code":"DEV001","vehicle_id":42,"triggered_at":1}`)}); err != nil {
		t.Fatalf("notifyHandle: %v", err)
	}
	if !noPayload(other) {
		t.Fatal("cross-tenant notify leaked")
	}
}

func TestMediaHandleFanoutAndGuards(t *testing.T) {
	vehReg = newVehicleRegistry()
	appHub = newHub(100, 10)
	cl, _ := mkClient(t, appHub, "DEV001", true)

	if err := mediaHandle(&nats.Msg{Subject: "media.event.DEV001", Data: []byte(`{"media_id":7,"vehicle_id":42,"imei":"864000000000000","company_code":"DEV001","media_type":"image/jpeg","trigger_type":"sos","status":"available","taken_at":1722000000,"published_at":1722000001,"size_bytes":1024}`)}); err != nil {
		t.Fatalf("mediaHandle: %v", err)
	}
	var ev models.MediaEventWS
	if err := json.Unmarshal(awaitPayload(t, cl), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "MEDIA_EVENT" || ev.Data.MediaID != 7 || ev.Data.MediaType != "image/jpeg" {
		t.Errorf("unexpected media event: %+v", ev)
	}

	// Missing company_code → skip.
	if err := mediaHandle(&nats.Msg{Subject: "media.event.DEV001", Data: []byte(`{"media_id":8,"vehicle_id":42,"taken_at":1}`)}); err != nil {
		t.Fatalf("mediaHandle guard: %v", err)
	}
	// Payload rusak → skip.
	if err := mediaHandle(&nats.Msg{Subject: "media.event.DEV001", Data: []byte(`{bad`)}); err != nil {
		t.Fatalf("mediaHandle bad payload: %v", err)
	}
	if !noPayload(cl) {
		t.Fatal("unexpected extra media broadcasts")
	}
}

// ---------------------------------------------------------------------------
// reference cache + base helpers
// ---------------------------------------------------------------------------

func TestRefCacheKey(t *testing.T) {
	if got := refCacheKey("countries", "indo"); got != "adatrack_gps:ref:countries:indo" {
		t.Errorf("refCacheKey = %s", got)
	}
}

func TestRefCacheWithNilAndMiniredis(t *testing.T) {
	// appRedis nil → miss, set no-op.
	old := appRedis
	appRedis = nil
	defer func() { appRedis = old }()

	ctx := context.Background()
	if _, ok := refGetCache(ctx, "k"); ok {
		t.Fatal("nil redis should miss")
	}
	refSetCache(ctx, "k", []byte("x")) // no panic

	// miniredis-backed client.
	mr := miniredis.RunT(t)
	cfg := &internal.Config{}
	cfg.Redis.Addr = mr.Addr()
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient(miniredis): %v", err)
	}
	appRedis = rc
	refSetCache(ctx, "countries:ID", []byte(`[{"id":1}]`))
	if got, ok := refGetCache(ctx, "countries:ID"); !ok || !strings.Contains(string(got), `"id":1`) {
		t.Errorf("ref cache roundtrip = %s, %v", got, ok)
	}
	if _, ok := refGetCache(ctx, "miss"); ok {
		t.Error("unexpected cache hit")
	}
}

// ---------------------------------------------------------------------------
// handlers_routes.go — helpers (sqlmock)
// ---------------------------------------------------------------------------

func TestNullableIntPtr(t *testing.T) {
	if got := nullableIntPtr(sql.NullInt64{Valid: false}); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if got := nullableIntPtr(sql.NullInt64{Int64: 12, Valid: true}); got == nil || *got != 12 {
		t.Errorf("expected 12, got %v", got)
	}
}

func TestFetchRouteByID(t *testing.T) {
	db, m := mockDB(t)
	now := time.Now()
	m.ExpectQuery(`SELECT id, name, waypoints`).WithArgs(uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "waypoints", "estimated_duration_sec", "created_by", "is_active", "created_at", "updated_at"}).
			AddRow(uint64(3), "Rute A", []byte(`[{"lat":-6.2,"lon":106.8}]`), int64(120), uint64(1), true, now, now))
	r, err := fetchRouteByID(db, 3)
	if err != nil || r.ID != 3 || r.Name != "Rute A" {
		t.Errorf("fetchRouteByID = %+v, %v", r, err)
	}

	m.ExpectQuery(`SELECT id, name, waypoints`).WillReturnError(sql.ErrNoRows)
	if _, err := fetchRouteByID(db, 99); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestLoadAssignments(t *testing.T) {
	db, m := mockDB(t)
	m.ExpectQuery(`SELECT ra.id, ra.route_id`).WithArgs(uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{
			"ra.id", "ra.route_id", "ra.vehicle_id", "ra.driver_user_id", "ra.status",
			"ra.started_at", "ra.completed_at", "ra.deviation_meters", "imei",
		}).AddRow(uint64(1), uint64(3), uint64(42), uint64(9), "in_progress",
			time.Now(), nil, 12.5, "864000000041234"))
	items := loadAssignments(db, 3)
	if len(items) != 1 || items[0].VehicleID != 42 || items[0].Status != "in_progress" || items[0].DeviationMeters != 12.5 {
		t.Errorf("loadAssignments = %+v", items)
	}

	db2, m2 := mockDB(t)
	m2.ExpectQuery(`SELECT ra\.id, ra\.route_id`).WillReturnError(errors.New("boom"))
	out := loadAssignments(db2, 1)
	if out == nil || len(out) != 0 {
		t.Errorf("expected empty slice, got %#v", out)
	}
}

func TestRouteToItem(t *testing.T) {
	now := time.Now()
	r := &routeRow{ID: 5, Name: "R", Waypoints: []byte(`[{"lat":-6.2,"lon":106.8}]`),
		EstimatedDurationSec: sql.NullInt64{Int64: 300, Valid: true}, CreatedBy: 1,
		IsActive: true, CreatedAt: now, UpdatedAt: now}
	item := routeToItem(nil, r)
	if item.ID != 5 || item.EstimatedDurationSec == nil || *item.EstimatedDurationSec != 300 {
		t.Errorf("routeToItem mismatch: %+v", item)
	}
	if len(item.Waypoints) != 1 {
		t.Errorf("expected 1 waypoint, got %+v", item.Waypoints)
	}

	r2 := &routeRow{ID: 6, Name: "X", CreatedAt: now, UpdatedAt: now}
	item2 := routeToItem(nil, r2)
	if item2.Waypoints == nil {
		t.Error("expected non-nil waypoints")
	}
}

func TestRouteAccessible(t *testing.T) {
	c, _ := ginCtx(http.MethodGet, "/api/v1/routes/5")
	c.Set(ctxAdminKey, true)
	if !routeAccessible(c, nil, &routeRow{}) {
		t.Error("admin sees all routes")
	}

	c2, _ := ginCtx(http.MethodGet, "/api/v1/routes/5")
	c2.Set(ctxAdminKey, false)
	c2.Set(ctxUserKey, models.AuthUser{ID: 7, CompanyCode: "DEV001", Role: "Operator", CompanyUserID: 9})
	c2.Set(ctxAllowedKey, map[uint64]struct{}{42: {}})
	db, m := mockDB(t)
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM route_assignments`).
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	if !routeAccessible(c2, db, &routeRow{ID: 5}) {
		t.Error("expected accessible via allowed vehicle")
	}

	db2, m2 := mockDB(t)
	m2.ExpectQuery(`SELECT COUNT\(\*\) FROM route_assignments`).WillReturnError(errors.New("boom"))
	if routeAccessible(c2, db2, &routeRow{ID: 5}) {
		t.Error("expected denied on DB error")
	}
}
