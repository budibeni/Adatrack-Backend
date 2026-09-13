package controllers

// Handler-level unit tests (B4 coverage 2026-08-31): mendorong checkGeofence /
// checkSpeeding / checkBattery / checkFuel / handleSOS / notify-dispatch /
// escalation-redis / helper murni memakai fakeStore (interface store) +
// miniredis (Redis in-memory) + tenant.Manager zero-value (aman — Master()→nil
// guarded, DB()→error tanpa panic). Semua jalur yang menyentuh NATS
// (publishAlert/dispatch-websocket) aman karena wa.nac=nil di-guard.

import (
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"

	"github.com/alicebob/miniredis/v2"
)

// ---------------------------------------------------------------------------
// fakeStore — implementasi penuh interface store untuk deterministik test.
// ---------------------------------------------------------------------------

type notifCall struct {
	userID  uint64
	alertID uint64
	channel string
	status  string
}

type fakeStore struct {
	vehID     uint64
	vehStatus string
	vehErr    error

	insertID  uint64
	insertErr error
	inserts   []models.AlertRecord

	open    bool
	openErr error

	speedCfg models.SpeedConfig
	speedOK  bool
	speedErr error

	fuelCfg models.FuelConfig
	fuelOK  bool
	fuelErr error

	geofences   []models.GeofenceDef
	geofenceErr error

	prefs    []models.NotifPreference
	prefsErr error

	vehUsers    []uint64
	vehUsersErr error

	sosList    []models.OpenSOSAlert
	sosListErr error

	notifs      []notifCall
	assignments []models.RouteAssignment
}

func (f *fakeStore) VehicleByIMEI(imei string) (uint64, string, error) {
	return f.vehID, f.vehStatus, f.vehErr
}
func (f *fakeStore) InsertAlert(a models.AlertRecord) (uint64, error) {
	f.inserts = append(f.inserts, a)
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	if f.insertID == 0 {
		f.insertID = uint64(len(f.inserts)) + 100
	}
	return f.insertID, nil
}
func (f *fakeStore) HasOpenAlert(vehicleID uint64, alertType string) (bool, error) {
	return f.open, f.openErr
}
func (f *fakeStore) SpeedConfigFor(vehicleID uint64) (models.SpeedConfig, bool, error) {
	return f.speedCfg, f.speedOK, f.speedErr
}
func (f *fakeStore) FuelConfigFor(vehicleID uint64) (models.FuelConfig, bool, error) {
	return f.fuelCfg, f.fuelOK, f.fuelErr
}
func (f *fakeStore) ActiveGeofences(vehicleID uint64) ([]models.GeofenceDef, error) {
	return f.geofences, f.geofenceErr
}
func (f *fakeStore) LoadActiveAssignments() ([]models.RouteAssignment, error) {
	return f.assignments, nil
}
func (f *fakeStore) UpdateAssignmentStatus(id uint64, status string) error { return nil }
func (f *fakeStore) UpdateAssignmentDeviation(id uint64, meters float64) error {
	return nil
}
func (f *fakeStore) EnabledPreferences(alertTypes []string) ([]models.NotifPreference, error) {
	return f.prefs, f.prefsErr
}
func (f *fakeStore) VehicleUserIDs(vehicleID uint64) ([]uint64, error) {
	return f.vehUsers, f.vehUsersErr
}
func (f *fakeStore) RecordNotification(userID uint64, alertID uint64, channel, alertType, subject, body, status, errMsg string) error {
	f.notifs = append(f.notifs, notifCall{userID: userID, alertID: alertID, channel: channel, status: status})
	return nil
}
func (f *fakeStore) OpenSOSOlderThan(cutoff time.Time) ([]models.OpenSOSAlert, error) {
	return f.sosList, f.sosListErr
}
func (f *fakeStore) GetAlert(id uint64) (models.AlertRecord, error) {
	return models.AlertRecord{}, nil
}

// ---------------------------------------------------------------------------
// Helper: WorkerAlert dengan miniredis + tenant manager zero-value.
// ---------------------------------------------------------------------------

func newTestWA(t *testing.T) (*WorkerAlert, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	red, err := internal.NewRedisClient(&internal.Config{
		Redis: struct {
			Addr      string
			Password  string
			DB        int
			PoolSize  int
			KeyPrefix string
		}{Addr: mr.Addr(), PoolSize: 2},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient(miniredis): %v", err)
	}
	wa, err := New(internal.LoadConfig(), &tenant.Manager{}, nil, red, nil)
	if err != nil {
		t.Fatalf("New worker-alert: %v", err)
	}
	return wa, mr
}

func tm(imei string, fuel *float64, speed float64, lat, lon float64, battery uint8) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI: imei, CompanyCode: "DEV001", Speed: speed,
		Lat: lat, Lon: lon, Battery: battery, FuelLevel: fuel,
	}
}
// ---------------------------------------------------------------------------
// checkSpeeding
// ---------------------------------------------------------------------------

func TestCheckSpeeding_TriggerWithinGrace(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{speedCfg: models.SpeedConfig{SpeedLimitKMH: 80, GraceMargin: 10}, speedOK: true}
	// 95 > 90 (limit+grace) tapi < 135 (1.5x) → warning.
	wa.checkSpeeding(f, "DEV001", tm("1", nil, 95, 0, 0, 0), 7)
	if len(f.inserts) != 1 {
		t.Fatalf("expected 1 overspeed alert, got %d", len(f.inserts))
	}
	if f.inserts[0].AlertType != models.AlertTypeSpeed || f.inserts[0].Severity != "warning" {
		t.Fatalf("unexpected alert: %s/%s", f.inserts[0].AlertType, f.inserts[0].Severity)
	}
}

func TestCheckSpeeding_Critical(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	// Default env 80+10=90; 150 > 135 (1.5x) → critical.
	wa.checkSpeeding(f, "DEV001", tm("1", nil, 150, 0, 0, 0), 7)
	if len(f.inserts) != 1 || f.inserts[0].Severity != "critical" {
		t.Fatalf("expected critical overspeed, got %+v", f.inserts)
	}
}

func TestCheckSpeeding_UnderLimitNoAlert(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	wa.checkSpeeding(f, "DEV001", tm("1", nil, 50, 0, 0, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("expected no alert, got %d", len(f.inserts))
	}
	// Kecepatan 0 (frame tanpa speed) → early return.
	wa.checkSpeeding(f, "DEV001", tm("1", nil, 0, 0, 0, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("speed 0 must not trigger, got %d", len(f.inserts))
	}
}

func TestCheckSpeeding_ConfigErrorMetricsPath(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{speedErr: errBoom}
	wa.checkSpeeding(f, "DEV001", tm("1", nil, 100, 0, 0, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("config error must not insert alert")
	}
}

// ---------------------------------------------------------------------------
// checkBattery
// ---------------------------------------------------------------------------

func TestCheckBattery_LowTriggersAndDedup(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	wa.checkBattery(f, "DEV001", tm("1", nil, 0, 0, 0, 15), 7)
	if len(f.inserts) != 1 {
		t.Fatalf("expected 1 battery alert, got %d", len(f.inserts))
	}
	if f.inserts[0].AlertType != models.AlertTypeBattery || f.inserts[0].Severity != "medium" {
		t.Fatalf("unexpected: %s/%s", f.inserts[0].AlertType, f.inserts[0].Severity)
	}
	// Panggilan kedua dalam dedup window (30m) → tidak ada alert baru.
	wa.checkBattery(f, "DEV001", tm("1", nil, 0, 0, 0, 12), 7)
	if len(f.inserts) != 1 {
		t.Fatalf("dedup window must suppress, got %d", len(f.inserts))
	}
}

func TestCheckBattery_OpenAlertGuard(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{open: true}
	wa.checkBattery(f, "DEV001", tm("9", nil, 0, 0, 0, 10), 8)
	if len(f.inserts) != 0 {
		t.Fatalf("open alert guard must suppress, got %d", len(f.inserts))
	}
}

func TestCheckBattery_NormalBatteryNoAlert(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	wa.checkBattery(f, "DEV001", tm("2", nil, 0, 0, 0, 30), 7) // >=20
	wa.checkBattery(f, "DEV001", tm("2", nil, 0, 0, 0, 0), 7)  // 0 = tidak tersedia
	if len(f.inserts) != 0 {
		t.Fatalf("expected no battery alerts, got %d", len(f.inserts))
	}
}
// ---------------------------------------------------------------------------
// checkGeofence
// ---------------------------------------------------------------------------

func TestCheckGeofence_CircleEntryThenExit(t *testing.T) {
	wa, mr := newTestWA(t)
	f := &fakeStore{geofences: []models.GeofenceDef{
		{ID: 1, Name: "Depot", AreaType: "circle", CenterLat: -6.2088, CenterLon: 106.8456, RadiusMeters: 800},
	}}
	// Di dalam circle pada posisi 1 → entry breach.
	wa.checkGeofence(f, "DEV001", tm("1", nil, 0, -6.2090, 106.8458, 0), 7)
	// Di luar circle pada posisi 2 → exit breach.
	wa.checkGeofence(f, "DEV001", tm("1", nil, 0, -6.2150, 106.8530, 0), 7)
	if len(f.inserts) != 2 {
		t.Fatalf("expected entry+exit alerts, got %d", len(f.inserts))
	}
	if f.inserts[0].AlertType != models.AlertTypeGeofence {
		t.Fatalf("alert type = %s", f.inserts[0].AlertType)
	}
	// State Redis harus mencerminkan outside (0).
	if !mr.Exists("adatrack_gps:dev001:geofence_state:1") {
		t.Fatal("geofence state key must exist after transition")
	}
	if val := mr.HGet("adatrack_gps:dev001:geofence_state:1", "1"); val != "0" {
		t.Fatalf("geofence state field 1 = %q, want 0", val)
	}
}

func TestCheckGeofence_PolygonInsideInsideNoBreach(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{geofences: []models.GeofenceDef{
		{ID: 2, Name: "Poly", AreaType: "polygon", Boundary: [][2]float64{{0, 0}, {0, 10}, {10, 10}, {10, 0}}},
	}}
	// Posisi di dalam polygon → entry pada first call.
	wa.checkGeofence(f, "DEV001", tm("3", nil, 0, 5, 5, 0), 7)
	// Kedua call masih di dalam → tidak breach baru (state 1→1).
	wa.checkGeofence(f, "DEV001", tm("3", nil, 0, 5.1, 5.1, 0), 7)
	if len(f.inserts) != 1 {
		t.Fatalf("expected only entry alert, got %d", len(f.inserts))
	}
}

func TestCheckGeofence_NoPositionNoLookup(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{geofenceErr: errBoom}
	wa.checkGeofence(f, "DEV001", tm("4", nil, 0, 0, 0, 0), 7) // lat==0 && lon==0 → return
	if len(f.inserts) != 0 {
		t.Fatalf("no position must not trigger")
	}
}

func TestCheckGeofence_LookupError(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{geofenceErr: errBoom}
	wa.checkGeofence(f, "DEV001", tm("4", nil, 0, -6.20, 106.80, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("lookup error must not insert")
	}
}

var errBoom = errTestBoom{}

type errTestBoom struct{}

func (errTestBoom) Error() string { return "boom" }
// ---------------------------------------------------------------------------
// checkFuel (B5a)
// ---------------------------------------------------------------------------

func seedFuelState(t *testing.T, mr *miniredis.Miniredis, key string, level float64, ts int64) {
	t.Helper()
	raw := `{"level":` + formatFuel(level) + `,"timestamp":` + intStr(ts) + `}`
	if err := mr.Set(key, raw); err != nil {
		t.Fatalf("seed fuel state: %v", err)
	}
}

func intStr(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func formatFuel(f float64) string {
	return strings.TrimRight(strings.TrimRight(intStr(int64(f))+".0", "0"), ".")
}

func TestCheckFuel_DropCreatesCritical(t *testing.T) {
	wa, mr := newTestWA(t)
	f := &fakeStore{}
	key := "adatrack_gps:dev001:fuel_state:864201040512345"
	seedFuelState(t, mr, key, 40, time.Now().Unix()-1000)
	lvl := 20.0
	wa.checkFuel(f, "DEV001", tm("864201040512345", &lvl, 0, 0, 0, 0), 7)
	if len(f.inserts) != 1 {
		t.Fatalf("expected FUEL_DROP alert, got %d", len(f.inserts))
	}
	if f.inserts[0].AlertType != models.AlertTypeFuelDrop || f.inserts[0].Severity != "critical" {
		t.Fatalf("unexpected: %s/%s", f.inserts[0].AlertType, f.inserts[0].Severity)
	}
}

func TestCheckFuel_RefuelCreatesLow(t *testing.T) {
	wa, mr := newTestWA(t)
	f := &fakeStore{}
	key := "adatrack_gps:dev001:fuel_state:864201040512346"
	seedFuelState(t, mr, key, 20, time.Now().Unix()-1000)
	lvl := 45.0
	wa.checkFuel(f, "DEV001", tm("864201040512346", &lvl, 0, 0, 0, 0), 7)
	if len(f.inserts) != 1 {
		t.Fatalf("expected REFUEL alert, got %d", len(f.inserts))
	}
	if f.inserts[0].AlertType != models.AlertTypeRefuel || f.inserts[0].Severity != "low" {
		t.Fatalf("unexpected: %s/%s", f.inserts[0].AlertType, f.inserts[0].Severity)
	}
}

func TestCheckFuel_TooSoonAntiNoise(t *testing.T) {
	wa, mr := newTestWA(t)
	f := &fakeStore{}
	key := "adatrack_gps:dev001:fuel_state:864201040512347"
	// Bacaan terakhir baru saja (delta 1 detik < window 300s) → anti-noise.
	seedFuelState(t, mr, key, 40, time.Now().Unix()-1)
	lvl := 20.0
	wa.checkFuel(f, "DEV001", tm("864201040512347", &lvl, 0, 0, 0, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("too-soon reading must be ignored, got %d", len(f.inserts))
	}
}

func TestCheckFuel_ACCGateSuppresses(t *testing.T) {
	t.Setenv("FUEL_DROP_REQUIRE_ACC", "true")
	t.Setenv("FUEL_ACC_STALE_SECONDS", "600")
	wa, mr := newTestWA(t)
	f := &fakeStore{}
	// live-state dengan ACC fresh=false.
	now := time.Now().Unix()
	_ = mr.Set("adatrack_gps:dev001:vehicle:state:864201040512348",
		`{"acc":false,"last_seen":`+intStr(now)+`}`)
	key := "adatrack_gps:dev001:fuel_state:864201040512348"
	seedFuelState(t, mr, key, 40, now-1000)
	lvl := 20.0
	wa.checkFuel(f, "DEV001", tm("864201040512348", &lvl, 0, 0, 0, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("strict ACC gate must suppress while ACC fresh OFF, got %d", len(f.inserts))
	}
}

func TestCheckFuel_NoStateIsFirstReading(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	lvl := 30.0
	wa.checkFuel(f, "DEV001", tm("5", &lvl, 0, 0, 0, 0), 7)
	if len(f.inserts) != 0 {
		t.Fatalf("first reading must only initialize state")
	}
}
// ---------------------------------------------------------------------------
// handleSOS
// ---------------------------------------------------------------------------

func TestHandleSOS_CreatesCritical(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	sos := models.TelemetryMessage{IMEI: "1", CompanyCode: "DEV001", Raw: "sos", Lat: -6.2, Lon: 106.8}
	wa.handleSOS(f, "DEV001", sos, 7)
	if len(f.inserts) != 1 {
		t.Fatalf("expected 1 SOS alert, got %d", len(f.inserts))
	}
	rec := f.inserts[0]
	if rec.AlertType != models.AlertTypeSOS || rec.Severity != "critical" {
		t.Fatalf("unexpected: %s/%s", rec.AlertType, rec.Severity)
	}
	if rec.VehicleLat == nil || rec.VehicleLon == nil {
		t.Fatalf("SOS alert must attach position")
	}
	// Cooldown aktif → panggilan kedua tidak membuat alert baru.
	wa.handleSOS(f, "DEV001", sos, 7)
	if len(f.inserts) != 1 {
		t.Fatalf("SOS cooldown must suppress, got %d", len(f.inserts))
	}
}

func TestIsSOS(t *testing.T) {
	if !isSOS(models.TelemetryMessage{Raw: "...sos..."}) {
		t.Fatal("raw containing sos must be detected")
	}
	if isSOS(models.TelemetryMessage{Raw: "regular"}) {
		t.Fatal("raw without sos must not trigger")
	}
}

// ---------------------------------------------------------------------------
// notifyAlert + dispatch (channel selain websocket — aman tanpa NATS)
// ---------------------------------------------------------------------------

func TestNotifyAlert_NoPrefsReturns(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	rec := models.AlertRecord{ID: 1, VehicleID: 7, AlertType: models.AlertTypeSpeed, Severity: "warning"}
	wa.notifyAlert(f, "DEV001", tm("1", nil, 0, 0, 0, 0), rec)
	if len(f.notifs) != 0 {
		t.Fatalf("no prefs must not dispatch, got %d", len(f.notifs))
	}
}

func TestNotifyAlert_PrefsError(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{prefsErr: errBoom}
	rec := models.AlertRecord{ID: 1, VehicleID: 7, AlertType: models.AlertTypeSpeed, Severity: "warning"}
	wa.notifyAlert(f, "DEV001", tm("1", nil, 0, 0, 0, 0), rec)
	if len(f.notifs) != 0 {
		t.Fatalf("prefs error must not dispatch")
	}
}

func TestNotifyAlert_UserNotAllowedSkipped(t *testing.T) {
	wa, _ := newTestWA(t)
	// Tenant manager zero-value → adminUserIDs error; vehUsers kosong → user
	// tidak allowed → dispatch tidak dipanggil (aman, tidak sentuh NATS).
	f := &fakeStore{
		prefs:    []models.NotifPreference{{UserID: 99, Channel: "websocket", MinSeverity: "info"}},
		vehUsers: nil,
	}
	rec := models.AlertRecord{ID: 1, VehicleID: 7, AlertType: models.AlertTypeSpeed, Severity: "warning"}
	wa.notifyAlert(f, "DEV001", tm("1", nil, 0, 0, 0, 0), rec)
	if len(f.notifs) != 0 {
		t.Fatalf("non-allowed user must be skipped, got %d", len(f.notifs))
	}
}

func TestDispatch_ExternalChannelsAudit(t *testing.T) {
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	rec := models.AlertRecord{ID: 1, VehicleID: 7, AlertType: models.AlertTypeSpeed, Severity: "warning"}
	tmMsg := tm("1", nil, 0, 0, 0, 0)
	// email tanpa SMTP_HOST → status skipped tercatat.
	wa.dispatch(f, "DEV001", 1, models.NotifPreference{UserID: 1, Channel: "email", MinSeverity: "info"}, rec, tmMsg)
	if len(f.notifs) != 1 || f.notifs[0].status != "skipped" || f.notifs[0].channel != "email" {
		t.Fatalf("email dispatch audit = %+v", f.notifs)
	}
	// sms tanpa SMS_API_KEY → skipped.
	wa.dispatch(f, "DEV001", 1, models.NotifPreference{UserID: 1, Channel: "sms", MinSeverity: "info"}, rec, tmMsg)
	if len(f.notifs) != 2 || f.notifs[1].status != "skipped" {
		t.Fatalf("sms dispatch audit = %+v", f.notifs)
	}
	// push → skipped (belum didukung).
	wa.dispatch(f, "DEV001", 1, models.NotifPreference{UserID: 1, Channel: "push", MinSeverity: "info"}, rec, tmMsg)
	if len(f.notifs) != 3 || f.notifs[2].status != "skipped" {
		t.Fatalf("push dispatch audit = %+v", f.notifs)
	}
	// unknown channel → warn, tanpa audit.
	wa.dispatch(f, "DEV001", 1, models.NotifPreference{UserID: 1, Channel: "carrier_pigeon", MinSeverity: "info"}, rec, tmMsg)
	if len(f.notifs) != 3 {
		t.Fatalf("unknown channel must not create audit row, got %d", len(f.notifs))
	}
}

func TestDispatch_EmailWithSMTPConfigured(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	wa, _ := newTestWA(t)
	f := &fakeStore{}
	rec := models.AlertRecord{ID: 1, VehicleID: 7, AlertType: models.AlertTypeSpeed, Severity: "warning"}
	wa.dispatch(f, "DEV001", 1, models.NotifPreference{UserID: 1, Channel: "email", MinSeverity: "info"}, rec, tm("1", nil, 0, 0, 0, 0))
	if len(f.notifs) != 1 || f.notifs[0].status != "pending" {
		t.Fatalf("email with SMTP should be pending, got %+v", f.notifs)
	}
}

// ---------------------------------------------------------------------------
// sos_escalation — Redis helpers (incr, mark seen) tanpa NATS
// ---------------------------------------------------------------------------

func TestIncrEscalation(t *testing.T) {
	wa, mr := newTestWA(t)
	n, err := wa.incrEscalation("DEV001", 42)
	if err != nil {
		t.Fatalf("incr escalation: %v", err)
	}
	if n != 1 {
		t.Fatalf("first escalation count = %d, want 1", n)
	}
	n2, _ := wa.incrEscalation("DEV001", 42)
	if n2 != 2 {
		t.Fatalf("second escalation count = %d, want 2", n2)
	}
	if has := mr.Exists("adatrack_gps:dev001:sos_escalation:42"); !has {
		t.Fatal("escalation key must exist")
	}
}

func TestMarkTTASeen(t *testing.T) {
	wa, mr := newTestWA(t)
	first, err := wa.markTTASeen("DEV001", 7)
	if err != nil || !first {
		t.Fatalf("first TTA mark: first=%v err=%v", first, err)
	}
	second, _ := wa.markTTASeen("DEV001", 7)
	if second {
		t.Fatal("second mark must report seen already")
	}
	if has := mr.Exists("adatrack_gps:dev001:sos_tta_seen"); !has {
		t.Fatal("TTA seen set must exist")
	}
}

func TestEscalateCompany_StoreErrMeters(t *testing.T) {
	wa, _ := newTestWA(t)
	// tenant manager zero-value → newStore error → incError tanpa panic.
	wa.escalateCompany("NOPE", 2*time.Minute, 3)
}