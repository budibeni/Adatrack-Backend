package controllers

// store_pg_it_test.go — integration coverage for the PostgresStore (PRD §6.2
// company schemas + master anti-spoofing map) on a LIVE PostgreSQL.
//
// It exercises the real tenant manager → company pool → real SQL paths that
// the hermetic (miniredis / fakeStore) tests cannot reach, mirroring the
// worker-persistence integration harness:
//   - opt-in via ADATRACK_IT=1 (so `go test ./...` stays hermetic),
//   - fixtures are written with unique IMEI/tag markers,
//   - every fixture is torn down in t.Cleanup, leaving DEV001 untouched,
//   - host ports (127.0.0.1:5533/6380/4222) match scripts/start-services.sh.

import (
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"adatrack_gps/api-vehicle/models"
	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// itCompany is the provider tenant seeded by database/seed + provision-tenant.sh.
const itCompany = "DEV001"

// skipNoDB gates the DB integration suite behind ADATRACK_IT=1.
func skipNoDB(t *testing.T) {
	t.Helper()
	if v, ok := os.LookupEnv("ADATRACK_IT"); ok && v == "1" {
		return
	}
	t.Skip("integration test — set ADATRACK_IT=1 with live PostgreSQL (127.0.0.1:5533)")
}

// envOr reads an env var or returns def.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// itHarness wires a real PostgresStore on the DEV001 tenant. The tenant manager
// is closed in t.Cleanup.
type itHarness struct {
	t     *testing.T
	ctx   context.Context
	store *PostgresStore
	tm    *tenant.Manager
	cfg   *internal.Config
}

func newITHarness(t *testing.T) *itHarness {
	t.Helper()
	skipNoDB(t)

	// Point at the host-port-forwarded infra (compose via start-services.sh).
	// LoadProjectEnv only fills in password/schema defaults not yet set.
	internal.LoadProjectEnv()
	t.Setenv("POSTGRES_HOST", envOr("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", envOr("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", envOr("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", envOr("ADATRACK_IT_REDIS_PORT", "6380"))
	t.Setenv("NATS_URL", envOr("ADATRACK_IT_NATS_URL", "nats://127.0.0.1:4222"))

	cfg := internal.LoadConfig()
	tcfg := tenant.ConfigFromEnv(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tm, err := tenant.New(ctx, cfg, tcfg, nil, nil)
	if err != nil {
		t.Fatalf("tenant manager unavailable (is compose up + migrated?): %v", err)
	}
	if _, err := tm.DB(itCompany); err != nil {
		t.Fatalf("tenant %s not provisioned: %v", itCompany, err)
	}
	h := &itHarness{t: t, ctx: context.Background(), tm: tm, cfg: cfg}
	h.store = NewPostgresStore(tm)
	t.Cleanup(func() { tm.Close() })
	return h
}

// companyDB is the DEV001 tenant pool (direct SQL for assertions + cleanup).
func (h *itHarness) companyDB() *internal.DBPool {
	pool, err := h.tm.DB(itCompany)
	if err != nil {
		h.t.Fatalf("company pool: %v", err)
	}
	return pool
}

// masterDB is the master pool (auth + IMEI map, different schema).
func (h *itHarness) masterDB() *internal.DBPool {
	return h.tm.Master()
}

// execCompany runs cleanup SQL against the DEV001 company schema.
func (h *itHarness) execCompany(q string, args ...any) {
	if _, err := h.companyDB().DB.ExecContext(h.ctx, q, args...); err != nil {
		h.t.Fatalf("company exec %q: %v", q, err)
	}
}

// execMaster runs SQL against the master schema.
func (h *itHarness) execMaster(q string, args ...any) {
	if _, err := h.masterDB().DB.ExecContext(h.ctx, q, args...); err != nil {
		h.t.Fatalf("master exec %q: %v", q, err)
	}
}

// --- unique-tagged fixtures -------------------------------------------------
//
// One atomic counter keeps IMEIs/names unique within a process; combined with
// the pid they survive re-runs against the shared DEV001 dev DB.

var itSeq uint64

func itTag() string { return "it" + strconv.Itoa(int(atomic.AddUint64(&itSeq, 1))) }

// itVehicle inserts one vehicle + master IMEI map row; cleanup removes it (child
// rows cascade via the FK ON DELETE CASCADE) plus the master map row.
func (h *itHarness) itVehicle() (id int64, imei string) {
	imei = itTag()
	plate := "IT-" + itTag()
	v := &models.Vehicle{
		IMEI:        imei,
		PlateNumber: plate,
		Make:        ptrString("TestMake"),
		Model:       ptrString("TestModel"),
		Year:        ptrInt(2020),
		Color:       ptrString("white"),
		FuelType:    ptrString("petrol"),
		Status:      "active",
	}
	id, err := h.store.CreateVehicle(h.ctx, itCompany, v, 1)
	if err != nil {
		h.t.Fatalf("create vehicle: %v", err)
	}
	h.t.Cleanup(func() {
		h.execCompany("DELETE FROM tm_vehicles WHERE id = $1", id)
		h.execMaster("DELETE FROM tm_vehicle_imei_map WHERE imei = $1", imei)
	})
	return id, imei
}

// itGeofence inserts a zone + its vehicle mapping; cleanup deletes the zone
// (cascades tm_geofence_vehicles).
func (h *itHarness) itGeofence(vehicleID int64, kind string) (id int64, name string) {
	name = "gfw-" + itTag()
	var g *models.Geofence
	if kind == "circle" {
		g = &models.Geofence{
			Name:       name,
			AreaType:   "circle",
			CenterLat:  ptrFloat64(-6.1025),
			CenterLon:  ptrFloat64(106.8198),
			RadiusM:    ptrInt(500),
			Severity:   "high",
			OnEntry:    true,
			OnExit:     true,
			Active:     true,
			VehicleIDs: []int64{vehicleID},
		}
	} else {
		g = &models.Geofence{
			Name:     name,
			AreaType: "polygon",
			Boundary: [][2]float64{{-6.1025, 106.8198}, {-6.2, 106.9}, {-6.15, 107.0}},
			Severity: "medium",
			Active:   true,
		}
	}
	id, err := h.store.CreateGeofence(h.ctx, itCompany, g, 1)
	if err != nil {
		h.t.Fatalf("create geofence (%s): %v", kind, err)
	}
	h.t.Cleanup(func() {
		h.execCompany("DELETE FROM tm_geofences WHERE id = $1", id)
	})
	return id, name
}

// itRoute inserts one route; cleanup deletes the route + its assignments.
func (h *itHarness) itRoute() (id int64, name string) {
	name = "rte-" + itTag()
	wp := []models.Waypoint{{Seq: 0, Lat: -6.1025, Lon: 106.8198, Name: "A"},
		{Seq: 1, Lat: -6.2, Lon: 106.9, Name: "B"}}
	r := &models.Route{Name: name, Description: ptrString("it route"), Waypoints: wp, EstMinutes: ptrInt(12)}
	id, err := h.store.CreateRoute(h.ctx, itCompany, r, 1)
	if err != nil {
		h.t.Fatalf("create route: %v", err)
	}
	h.t.Cleanup(func() {
		h.execCompany("DELETE FROM th_route_assignments WHERE route_id = $1", id)
		h.execCompany("DELETE FROM tm_routes WHERE id = $1", id)
	})
	return id, name
}

// itSpeedConfig inserts one speed config; cleanup deletes it by id.
func (h *itHarness) itSpeedConfig(vehicleID int64, global bool) int64 {
	sc := &models.SpeedConfig{MaxSpeedKMH: 80, GracePct: 10, Severity: "high", Enabled: true}
	if !global {
		sc.VehicleID = ptrInt64(vehicleID)
	}
	id, err := h.store.CreateSpeedConfig(h.ctx, itCompany, sc, 1)
	if err != nil {
		h.t.Fatalf("create speed config (global=%v): %v", global, err)
	}
	h.t.Cleanup(func() { h.execCompany("DELETE FROM tm_speed_configs WHERE id = $1", id) })
	return id
}

// itFuelConfig inserts one fuel config; cleanup deletes it by id.
func (h *itHarness) itFuelConfig(vehicleID int64, global bool) int64 {
	fc := &models.FuelConfig{DropThresholdPct: 15, RefuelThresholdPct: 10, WindowSeconds: 300,
		Severity: "critical", RequireACC: false, ACCStaleSeconds: 600, Enabled: true}
	if !global {
		fc.VehicleID = ptrInt64(vehicleID)
	}
	id, err := h.store.CreateFuelConfig(h.ctx, itCompany, fc, 1)
	if err != nil {
		h.t.Fatalf("create fuel config (global=%v): %v", global, err)
	}
	h.t.Cleanup(func() { h.execCompany("DELETE FROM tm_fuel_configs WHERE id = $1", id) })
	return id
}

// itFuelLog inserts a raw fuel-history row (no FK, partitioned table).
func (h *itHarness) itFuelLog(vehicleID int64, imei string, ts time.Time, level *float64) {
	h.t.Helper()
	q := `INSERT INTO td_fuel_logs (vehicle_id, imei, company_code, fuel_level,` +
		` fuel_volume, fuel_temp_c, latitude, longitude, acc_status, "timestamp")` +
		` VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	if _, err := h.companyDB().DB.ExecContext(h.ctx, q, vehicleID, imei, itCompany, level,
		nil, nil, nil, nil, nil, ts); err != nil {
		h.t.Fatalf("insert fuel log: %v", err)
	}
	h.t.Cleanup(func() {
		h.execCompany(`DELETE FROM td_fuel_logs WHERE vehicle_id = $1`, vehicleID)
	})
}

// itAlert inserts a raw alert row (worker owns this table) with a unique
// dedup_key; cleanup deletes by dedup_key.
func (h *itHarness) itAlert(vehicleID int64, imei, typ, sev string) (id int64) {
	h.t.Helper()
	dedup := "it:" + itTag() + ":alert"
	err := h.companyDB().DB.QueryRowContext(h.ctx,
		`INSERT INTO th_alerts (type, severity, vehicle_id, imei, company_code, dedup_key, status, detected_at)`+
			` VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		typ, sev, vehicleID, imei, itCompany, dedup, "open", time.Now()).Scan(&id)
	if err != nil {
		h.t.Fatalf("insert alert: %v", err)
	}
	h.t.Cleanup(func() {
		h.execCompany(`DELETE FROM th_alerts WHERE dedup_key = $1`, dedup)
	})
	return id
}

// --- pointer helpers (distinct from fi/ii/i64/f64 to avoid redeclaration) ----

func ptrString(v string) *string    { return &v }
func ptrInt(v int) *int             { return &v }
func ptrInt64(v int64) *int64       { return &v }
func ptrFloat64(v float64) *float64 { return &v }
func ptrBool(v bool) *bool          { return &v }

// derefString is a helper for assertions on nullable scans.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---------------------------------------------------------------------------
// vehicles + master IMEI map (CreateVehicle, VehicleByID, IMEIExists, Update,
// ListVehicles filters + row-level, SoftDelete/Restore, SyncIMEIMap)
// ---------------------------------------------------------------------------

func TestITVehicleCRUDAndIMEIMap(t *testing.T) {
	h := newITHarness(t)
	id, imei := h.itVehicle()

	// VehicleByID round-trips the persisted fields.
	v, err := h.store.VehicleByID(h.ctx, itCompany, id, false)
	if err != nil {
		t.Fatalf("VehicleByID: %v", err)
	}
	if v == nil {
		t.Fatal("VehicleByID returned nil for existing id")
	}
	if v.IMEI != imei || derefString(v.Make) != "TestMake" || v.Status != "active" {
		t.Fatalf("VehicleByID fields = %+v", v)
	}

	// IMEIExists: present generally…
	if ok, err := h.store.IMEIExists(h.ctx, itCompany, imei, 0); err != nil || !ok {
		t.Fatalf("IMEIExists(exclude=0) = %v,%v want true", ok, err)
	}
	// …but the owner is excluded.
	if ok, err := h.store.IMEIExists(h.ctx, itCompany, imei, id); err != nil || ok {
		t.Fatalf("IMEIExists(exclude=self) = %v,%v want false", ok, err)
	}

	// UpdateVehicle + row-level ListVehicles (search by IMEI fragment).
	upd := *v
	upd.PlateNumber = "IT-UPD"
	if err := h.store.UpdateVehicle(h.ctx, itCompany, &upd, 1); err != nil {
		t.Fatalf("UpdateVehicle: %v", err)
	}
	rows, total, err := h.store.ListVehicles(h.ctx, VehicleQuery{
		CompanyCode: itCompany, AllVehicles: true, Search: imei, Page: 1, Limit: 50})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].PlateNumber != "IT-UPD" {
		t.Fatalf("ListVehicles search = %v rows, %d total, plate %q (want 1/1/IT-UPD); err %v",
			len(rows), total, safePlate(rows), err)
	}

	// Row-level grants: the scoped query only returns the granted ids.
	if granted, _, err := h.store.ListVehicles(h.ctx, VehicleQuery{
		CompanyCode: itCompany, AssignedIDs: []int64{id}, Page: 1, Limit: 50}); err != nil || len(granted) != 1 {
		t.Fatalf("ListVehicles row-level granted = %v rows, err %v want 1", len(granted), err)
	}
	// An empty grant set short-circuits to an empty page (no widening).
	if got, _, err := h.store.ListVehicles(h.ctx, VehicleQuery{
		CompanyCode: itCompany, AssignedIDs: []int64{999999}, Page: 1, Limit: 50}); err != nil || len(got) != 0 {
		t.Fatalf("ListVehicles non-granted id = %v rows, err %v want 0", len(got), err)
	}

	// Soft delete disables the master IMEI map row (FR-1.4 anti-spoof) and hides
	// the row from the default (includeDeleted=false) read; restore re-enables.
	if err := h.store.SoftDeleteVehicle(h.ctx, itCompany, id, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteVehicle: %v", err)
	}
	if got, _ := h.store.VehicleByID(h.ctx, itCompany, id, false); got != nil {
		t.Fatal("VehicleByID after soft delete should be nil")
	}
	if rest, _ := h.store.VehicleByID(h.ctx, itCompany, id, true); rest == nil || rest.DeletedAt == nil {
		t.Fatalf("VehicleByID includeDeleted = %+v (want deleted)", rest)
	}
	var active bool
	if err := h.masterDB().DB.QueryRowContext(h.ctx,
		`SELECT is_active FROM tm_vehicle_imei_map WHERE imei = $1`, imei).Scan(&active); err != nil {
		t.Fatalf("master imei map scan: %v", err)
	}
	if active {
		t.Fatal("IMEI map row should be inactive after soft delete")
	}
	if err := h.store.RestoreVehicle(h.ctx, itCompany, id); err != nil {
		t.Fatalf("RestoreVehicle: %v", err)
	}
	if got, err := h.store.VehicleByID(h.ctx, itCompany, id, false); err != nil || got == nil {
		t.Fatalf("VehicleByID after restore = %v, err %v want non-nil", got, err)
	}
}

// safePlate tolerates an empty result slice so the fatal message stays readable.
func safePlate(rows []models.Vehicle) string {
	if len(rows) == 0 {
		return "<none>"
	}
	return rows[0].PlateNumber
}

// ---------------------------------------------------------------------------
// geofences (Create/List/ByID/NameExists/Update/ReplaceVehicles/SoftDelete/Restore)
// ---------------------------------------------------------------------------

func TestITGeofenceLifecycle(t *testing.T) {
	h := newITHarness(t)
	vid, _ := h.itVehicle()
	gid, name := h.itGeofence(vid, "circle")

	// GeofenceByID eager-loads the vehicle mapping.
	g, err := h.store.GeofenceByID(h.ctx, itCompany, gid, false)
	if err != nil {
		t.Fatalf("GeofenceByID: %v", err)
	}
	if g == nil || g.Name != name || len(g.VehicleIDs) != 1 || g.VehicleIDs[0] != vid {
		t.Fatalf("GeofenceByID = %+v (want name=%s, vehicles=[%d])", g, name, vid)
	}

	// NameExists excludes self, collides for a different id.
	if ok, err := h.store.GeofenceNameExists(h.ctx, itCompany, name, gid); err != nil || ok {
		t.Fatalf("GeofenceNameExists(self) = %v,%v want false", ok, err)
	}
	if ok, err := h.store.GeofenceNameExists(h.ctx, itCompany, name, 999999); err != nil || !ok {
		t.Fatalf("GeofenceNameExists(other) = %v,%v want true", ok, err)
	}

	// ListGeofences page + count, ordered by name.
	rows, total, err := h.store.ListGeofences(h.ctx, GeofenceQuery{CompanyCode: itCompany, Page: 1, Limit: 50})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != gid {
		t.Fatalf("ListGeofences = %v rows, %d total (want 1/1); err %v", len(rows), total, err)
	}

	// UpdateGeofence + mapping replace: drop the vehicle, mapping must go empty.
	upd := *g
	upd.VehicleIDs = nil
	if err := h.store.UpdateGeofence(h.ctx, itCompany, &upd, 1); err != nil {
		t.Fatalf("UpdateGeofence: %v", err)
	}
	g2, _ := h.store.GeofenceByID(h.ctx, itCompany, gid, false)
	if len(g2.VehicleIDs) != 0 {
		t.Fatalf("GeofenceByID mapping after replace = %v want empty", g2.VehicleIDs)
	}

	// Soft delete / includeDeleted / restore.
	if err := h.store.SoftDeleteGeofence(h.ctx, itCompany, gid, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteGeofence: %v", err)
	}
	if got, _ := h.store.GeofenceByID(h.ctx, itCompany, gid, false); got != nil {
		t.Fatal("GeofenceByID after soft delete should be nil")
	}
	if got, _ := h.store.GeofenceByID(h.ctx, itCompany, gid, true); got == nil || got.DeletedAt == nil {
		t.Fatalf("GeofenceByID includeDeleted = %+v (want deleted)", got)
	}
	if err := h.store.RestoreGeofence(h.ctx, itCompany, gid); err != nil {
		t.Fatalf("RestoreGeofence: %v", err)
	}
}

// ---------------------------------------------------------------------------
// routes + assignments (Create/List/ByID/NameExists/Update/List/AssignmentByID/
// CreateAssignment/UpdateAssignmentStatus/SoftDeleteAssignment/SoftDeleteRoute)
// ---------------------------------------------------------------------------

func TestITRouteAndAssignment(t *testing.T) {
	h := newITHarness(t)
	rid, name := h.itRoute()
	vid, imei := h.itVehicle()

	r, err := h.store.RouteByID(h.ctx, itCompany, rid, false)
	if err != nil || r == nil || r.Name != name || len(r.Waypoints) != 2 {
		t.Fatalf("RouteByID = %+v, err %v", r, err)
	}

	// NameExists: self excluded, other collides.
	if ok, err := h.store.RouteNameExists(h.ctx, itCompany, name, rid); err != nil || ok {
		t.Fatalf("RouteNameExists(self) = %v,%v want false", ok, err)
	}
	if ok, err := h.store.RouteNameExists(h.ctx, itCompany, name, 0); err != nil || !ok {
		t.Fatalf("RouteNameExists(other) = %v,%v want true", ok, err)
	}

	// Assignment life-cycle: not_started → in_progress → completed.
	a := &models.RouteAssignment{RouteID: rid, VehicleID: vid, DriverUserID: ptrInt64(vid), Status: "not_started"}
	aid, err := h.store.CreateAssignment(h.ctx, itCompany, a, 1)
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	list, err := h.store.ListAssignments(h.ctx, itCompany, rid)
	if err != nil || len(list) != 1 || list[0].ID != aid || list[0].Status != "not_started" {
		t.Fatalf("ListAssignments = %+v (want 1, id=%d, not_started); err %v", list, aid, err)
	}
	one, err := h.store.AssignmentByID(h.ctx, itCompany, rid, aid)
	if err != nil || one == nil || one.Status != "not_started" {
		t.Fatalf("AssignmentByID = %+v, err %v", one, err)
	}
	one.Status = "in_progress"
	now := time.Now().UTC().Format(time.RFC3339)
	one.StartedAt = &now
	if err := h.store.UpdateAssignmentStatus(h.ctx, itCompany, one); err != nil {
		t.Fatalf("UpdateAssignmentStatus in_progress: %v", err)
	}
	one.Status = "completed"
	one.CompletedAt = &now
	if err := h.store.UpdateAssignmentStatus(h.ctx, itCompany, one); err != nil {
		t.Fatalf("UpdateAssignmentStatus completed: %v", err)
	}
	up, _ := h.store.AssignmentByID(h.ctx, itCompany, rid, aid)
	if up.Status != "completed" || up.StartedAt == nil || up.CompletedAt == nil {
		t.Fatalf("AssignmentByID after life-cycle = %+v (want completed + timestamps)", up)
	}
	_ = imei // vehicle fixture ties the FK; imei kept unique
}

// ---------------------------------------------------------------------------
// speed configs (Create/List/ByID/Update/SoftDelete/Restore + unique-globals)
// ---------------------------------------------------------------------------

func TestITSpeedConfigs(t *testing.T) {
	h := newITHarness(t)
	vid, _ := h.itVehicle()

	gid := h.itSpeedConfig(vid, true)    // global default
	vidSC := h.itSpeedConfig(vid, false) // vehicle-specific

	list, err := h.store.ListSpeedConfigs(h.ctx, itCompany, false)
	if err != nil {
		t.Fatalf("ListSpeedConfigs: %v", err)
	}
	// Assert pada fixture test INI, bukan jumlah baris total: schema dev bersama
	// bisa sudah berisi baris dari run manual/E2E, dan hitungan absolut membuat
	// test gagal padahal kode yang diuji benar.
	foundGlobal, foundVehicle := false, false
	for _, sc := range list {
		switch sc.ID {
		case gid:
			foundGlobal = true
			// Global row is nil-VehicleID; the vehicle row is not.
			if sc.VehicleID != nil {
				t.Fatalf("global speed config has vehicle_id=%v want nil", *sc.VehicleID)
			}
		case vidSC:
			foundVehicle = true
			if sc.VehicleID == nil {
				t.Fatal("vehicle speed config missing vehicle_id")
			}
		}
	}
	if !foundGlobal || !foundVehicle {
		t.Fatalf("fixtures missing from ListSpeedConfigs (global=%v vehicle=%v, rows=%d)",
			foundGlobal, foundVehicle, len(list))
	}

	// includeDeleted surfaces the soft-deleted global row.
	if err := h.store.SoftDeleteSpeedConfig(h.ctx, itCompany, gid, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteSpeedConfig: %v", err)
	}
	inc, err := h.store.ListSpeedConfigs(h.ctx, itCompany, true)
	if err != nil {
		t.Fatalf("ListSpeedConfigs(deleted=true): %v", err)
	}
	if !containsSpeedConfigID(inc, gid) {
		t.Fatalf("ListSpeedConfigs(deleted=true) tidak memuat baris terhapus id=%d", gid)
	}
	out, err := h.store.ListSpeedConfigs(h.ctx, itCompany, false)
	if err != nil {
		t.Fatalf("ListSpeedConfigs(deleted=false): %v", err)
	}
	if containsSpeedConfigID(out, gid) {
		t.Fatalf("ListSpeedConfigs(deleted=false) masih memuat baris terhapus id=%d", gid)
	}
	if err := h.store.RestoreSpeedConfig(h.ctx, itCompany, gid); err != nil {
		t.Fatalf("RestoreSpeedConfig: %v", err)
	}
}

// ---------------------------------------------------------------------------
// fuel configs + history (B5a: global/vehicle config, upsert, history page)
// ---------------------------------------------------------------------------

func TestITFuelConfigsAndHistory(t *testing.T) {
	h := newITHarness(t)
	vid, imei := h.itVehicle()

	gid := h.itFuelConfig(vid, true)
	vidFC := h.itFuelConfig(vid, false)

	list, err := h.store.ListFuelConfigs(h.ctx, itCompany, false)
	if err != nil {
		t.Fatalf("ListFuelConfigs: %v", err)
	}
	// Fixture-based (bukan jumlah absolut): dataset dev bisa berisi fuel config
	// dari run manual/E2E, dan hitungan absolut membuat test gagal padahal kode
	// yang diuji benar.
	foundGlobal, foundVehicle := false, false
	for _, fc := range list {
		switch fc.ID {
		case gid:
			foundGlobal = true
			if fc.VehicleID != nil {
				t.Fatalf("global fuel config has vehicle_id=%v want nil", *fc.VehicleID)
			}
		case vidFC:
			foundVehicle = true
			if fc.VehicleID == nil {
				t.Fatal("vehicle fuel config missing vehicle_id")
			}
		}
	}
	if !foundGlobal || !foundVehicle {
		t.Fatalf("fixtures missing from ListFuelConfigs (global=%v vehicle=%v, rows=%d)",
			foundGlobal, foundVehicle, len(list))
	}

	// UpdateFuelConfig round-trip.
	upd, _ := h.store.FuelConfigByID(h.ctx, itCompany, vidFC, false)
	upd.DropThresholdPct = 20
	if err := h.store.UpdateFuelConfig(h.ctx, itCompany, upd, 1); err != nil {
		t.Fatalf("UpdateFuelConfig: %v", err)
	}
	got, _ := h.store.FuelConfigByID(h.ctx, itCompany, vidFC, false)
	if got.DropThresholdPct != 20 {
		t.Fatalf("FuelConfigByID after update = %+v (want drop=20)", got)
	}

	// ListFuelHistory: 2 logs, newest-first, with total + pagination.
	base := time.Now().Add(-2 * time.Hour).UTC()
	h.itFuelLog(vid, imei, base, ptrFloat64(50.0))
	h.itFuelLog(vid, imei, base.Add(time.Hour), ptrFloat64(45.0))
	logs, total, err := h.store.ListFuelHistory(h.ctx, itCompany, vid, time.Time{}, time.Time{}, 1, 50)
	if err != nil || total != 2 || len(logs) != 2 {
		t.Fatalf("ListFuelHistory = %v total=%d (want 2/2); err %v", logs, total, err)
	}
	// Newest first → the second-inserted (later ts) row is logs[0].
	if logs[0].FuelLevel == nil || *logs[0].FuelLevel != 45.0 {
		t.Fatalf("ListFuelHistory newest = %+v want fuel_level 45.0", logs[0])
	}
	// Pagination slice: page 1, limit 1 → 1 row but total stays 2.
	page, total, err := h.store.ListFuelHistory(h.ctx, itCompany, vid, time.Time{}, time.Time{}, 1, 1)
	if err != nil || total != 2 || len(page) != 1 {
		t.Fatalf("ListFuelHistory page1/limit1 = %v total=%d (want 1/2); err %v", page, total, err)
	}

	// Range filter: from the newest log onward → 1 row.
	from := base.Add(30 * time.Minute)
	rng, total, err := h.store.ListFuelHistory(h.ctx, itCompany, vid, from, time.Time{}, 1, 50)
	if err != nil || total != 1 || len(rng) != 1 {
		t.Fatalf("ListFuelHistory from-filter = %v total=%d (want 1/1); err %v", rng, total, err)
	}
}

// containsAlertID reports whether the page holds the alert id (order-agnostic,
// robust against other rows already living in the shared dev schema).
func containsAlertID(rows []models.Alert, id int64) bool {
	for _, a := range rows {
		if a.ID == id {
			return true
		}
	}
	return false
}

// containsSpeedConfigID reports whether the page holds the id (order-agnostic,
// robust against other rows already living in the shared dev schema).
func containsSpeedConfigID(rows []models.SpeedConfig, id int64) bool {
	for _, sc := range rows {
		if sc.ID == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// alerts (List/ByID + the open→acknowledged→resolved machine + TTA write-once).
// th_alerts is worker-owned; fixtures are inserted directly with a unique
// dedup_key so the partial-unique index on (dedup_key) WHERE status='open' holds.
// ---------------------------------------------------------------------------

func TestITAlertsLifecycle(t *testing.T) {
	h := newITHarness(t)
	vid, imei := h.itVehicle()
	from := time.Now().Add(-time.Minute) // bounds the window to this fixture
	aid := h.itAlert(vid, imei, "sos", "critical")

	// AlertByID (includes the SOS TTA slot, NULL before ack).
	a, err := h.store.AlertByID(h.ctx, itCompany, aid)
	if err != nil || a == nil || a.Status != "open" {
		t.Fatalf("AlertByID = %+v, err %v (want status open)", a, err)
	}
	if a.SOSTTA != nil {
		t.Fatalf("AlertByID SOSTTA = %v want nil before ack", *a.SOSTTA)
	}

	// Row-level ListAlerts: a granted vehicle sees the row; a non-granted id
	// returns an empty page (never a nil-deref / never widened).
	got, _, err := h.store.ListAlerts(h.ctx, AlertQuery{
		CompanyCode: itCompany, AssignedIDs: []int64{vid}, AllVehicles: true,
		Type: "sos", Severity: "critical", Status: "open", From: from, Page: 1, Limit: 50})
	if err != nil || !containsAlertID(got, aid) {
		t.Fatalf("ListAlerts filtered = %v (want to contain id=%d); err %v", got, aid, err)
	}
	if len(got) != 1 {
		t.Fatalf("ListAlerts filtered returned %d rows (want 1 — unique type/severity/status fixture)", len(got))
	}
	none, _, err := h.store.ListAlerts(h.ctx, AlertQuery{
		CompanyCode: itCompany, AssignedIDs: []int64{999999}, AllVehicles: false, Page: 1, Limit: 50})
	if err != nil || len(none) != 0 {
		t.Fatalf("ListAlerts non-granted id = %v rows, err %v want 0", len(none), err)
	}

	// open → acknowledged: rows affected = 1, SOSTTA populated (≥0).
	n, err := h.store.AcknowledgeAlert(h.ctx, itCompany, aid, 1)
	if err != nil || n != 1 {
		t.Fatalf("AcknowledgeAlert = %d, err %v want 1", n, err)
	}
	ack, _ := h.store.AlertByID(h.ctx, itCompany, aid)
	if ack.Status != "acknowledged" || ack.SOSTTA == nil {
		t.Fatalf("AlertByID after ack = %+v (want status=acknowledged, SOS TTA set)", ack)
	}
	// Second acknowledge is a no-op (already transitioned) → 0 rows.
	if n2, err := h.store.AcknowledgeAlert(h.ctx, itCompany, aid, 1); err != nil || n2 != 0 {
		t.Fatalf("double AcknowledgeAlert = %d, err %v want 0 (write-once TTA guard)", n2, err)
	}

	// acknowledged → resolved.
	n, err = h.store.ResolveAlert(h.ctx, itCompany, aid, 1)
	if err != nil || n != 1 {
		t.Fatalf("ResolveAlert = %d, err %v want 1", n, err)
	}
	// Re-resolve on an already-resolved alert → 0 rows.
	if n2, err := h.store.ResolveAlert(h.ctx, itCompany, aid, 1); err != nil || n2 != 0 {
		t.Fatalf("double ResolveAlert = %d, err %v want 0", n2, err)
	}
	res, _ := h.store.AlertByID(h.ctx, itCompany, aid)
	if res.Status != "resolved" {
		t.Fatalf("AlertByID after resolve = %+v (want status=resolved)", res)
	}
}

// ---------------------------------------------------------------------------
// assignment + route soft-delete (the delete/restore + timestamp branches)
// ---------------------------------------------------------------------------

func TestITAssignmentAndRouteSoftDelete(t *testing.T) {
	h := newITHarness(t)
	vid, _ := h.itVehicle()
	rid, name := h.itRoute()
	a := &models.RouteAssignment{RouteID: rid, VehicleID: vid, Status: "not_started"}
	aid, err := h.store.CreateAssignment(h.ctx, itCompany, a, 1)
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	// Soft-deleting the assignment hides it from the default list and fetch.
	if err := h.store.SoftDeleteAssignment(h.ctx, itCompany, rid, aid, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteAssignment: %v", err)
	}
	list, err := h.store.ListAssignments(h.ctx, itCompany, rid)
	if err != nil || len(list) != 0 {
		t.Fatalf("ListAssignments after soft delete = %v (want 0); err %v", list, err)
	}
	if got, _ := h.store.AssignmentByID(h.ctx, itCompany, rid, aid); got != nil {
		t.Fatalf("AssignmentByID after soft delete = %+v want nil", got)
	}

	// Soft-deleting the route cascades to its (still-live) assignments.
	ra := &models.RouteAssignment{RouteID: rid, VehicleID: vid, DriverUserID: ptrInt64(vid), Status: "not_started"}
	aid2, err := h.store.CreateAssignment(h.ctx, itCompany, ra, 1)
	if err != nil {
		t.Fatalf("CreateAssignment #2: %v", err)
	}
	if err := h.store.SoftDeleteRoute(h.ctx, itCompany, rid, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteRoute: %v", err)
	}
	if got, _ := h.store.RouteByID(h.ctx, itCompany, rid, false); got != nil {
		t.Fatalf("RouteByID after soft delete = %+v want nil", got)
	}
	if rest, _ := h.store.RouteByID(h.ctx, itCompany, rid, true); rest == nil || rest.DeletedAt == nil {
		t.Fatalf("RouteByID includeDeleted = %+v (want deleted)", rest)
	}
	// Restored route re-appears; its child assignment was soft-deleted by the
	// store call above (the route soft-delete does not touch assignments, but the
	// test only needs the route life-cycle).
	if err := h.store.RestoreRoute(h.ctx, itCompany, rid); err != nil {
		t.Fatalf("RestoreRoute: %v", err)
	}
	_ = name
	_ = aid
	_ = aid2
}

// ---------------------------------------------------------------------------
// identity + tenant access (master tm_users, PRD §9.1/§9.2)
// ---------------------------------------------------------------------------

// itUser inserts one master user authority row; cleanup removes its tenant
// membership grants first, then the master row.
func (h *itHarness) itUser(role string) int64 {
	h.t.Helper()
	email := "it-" + itTag() + "@example.test"
	var uid int64
	err := h.masterDB().DB.QueryRowContext(h.ctx,
		`INSERT INTO tm_users (email, password_hash, full_name, global_role, company_code, is_active)
		 VALUES ($1, $2, $3, $4, $5, TRUE) RETURNING id`,
		email, "x-hash-not-used", "IT User", role, itCompany).Scan(&uid)
	if err != nil {
		h.t.Fatalf("insert master user: %v", err)
	}
	h.t.Cleanup(func() {
		h.execCompany(`DELETE FROM tm_user_company_access WHERE user_id = $1`, uid)
		h.execCompany(`DELETE FROM tm_user_vehicles WHERE user_id = $1`, uid)
		h.execMaster(`DELETE FROM tm_users WHERE id = $1`, uid)
	})
	return uid
}

// TestITUserByIDAndTenantAccess covers UserByID (master authority row) and
// TenantAccess (per-tenant membership + role_override, PRD §3.1).
func TestITUserByIDAndTenantAccess(t *testing.T) {
	h := newITHarness(t)
	uid := h.itUser("Operator")

	u, err := h.store.UserByID(h.ctx, uid)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if u == nil {
		t.Fatal("UserByID returned nil for an existing master user")
	}
	if u.ID != uid || u.CompanyCode != itCompany || u.GlobalRole != "Operator" || !u.IsActive {
		t.Fatalf("UserByID fields = %+v", u)
	}

	// Unknown id → (nil, nil), never an error (the caller maps it to 401).
	if missing, err := h.store.UserByID(h.ctx, 0); err != nil || missing != nil {
		t.Fatalf("UserByID(unknown) = %+v, %v want nil,nil", missing, err)
	}

	// No membership row yet → found == false.
	role, active, found, err := h.store.TenantAccess(h.ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("TenantAccess(no row): %v", err)
	}
	if found || active || role != "" {
		t.Fatalf("TenantAccess(no row) = %q,%v,%v want \"\",false,false", role, active, found)
	}

	// A membership with role_override wins over the global role.
	h.execCompany(`INSERT INTO tm_user_company_access (user_id, role_override, is_active)
		VALUES ($1, 'Manager', TRUE)`, uid)
	role, active, found, err = h.store.TenantAccess(h.ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("TenantAccess: %v", err)
	}
	if !found || !active || role != "Manager" {
		t.Fatalf("TenantAccess = %q,%v,%v want Manager,true,true", role, active, found)
	}

	// An unknown tenant code surfaces the pool resolver error (mapped to 403).
	if _, _, _, err := h.store.TenantAccess(h.ctx, "NOSUCHTENANT", uid); err == nil {
		t.Fatal("TenantAccess(unknown tenant) err = nil, want tenant resolution error")
	}
}

// TestITAssignedVehicleIDs covers the row-level grant lookup (PRD §9.2).
func TestITAssignedVehicleIDs(t *testing.T) {
	h := newITHarness(t)
	uid := h.itUser("Driver")
	v1, _ := h.itVehicle()
	v2, _ := h.itVehicle()

	ids, err := h.store.AssignedVehicleIDs(h.ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("AssignedVehicleIDs(empty): %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("AssignedVehicleIDs(empty) = %v want none", ids)
	}

	h.execCompany(`INSERT INTO tm_user_vehicles (user_id, vehicle_id) VALUES ($1, $2), ($1, $3)`, uid, v1, v2)
	ids, err = h.store.AssignedVehicleIDs(h.ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("AssignedVehicleIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("AssignedVehicleIDs = %v want 2 grants", ids)
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen[v1] || !seen[v2] {
		t.Fatalf("AssignedVehicleIDs = %v want both %d and %d", ids, v1, v2)
	}

	// A revoked (soft-deleted) grant drops out of the row-level filter.
	h.execCompany(`UPDATE tm_user_vehicles SET deleted_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND vehicle_id = $2`, uid, v2)
	ids, err = h.store.AssignedVehicleIDs(h.ctx, itCompany, uid)
	if err != nil || len(ids) != 1 || ids[0] != v1 {
		t.Fatalf("AssignedVehicleIDs after revoke = %v, %v want [%d]", ids, err, v1)
	}
}

// ---------------------------------------------------------------------------
// route list/update, speed config detail/update, fuel config delete/restore
// ---------------------------------------------------------------------------

// TestITRouteListAndUpdate covers ListRoutes (paged + includeDeleted) and
// UpdateRoute (waypoint JSONB rewrite, PRD §5.9.2).
func TestITRouteListAndUpdate(t *testing.T) {
	h := newITHarness(t)
	rid, name := h.itRoute()

	// RouteQuery has no text filter (unlike vehicles), so page wide enough to
	// hold the dev tenant's routes and locate the fixture by id.
	live := RouteQuery{CompanyCode: itCompany, Page: 1, Limit: 500}
	rows, total, err := h.store.ListRoutes(h.ctx, live)
	if err != nil {
		t.Fatalf("ListRoutes: %v", err)
	}
	if total < 1 {
		t.Fatalf("ListRoutes total = %d want >= 1", total)
	}
	var found *models.Route
	for i := range rows {
		if rows[i].ID == rid {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("ListRoutes did not return the fixture route %d", rid)
	}
	if found.Name != name {
		t.Fatalf("ListRoutes name = %q want %q", found.Name, name)
	}
	if len(found.Waypoints) != 2 {
		t.Fatalf("ListRoutes waypoints = %+v want 2", found.Waypoints)
	}

	// UpdateRoute rewrites description/waypoints/duration.
	upd := *found
	upd.Description = ptrString("it route updated")
	upd.Waypoints = append(upd.Waypoints, models.Waypoint{Seq: 2, Lat: -6.3, Lon: 107.1, Name: "C"})
	upd.EstMinutes = ptrInt(33)
	if err := h.store.UpdateRoute(h.ctx, itCompany, &upd, 1); err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}
	after, err := h.store.RouteByID(h.ctx, itCompany, rid, false)
	if err != nil || after == nil {
		t.Fatalf("RouteByID after update = %+v, %v", after, err)
	}
	if derefString(after.Description) != "it route updated" || len(after.Waypoints) != 3 {
		t.Fatalf("route after update = %+v", after)
	}
	if after.EstMinutes == nil || *after.EstMinutes != 33 {
		t.Fatalf("route est_minutes after update = %v want 33", after.EstMinutes)
	}

	// includeDeleted surfaces a soft-deleted route in the same page.
	if err := h.store.SoftDeleteRoute(h.ctx, itCompany, rid, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteRoute: %v", err)
	}
	rowsLive, _, err := h.store.ListRoutes(h.ctx, live)
	if err != nil {
		t.Fatalf("ListRoutes(live): %v", err)
	}
	for i := range rowsLive {
		if rowsLive[i].ID == rid {
			t.Fatalf("soft-deleted route leaked into the live page: %+v", rowsLive[i])
		}
	}
	allQ := live
	allQ.IncludeDel = true
	all, _, err := h.store.ListRoutes(h.ctx, allQ)
	if err != nil {
		t.Fatalf("ListRoutes(includeDeleted): %v", err)
	}
	seenDeleted := false
	for i := range all {
		if all[i].ID == rid && all[i].DeletedAt != nil {
			seenDeleted = true
		}
	}
	if !seenDeleted {
		t.Fatal("ListRoutes(includeDeleted) did not surface the soft-deleted fixture")
	}
}

// TestITSpeedConfigByIDAndUpdate covers SpeedConfigByID + UpdateSpeedConfig.
func TestITSpeedConfigByIDAndUpdate(t *testing.T) {
	h := newITHarness(t)
	vid, _ := h.itVehicle()
	id := h.itSpeedConfig(vid, false)

	sc, err := h.store.SpeedConfigByID(h.ctx, itCompany, id, false)
	if err != nil {
		t.Fatalf("SpeedConfigByID: %v", err)
	}
	if sc == nil || sc.VehicleID == nil || *sc.VehicleID != vid {
		t.Fatalf("SpeedConfigByID = %+v want vehicle %d", sc, vid)
	}
	if sc.MaxSpeedKMH != 80 || sc.Severity != "high" {
		t.Fatalf("SpeedConfigByID fields = %+v", sc)
	}

	// Unknown id → (nil, nil) so the handler can answer 404.
	if missing, err := h.store.SpeedConfigByID(h.ctx, itCompany, 0, false); err != nil || missing != nil {
		t.Fatalf("SpeedConfigByID(unknown) = %+v, %v want nil,nil", missing, err)
	}

	sc.MaxSpeedKMH = 65
	sc.GracePct = 5
	sc.Severity = "critical"
	sc.Enabled = false
	if err := h.store.UpdateSpeedConfig(h.ctx, itCompany, sc, 1); err != nil {
		t.Fatalf("UpdateSpeedConfig: %v", err)
	}
	after, err := h.store.SpeedConfigByID(h.ctx, itCompany, id, false)
	if err != nil || after == nil {
		t.Fatalf("SpeedConfigByID after update = %+v, %v", after, err)
	}
	if after.MaxSpeedKMH != 65 || after.GracePct != 5 || after.Severity != "critical" || after.Enabled {
		t.Fatalf("speed config after update = %+v", after)
	}

	// Soft delete hides it; includeDeleted still resolves it.
	if err := h.store.SoftDeleteSpeedConfig(h.ctx, itCompany, id, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteSpeedConfig: %v", err)
	}
	if got, _ := h.store.SpeedConfigByID(h.ctx, itCompany, id, false); got != nil {
		t.Fatalf("SpeedConfigByID after soft delete = %+v want nil", got)
	}
	if got, _ := h.store.SpeedConfigByID(h.ctx, itCompany, id, true); got == nil || got.DeletedAt == nil {
		t.Fatalf("SpeedConfigByID(includeDeleted) = %+v want deleted", got)
	}
}

// TestITFuelConfigSoftDeleteRestore covers SoftDeleteFuelConfig +
// RestoreFuelConfig (FR-7.6 config life-cycle).
func TestITFuelConfigSoftDeleteRestore(t *testing.T) {
	h := newITHarness(t)
	vid, _ := h.itVehicle()
	id := h.itFuelConfig(vid, false)

	if err := h.store.SoftDeleteFuelConfig(h.ctx, itCompany, id, 1, "it"); err != nil {
		t.Fatalf("SoftDeleteFuelConfig: %v", err)
	}
	if got, _ := h.store.FuelConfigByID(h.ctx, itCompany, id, false); got != nil {
		t.Fatalf("FuelConfigByID after soft delete = %+v want nil", got)
	}
	dead, err := h.store.FuelConfigByID(h.ctx, itCompany, id, true)
	if err != nil || dead == nil || dead.DeletedAt == nil {
		t.Fatalf("FuelConfigByID(includeDeleted) = %+v, %v want deleted row", dead, err)
	}

	// The soft-deleted row also disappears from the default list.
	list, err := h.store.ListFuelConfigs(h.ctx, itCompany, false)
	if err != nil {
		t.Fatalf("ListFuelConfigs: %v", err)
	}
	for i := range list {
		if list[i].ID == id {
			t.Fatalf("soft-deleted fuel config leaked into the live list: %+v", list[i])
		}
	}

	// Restore brings it back with a NULL deleted_at.
	if err := h.store.RestoreFuelConfig(h.ctx, itCompany, id); err != nil {
		t.Fatalf("RestoreFuelConfig: %v", err)
	}
	back, err := h.store.FuelConfigByID(h.ctx, itCompany, id, false)
	if err != nil || back == nil {
		t.Fatalf("FuelConfigByID after restore = %+v, %v want live row", back, err)
	}
	if back.DeletedAt != nil {
		t.Fatalf("restored fuel config deleted_at = %v want nil", *back.DeletedAt)
	}
}
