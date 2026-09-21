package controllers

// store_pg_it_test.go — integration coverage for the worker-alert PostgresStore
// and the live publish/notify pipeline (PRD §5.9). Opt-in via ADATRACK_IT=1
// (mirrors worker-persistence / api-vehicle): live PostgreSQL (127.0.0.1:5533,
// tenant DEV001) + live NATS (127.0.0.1:4222).
//
// Fixtures carry unique it-alert-<pid> markers and are removed in t.Cleanup, so
// the shared dev dataset is left untouched.

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/worker-alert/models"
)

// itMarker is the unique fixture tag of this process (dedup keys, IMEIs, emails).
var itMarker = "it-alert-" + strconv.Itoa(os.Getpid())

// itCompany is the provider tenant seeded by database/seed + provision-tenant.sh.
const itCompany = "DEV001"

// itStoreHarness wires a real PostgresStore + live Redis/NATS clients on the
// DEV001 tenant; the tenant manager and clients close in t.Cleanup.
type itStoreHarness struct {
	t     *testing.T
	ctx   context.Context
	store *PostgresStore
	tm    *tenant.Manager
	cfg   *internal.Config
	red   *internal.RedisClient
	nats  *internal.NATSClient
}

// envOr reads an env override or the documented dev default.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// skipNoDB gates the IT suite behind ADATRACK_IT=1.
func skipNoDB(t *testing.T) {
	t.Helper()
	if v, ok := os.LookupEnv("ADATRACK_IT"); ok && v == "1" {
		return
	}
	t.Skip("integration test — set ADATRACK_IT=1 with live PostgreSQL/NATS (127.0.0.1:5533/4222)")
}

func newITStoreHarness(t *testing.T) *itStoreHarness {
	t.Helper()
	skipNoDB(t)

	internal.LoadProjectEnv()
	t.Setenv("POSTGRES_HOST", envOr("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", envOr("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", envOr("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", envOr("ADATRACK_IT_REDIS_PORT", "6380"))
	t.Setenv("NATS_URL", envOr("ADATRACK_IT_NATS_URL", "nats://127.0.0.1:4222"))

	cfg := internal.LoadConfig()
	cfg.Alert.DedupWindow = time.Minute
	cfg.Alert.NotifyRateLimitPerMin = 0 // no rate limit unless a test sets it
	cfg.Fuel.Severity = models.SeverityHigh

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tm, err := tenant.New(ctx, cfg, tenant.ConfigFromEnv(cfg), nil, nil)
	if err != nil {
		t.Fatalf("tenant manager unavailable (is compose up + migrated?): %v", err)
	}
	if _, err := tm.DB(itCompany); err != nil {
		t.Fatalf("tenant %s not provisioned: %v", itCompany, err)
	}

	h := &itStoreHarness{t: t, ctx: context.Background(), tm: tm, cfg: cfg}
	h.store = NewPostgresStore(tm)
	h.red, err = internal.NewRedisClient(cfg)
	if err != nil {
		t.Fatalf("redis unavailable: %v", err)
	}
	h.nats, err = internal.NewNATSClient(cfg)
	if err != nil {
		t.Fatalf("nats unavailable: %v", err)
	}
	t.Cleanup(func() {
		h.nats.Close()
		_ = h.red.Close()
		tm.Close()
	})
	return h
}

// companyDB is the DEV001 pool (direct SQL for fixtures + cleanup).
func (h *itStoreHarness) companyDB() *internal.DBPool {
	pool, err := h.tm.DB(itCompany)
	if err != nil {
		h.t.Fatalf("company pool: %v", err)
	}
	return pool
}

// exec runs SQL against the company schema and fails the test on error.
func (h *itStoreHarness) exec(q string, args ...any) {
	h.t.Helper()
	if _, err := h.companyDB().DB.ExecContext(h.ctx, q, args...); err != nil {
		h.t.Fatalf("exec %q: %v", q, err)
	}
}

// execMaster runs SQL against the master schema.
func (h *itStoreHarness) execMaster(q string, args ...any) {
	h.t.Helper()
	if _, err := h.tm.Master().DB.ExecContext(h.ctx, q, args...); err != nil {
		h.t.Fatalf("master exec %q: %v", q, err)
	}
}

// queryInt runs a scalar query.
func (h *itStoreHarness) queryInt(q string, args ...any) int64 {
	h.t.Helper()
	var n int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, q, args...).Scan(&n); err != nil {
		h.t.Fatalf("query %q: %v", q, err)
	}
	return n
}

// --- fixtures (unique-marked, cleaned up in t.Cleanup) -----------------------

// itVehicle inserts a fixture vehicle and returns its id.
func (h *itStoreHarness) itVehicle(imei string) int64 {
	h.t.Helper()
	var id int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, `
INSERT INTO tm_vehicles (imei, plate_number, status, created_by)
VALUES ($1, $2, 'active', 1) RETURNING id`, imei, itMarker).Scan(&id); err != nil {
		h.t.Fatalf("vehicle fixture: %v", err)
	}
	h.t.Cleanup(func() { h.exec(`DELETE FROM tm_vehicles WHERE id = $1`, id) })
	return id
}

// itMasterUser inserts a fixture master user and returns its id.
func (h *itStoreHarness) itMasterUser(tag string) int64 {
	h.t.Helper()
	email := itMarker + "." + tag + "@example.com"
	var id int64
	if err := h.tm.Master().DB.QueryRowContext(h.ctx, `
INSERT INTO tm_users (email, password_hash, full_name, global_role, is_active)
VALUES ($1, 'it-test-hash', $2, 'Admin', TRUE) RETURNING id`,
		email, itMarker+" "+tag).Scan(&id); err != nil {
		h.t.Fatalf("user fixture: %v", err)
	}
	h.t.Cleanup(func() { h.execMaster(`DELETE FROM tm_users WHERE id = $1`, id) })
	return id
}

// itAdminAccess grants a user the Admin override in DEV001.
func (h *itStoreHarness) itAdminAccess(userID int64) {
	h.t.Helper()
	h.exec(`INSERT INTO tm_user_company_access (user_id, role_override, is_active) VALUES ($1, 'Admin', TRUE)`, userID)
	h.t.Cleanup(func() { h.exec(`DELETE FROM tm_user_company_access WHERE user_id = $1`, userID) })
}

// itGrant gives a user the row-level grant on one vehicle.
func (h *itStoreHarness) itGrant(userID, vehicleID int64) {
	h.t.Helper()
	h.exec(`INSERT INTO tm_user_vehicles (user_id, vehicle_id) VALUES ($1, $2)`, userID, vehicleID)
	h.t.Cleanup(func() { h.exec(`DELETE FROM tm_user_vehicles WHERE user_id = $1`, userID) })
}

// itPref inserts one notification preference row.
func (h *itStoreHarness) itPref(userID int64, alertType, channel string, enabled bool, minSeverity string) {
	h.t.Helper()
	h.exec(`INSERT INTO tm_notification_preferences (user_id, alert_type, channel, enabled, min_severity)
VALUES ($1, $2, $3, $4, $5)`, userID, alertType, channel, enabled, minSeverity)
	h.t.Cleanup(func() {
		h.exec(`DELETE FROM tm_notification_preferences WHERE user_id = $1`, userID)
	})
}

// itAlert inserts a fixture alert directly and returns its id.
func (h *itStoreHarness) itAlert(dedupKey, alertType string, detectedAt time.Time) int64 {
	h.t.Helper()
	var id int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, `
INSERT INTO th_alerts (type, severity, vehicle_id, imei, company_code, status, dedup_key, detected_at)
VALUES ($1, 'high', 7, $2, $3, 'open', $4, $5) RETURNING id`,
		alertType, itMarker, itCompany, dedupKey, detectedAt).Scan(&id); err != nil {
		h.t.Fatalf("alert fixture: %v", err)
	}
	h.t.Cleanup(func() { h.exec(`DELETE FROM th_alerts WHERE dedup_key = $1`, dedupKey) })
	return id
}

// itGeofence inserts a fixture circle zone mapped to one vehicle.
func (h *itStoreHarness) itGeofence(vehicleID int64) int64 {
	h.t.Helper()
	var id int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, `
INSERT INTO tm_geofences (name, area_type, center_lat, center_lon, radius_meters, severity, on_entry, on_exit, active, created_by)
VALUES ($1, 'circle', -6.2, 106.8, 500, 'high', TRUE, TRUE, TRUE, 1) RETURNING id`,
		itMarker).Scan(&id); err != nil {
		h.t.Fatalf("geofence fixture: %v", err)
	}
	h.exec(`INSERT INTO tm_geofence_vehicles (geofence_id, vehicle_id, enabled) VALUES ($1, $2, TRUE)`, id, vehicleID)
	h.t.Cleanup(func() { h.exec(`DELETE FROM tm_geofences WHERE id = $1`, id) })
	return id
}

// itSpeedConfig inserts one enabled speed config.
func (h *itStoreHarness) itSpeedConfig(vehicleID int64, maxSpeed int) int64 {
	h.t.Helper()
	var id int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, `
INSERT INTO tm_speed_configs (vehicle_id, max_speed_kmh, grace_margin_percent, alert_severity, enabled, created_by)
VALUES (NULLIF($1, 0), $2, 0, 'high', TRUE, 1) RETURNING id`, vehicleID, maxSpeed).Scan(&id); err != nil {
		h.t.Fatalf("speed fixture: %v", err)
	}
	h.t.Cleanup(func() { h.exec(`DELETE FROM tm_speed_configs WHERE id = $1`, id) })
	return id
}

// itRouteWithAssignment inserts an in-progress assignment with waypoints.
func (h *itStoreHarness) itRouteWithAssignment(vehicleID int64) int64 {
	h.t.Helper()
	var routeID int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, `
INSERT INTO tm_routes (name, waypoints, created_by)
VALUES ($1, $2::jsonb, 1) RETURNING id`,
		itMarker, `[{"seq":1,"lat":-6.2,"lon":106.8},{"seq":2,"lat":-6.25,"lon":106.85}]`).Scan(&routeID); err != nil {
		h.t.Fatalf("route fixture: %v", err)
	}
	var id int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, `
INSERT INTO th_route_assignments (route_id, vehicle_id, status, assigned_by)
VALUES ($1, $2, 'in_progress', 1) RETURNING id`, routeID, vehicleID).Scan(&id); err != nil {
		h.t.Fatalf("assignment fixture: %v", err)
	}
	h.t.Cleanup(func() {
		h.exec(`DELETE FROM th_route_assignments WHERE id = $1`, id)
		h.exec(`DELETE FROM tm_routes WHERE id = $1`, routeID)
	})
	return id
}

// itFuelConfig inserts one enabled fuel config (vehicle_id 0 = tenant-wide).
func (h *itStoreHarness) itFuelConfig(vehicleID int64) int64 {
	h.t.Helper()
	var id int64
	if err := h.companyDB().DB.QueryRowContext(h.ctx, `
INSERT INTO tm_fuel_configs
(vehicle_id, drop_threshold_percent, refuel_threshold_percent, window_seconds, alert_severity, require_acc, acc_stale_seconds, enabled, created_by)
VALUES (NULLIF($1, 0), 10, 20, 300, 'high', FALSE, 60, TRUE, 1) RETURNING id`, vehicleID).Scan(&id); err != nil {
		h.t.Fatalf("fuel fixture: %v", err)
	}
	h.t.Cleanup(func() { h.exec(`DELETE FROM tm_fuel_configs WHERE id = $1`, id) })
	return id
}

// ---------------------------------------------------------------------------
// store read paths + identity (master + tenant)
// ---------------------------------------------------------------------------

func TestITStoreReadPaths(t *testing.T) {
	h := newITStoreHarness(t)
	ctx := h.ctx

	// Readiness + company codes.
	if err := h.store.Readiness(ctx); err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	codes, err := h.store.CompanyCodes(ctx)
	if err != nil {
		t.Fatalf("CompanyCodes: %v", err)
	}
	found := false
	for _, c := range codes {
		if c == itCompany {
			found = true
		}
	}
	if !found {
		t.Errorf("CompanyCodes = %v, want %s present", codes, itCompany)
	}

	// Master users + tenant admins + vehicle grants + preferences.
	vID := h.itVehicle(itMarker)
	u1 := h.itMasterUser("grant")
	u2 := h.itMasterUser("admin")
	h.itGrant(u1, vID)
	h.itAdminAccess(u2)

	users, err := h.store.Users(ctx, []int64{u1, u2})
	if err != nil || len(users) != 2 {
		t.Fatalf("Users = %v, %v; want both fixtures", users, err)
	}
	if users[0].Email == "" || users[0].FullName == "" {
		t.Errorf("recipient scan = %+v, want email + full name", users[0])
	}
	if got, err := h.store.Users(ctx, nil); got != nil || err != nil {
		t.Errorf("Users(nil) = %v, %v; want nil,nil", got, err)
	}

	admins, err := h.store.TenantAdminUserIDs(ctx, itCompany)
	if err != nil {
		t.Fatalf("TenantAdminUserIDs: %v", err)
	}
	if !containsID(admins, u2) {
		t.Errorf("admins %v must include the fixture admin %d", admins, u2)
	}

	grants, err := h.store.VehicleGrants(ctx, itCompany, vID)
	if err != nil || !containsID(grants, u1) {
		t.Fatalf("VehicleGrants = %v, %v; want %d", grants, err, u1)
	}

	prefs, err := h.store.Preferences(ctx, itCompany, []int64{u1})
	if err != nil || len(prefs) != 0 {
		t.Fatalf("Preferences(empty) = %v, %v; want empty", prefs, err)
	}
	h.itPref(u1, "all", models.ChannelWebsocket, true, models.SeverityLow)
	prefs, err = h.store.Preferences(ctx, itCompany, []int64{u1})
	if err != nil || len(prefs) != 1 || prefs[0].Channel != models.ChannelWebsocket {
		t.Fatalf("Preferences = %+v, %v; want the fixture row", prefs, err)
	}

	// ActiveVehicles must include the fixture vehicle.
	vehicles, err := h.store.ActiveVehicles(ctx, itCompany)
	if err != nil {
		t.Fatalf("ActiveVehicles: %v", err)
	}
	var vFound bool
	for _, v := range vehicles {
		if v.ID == vID {
			vFound = true
		}
	}
	if !vFound {
		t.Errorf("ActiveVehicles must include the fixture vehicle %d", vID)
	}

	// An unknown company must fail pool resolution (error path).
	if _, err := h.store.VehicleGrants(ctx, "NO_SUCH_TENANT_XY", vID); err == nil {
		t.Error("an unknown tenant must fail")
	}
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// alert lifecycle (insert + dedup guard, deviation, resolve, escalation)
// ---------------------------------------------------------------------------

func TestITStoreAlertLifecycle(t *testing.T) {
	h := newITStoreHarness(t)
	ctx := h.ctx
	dedup := itMarker + ":speed:7"

	// Insert → dedup guard → resolve.
	a := &models.Alert{
		Type: models.AlertOverspeeding, Severity: models.SeverityHigh,
		VehicleID: 7, IMEI: itMarker, CompanyCode: itCompany,
		Lat: -6.2, Lon: 106.8, Speed: 130,
		Status: models.StatusOpen, DedupKey: dedup,
		Metadata: map[string]any{"speed_kmh": 130.0},
	}
	inserted, err := h.store.InsertAlert(ctx, itCompany, a)
	if err != nil || !inserted {
		t.Fatalf("InsertAlert = (%v, %v); want true", inserted, err)
	}
	if a.ID <= 0 {
		t.Fatalf("alert id = %d, want the RETURNING id", a.ID)
	}

	again, err := h.store.InsertAlert(ctx, itCompany, a)
	if err != nil || again {
		t.Fatalf("duplicate InsertAlert = (%v, %v); want false (open-row guard)", again, err)
	}

	// UpdateRouteDeviation raises the stored max only when larger.
	if err := h.store.UpdateRouteDeviation(ctx, itCompany, dedup, 350); err != nil {
		t.Fatalf("UpdateRouteDeviation: %v", err)
	}
	if got := h.queryInt(`SELECT COALESCE((metadata->>'deviation_meters')::bigint, 0) FROM th_alerts WHERE dedup_key = $1`, dedup); got != 350 {
		t.Errorf("deviation = %d, want 350", got)
	}
	if err := h.store.UpdateRouteDeviation(ctx, itCompany, dedup, 100); err != nil {
		t.Fatalf("UpdateRouteDeviation(lower): %v", err)
	}
	if got := h.queryInt(`SELECT COALESCE((metadata->>'deviation_meters')::bigint, 0) FROM th_alerts WHERE dedup_key = $1`, dedup); got != 350 {
		t.Errorf("a lower deviation must not overwrite the max: %d", got)
	}

	// OpenSOAlerts picks the SOS fixture; escalation bumps its counter.
	old := time.Now().UTC().Add(-time.Hour)
	sosID := h.itAlert(itMarker+":sos:7", models.AlertSOS, old)
	open, err := h.store.OpenSOAlerts(ctx, itCompany, time.Now().UTC().Add(-time.Minute), 5)
	if err != nil {
		t.Fatalf("OpenSOAlerts: %v", err)
	}
	var found *models.Alert
	for i := range open {
		if open[i].ID == sosID {
			found = &open[i]
		}
	}
	if found == nil {
		t.Fatalf("OpenSOAlerts = %+v, want the fixture %d", open, sosID)
	}

	if err := h.store.EscalateAlert(ctx, itCompany, sosID, 1); err != nil {
		t.Fatalf("EscalateAlert: %v", err)
	}
	if got := h.queryInt(`SELECT escalation_count FROM th_alerts WHERE id = $1`, sosID); got != 1 {
		t.Errorf("escalation_count = %d, want 1", got)
	}

	// ResolveOpenAlerts resolves exactly the open rows of one identity.
	n, err := h.store.ResolveOpenAlerts(ctx, itCompany, dedup)
	if err != nil || n != 1 {
		t.Fatalf("ResolveOpenAlerts = (%d, %v); want 1", n, err)
	}
	if n, _ := h.store.ResolveOpenAlerts(ctx, itCompany, dedup); n != 0 {
		t.Errorf("a second resolve must match nothing, got %d", n)
	}
	if got := h.queryInt(`SELECT count(*) FROM th_alerts WHERE dedup_key = $1 AND status = 'resolved'`, dedup); got != 1 {
		t.Errorf("resolved row = %d, want 1", got)
	}
}

func TestITStoreInsertNotifications(t *testing.T) {
	h := newITStoreHarness(t)
	ctx := h.ctx

	dedup := itMarker + ":notify"
	alertID := h.itAlert(dedup, models.AlertOffline, time.Now().UTC())

	rows := []models.NotificationRow{
		{AlertID: alertID, UserID: 1, Channel: models.ChannelWebsocket, Status: models.NotifySent},
		{AlertID: alertID, UserID: 1, Channel: models.ChannelEmail, Status: models.NotifySkipped, Reason: "smtp_not_configured", ResponseJSON: []byte(`{"ok":true}`)},
	}
	if err := h.store.InsertNotifications(ctx, itCompany, rows); err != nil {
		t.Fatalf("InsertNotifications: %v", err)
	}
	if got := h.queryInt(`SELECT count(*) FROM td_notifications WHERE alert_id = $1`, alertID); got != 2 {
		t.Fatalf("notification rows = %d, want 2", got)
	}
	if got := h.queryInt(`SELECT count(*) FROM td_notifications WHERE alert_id = $1 AND status = 'sent' AND sent_at IS NOT NULL`, alertID); got != 1 {
		t.Errorf("sent rows must stamp sent_at, got %d", got)
	}
	if got := h.queryInt(`SELECT count(*) FROM td_notifications WHERE alert_id = $1 AND error_reason = 'smtp_not_configured'`, alertID); got != 1 {
		t.Errorf("skip reason = %d, want 1", got)
	}
	if err := h.store.InsertNotifications(ctx, itCompany, nil); err != nil {
		t.Errorf("empty rows: %v", err)
	}
}

// ---------------------------------------------------------------------------
// config loaders
// ---------------------------------------------------------------------------

func TestITStoreConfigLoaders(t *testing.T) {
	h := newITStoreHarness(t)
	ctx := h.ctx

	// Geofences (+ vehicle mapping).
	vID := h.itVehicle(itMarker)
	zoneID := h.itGeofence(vID)
	zones, err := h.store.LoadGeofences(ctx, itCompany)
	if err != nil {
		t.Fatalf("LoadGeofences: %v", err)
	}
	var zone *models.Geofence
	for i := range zones {
		if zones[i].ID == zoneID {
			zone = &zones[i]
		}
	}
	if zone == nil {
		t.Fatalf("LoadGeofences must include fixture %d, got %+v", zoneID, zones)
	}
	if zone.AreaType != "circle" || !zone.VehicleIDs[vID] {
		t.Errorf("zone = %+v, want circle mapped to %d", zone, vID)
	}

	// Speed configs (global + per-vehicle).
	gID := h.itSpeedConfig(0, 80)
	vcID := h.itSpeedConfig(vID, 60)
	speeds, err := h.store.LoadSpeedConfigs(ctx, itCompany)
	if err != nil {
		t.Fatalf("LoadSpeedConfigs: %v", err)
	}
	var gFound, vFound bool
	for _, s := range speeds {
		if s.ID == gID {
			gFound = s.VehicleID == 0 && s.MaxSpeed == 80
		}
		if s.ID == vcID {
			vFound = s.VehicleID == vID && s.MaxSpeed == 60
		}
	}
	if !gFound || !vFound {
		t.Errorf("speeds = %+v, want both fixtures scanned", speeds)
	}

	// In-progress assignments with waypoints.
	aID := h.itRouteWithAssignment(vID)
	assignments, err := h.store.LoadAssignments(ctx, itCompany)
	if err != nil {
		t.Fatalf("LoadAssignments: %v", err)
	}
	var aFound *models.Assignment
	for i := range assignments {
		if assignments[i].ID == aID {
			aFound = &assignments[i]
		}
	}
	if aFound == nil || len(aFound.Waypoints) != 2 {
		t.Fatalf("assignment = %+v, want the fixture with 2 waypoints", aFound)
	}

	// Fuel configs + the upsert path (insert then update the same row).
	h.itFuelConfig(0)
	fuel, err := h.store.FuelConfigs(ctx, itCompany)
	if err != nil {
		t.Fatalf("FuelConfigs: %v", err)
	}
	if len(fuel) == 0 {
		t.Fatal("FuelConfigs must return the fixture row")
	}

	upsert := &models.FuelConfig{VehicleID: vID, DropThresholdPct: 15, RefuelThresholdPct: 25, WindowSeconds: 120, Severity: models.SeverityHigh, RequireACC: true, ACCStaleSeconds: 30, Enabled: true}
	if err := h.store.UpsertFuelConfig(ctx, itCompany, upsert, 1); err != nil {
		t.Fatalf("UpsertFuelConfig(insert): %v", err)
	}
	upsert.DropThresholdPct = 18
	if err := h.store.UpsertFuelConfig(ctx, itCompany, upsert, 1); err != nil {
		t.Fatalf("UpsertFuelConfig(update): %v", err)
	}
	reloaded, err := h.store.FuelConfigs(ctx, itCompany)
	if err != nil {
		t.Fatalf("FuelConfigs(reload): %v", err)
	}
	var vehRow *models.FuelConfig
	for i := range reloaded {
		if reloaded[i].VehicleID == vID {
			vehRow = &reloaded[i]
		}
	}
	if vehRow == nil || vehRow.DropThresholdPct != 18 {
		t.Fatalf("upserted row = %+v, want drop 18 on the vehicle row", vehRow)
	}
}

// ---------------------------------------------------------------------------
// live engine + worker (real NATS publish + notify pipeline)
// ---------------------------------------------------------------------------

// TestITEngineEndToEnd: RaiseAlert with an inserted alert publishes on real
// JetStream, fans the websocket notification out and writes the audit rows.
func TestITEngineEndToEnd(t *testing.T) {
	h := newITStoreHarness(t)
	ctx := h.ctx

	eng := NewEngine(h.cfg, h.red, h.nats, h.store)
	dedup := itMarker + ":e2e"

	a := &models.Alert{
		Type: models.AlertOverspeeding, Severity: models.SeverityHigh,
		VehicleID: 7, IMEI: itMarker, CompanyCode: itCompany,
		Lat: -6.2, Lon: 106.8, Speed: 140,
		Status: models.StatusOpen, DedupKey: dedup,
		Metadata: map[string]any{"speed_kmh": 140.0},
	}
	raised, err := eng.RaiseAlert(ctx, a)
	if err != nil {
		t.Fatalf("RaiseAlert: %v", err)
	}
	if raised == nil || raised.ID <= 0 {
		t.Fatalf("raised = %+v, want the stored alert", raised)
	}

	// The alert row is persisted with the marker dedup key.
	if got := h.queryInt(`SELECT count(*) FROM th_alerts WHERE dedup_key = $1`, dedup); got != 1 {
		t.Errorf("persisted alert rows = %d, want 1", got)
	}

	// The Notify pipeline ran (websocket recipients may be empty in the dev
	// tenant — no failure is acceptable either way; audit rows only when the
	// pipeline produced rows).
	h.t.Cleanup(func() {
		h.exec(`DELETE FROM th_alerts WHERE dedup_key = $1`, dedup)
		h.exec(`DELETE FROM td_notifications WHERE alert_id IN (SELECT id FROM th_alerts WHERE dedup_key = $1)`, dedup)
	})

	// A repeat inside the dedup window is suppressed by the Redis fast path.
	repeat, err := eng.RaiseAlert(ctx, a)
	if err != nil || repeat != nil {
		t.Errorf("deduped repeat = (%+v, %v), want (nil, nil)", repeat, err)
	}
}

// TestITWorkerLifecycle: Start subscribes on live NATS, the sweeps run against
// the real store and Stop drains.
func TestITWorkerLifecycle(t *testing.T) {
	h := newITStoreHarness(t)
	ctx := h.ctx

	w := New(h.cfg, h.red, h.nats, h.store)
	sub, err := w.Start()
	if err != nil || sub == nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// The company seed includes DEV001.
	found := false
	for _, c := range w.companiesSnapshot() {
		if c == itCompany {
			found = true
		}
	}
	if !found {
		t.Errorf("company seed = %v, want %s", w.companiesSnapshot(), itCompany)
	}

	// Direct loop invocations (the ticker cadence would wait a minute).
	w.cacheRefreshLoop(ctx) // loads every cached lookup for the seeded companies

	// escalationLoop with an aged open SOS + an exhausted cap counter.
	vID := h.itVehicle(itMarker)
	h.itGrant(h.itMasterUser("esc"), vID) // a recipient for the notify path
	_ = vID
	sosID := h.itAlert(itMarker+":esc", models.AlertSOS, time.Now().UTC().Add(-time.Hour))
	h.cfg.Alert.SOSEscalationMax = 3
	h.red.Del(ctx, "alert:sos:esc:"+itCompany+":"+strconvItoa(sosID))
	w.escalationLoop(ctx)
	if got := h.queryInt(`SELECT escalation_count FROM th_alerts WHERE id = $1`, sosID); got < 1 {
		t.Errorf("escalation loop must bump the counter, got %d", got)
	}

	// Cap exceeded → skipped (counter past max).
	h.red.Set(ctx, "alert:sos:esc:"+itCompany+":"+strconvItoa(sosID), "99", time.Minute)
	w.escalationLoop(ctx) // must not escalate past the cap

	// sweepLoop over stale vehicles raises OFFLINE alerts (published live).
	w.sweepLoop(ctx)
}

func strconvItoa(v int64) string { return strconv.FormatInt(v, 10) }
