package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// --- fakes ----------------------------------------------------------------

// stubLive is a LiveStateReader backed by a map (no Redis in unit tests).
type stubLive struct {
	prefix   string
	states   map[string]string
	err      error
	calls    int
	lastKeys []string
}

func newStubLive() *stubLive {
	return &stubLive{prefix: DefaultRedisKeyPrefix, states: map[string]string{}}
}

func (s *stubLive) LiveStateKey(companyCode, imei string) string {
	return s.prefix + lowerCompany(companyCode) + ":vehicle:state:" + imei
}

func (s *stubLive) MGet(_ context.Context, keys ...string) ([]string, error) {
	s.calls++
	s.lastKeys = keys
	if s.err != nil {
		return nil, s.err
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = s.states[k]
	}
	return out, nil
}

func (s *stubLive) set(companyCode, imei string, state models.LiveState) {
	ls := s.LiveStateKey(companyCode, imei)
	raw, _ := json.Marshal(state)
	s.states[ls] = string(raw)
}

// errLiveState makes MGet return an error.
type errLiveState struct{}

func (errLiveState) LiveStateKey(string, string) string { return "" }
func (errLiveState) MGet(_ context.Context, _ ...string) ([]string, error) {
	return nil, errors.New("redis unavailable")
}

// lowerCompany mirrors the normalisation worker-live applies.
func lowerCompany(code string) string {
	if code == "" {
		return "default"
	}
	out := make([]byte, 0, len(code))
	for i := 0; i < len(code); i++ {
		b := code[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if b != ' ' {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}

// --- unit tests for applyLiveState (no HTTP) ------------------------------

// TestApplyLiveStateNilSafe: nil vehicle must not panic.
func TestApplyLiveStateNilSafe(t *testing.T) {
	var v *models.Vehicle
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil vehicle panicked: %v", r)
		}
	}()
	applyLiveState(v, nil)
	applyLiveState(v, &models.LiveState{IMEI: "x"})
}

// TestApplyLiveStateFuelOnlyDoesNotErasePosition: a fuel-only live state
// (Lat/Lon == 0, worker-live mergeFuelState) must NOT zero an existing DB
// position (FR-2.3).
func TestApplyLiveStateFuelOnlyDoesNotErasePosition(t *testing.T) {
	lat, lon := 3.5, 98.2
	v := models.Vehicle{CurrentLat: &lat, CurrentLon: &lon}

	applyLiveState(&v, &models.LiveState{
		IMEI: "x", FuelLevel: f64(61.25), Status: "IDLE",
	})

	if v.CurrentLat == nil || *v.CurrentLat != lat {
		t.Errorf("CurrentLat clobbered to %v", v.CurrentLat)
	}
	if v.CurrentLon == nil || *v.CurrentLon != lon {
		t.Errorf("CurrentLon clobbered to %v", v.CurrentLon)
	}
	if v.Live == nil || v.Live.FuelLevel == nil || *v.Live.FuelLevel != 61.25 {
		t.Error("live fuel_level must still be applied")
	}
}

// TestApplyLiveStateFullPositionOverlays: a full telemetry with lat/lon>0 does
// overwrite the DB position (the vehicle moved).
func TestApplyLiveStateFullPositionOverlays(t *testing.T) {
	lat, lon := 3.5, 98.2
	v := models.Vehicle{CurrentLat: &lat, CurrentLon: &lon}

	applyLiveState(&v, &models.LiveState{
		IMEI: "x", Lat: 4.0, Lon: 97.0, Speed: 50, Fix: true, Status: "ONLINE",
	})

	if v.CurrentLat == nil || *v.CurrentLat != 4.0 {
		t.Errorf("CurrentLat = %v, want 4.0", v.CurrentLat)
	}
	if v.CurrentLon == nil || *v.CurrentLon != 97.0 {
		t.Errorf("CurrentLon = %v, want 97.0", v.CurrentLon)
	}
	if v.CurrentSpeed == nil || *v.CurrentSpeed != 50 {
		t.Errorf("CurrentSpeed = %v, want 50", v.CurrentSpeed)
	}
}

// TestApplyLiveStateLastSeenMirror
func TestApplyLiveStateLastSeenMirror(t *testing.T) {
	v := models.Vehicle{}
	applyLiveState(&v, &models.LiveState{LastSeen: 1760000000, Status: "IDLE"})
	if v.LastSeenAt == nil {
		t.Fatal("LastSeenAt should be populated from last_seen")
	}
}

// pBool is a test helper that returns a *bool.
func pBool(v bool) *bool { return &v }

// --- RedisKV key layout ---------------------------------------------------

// TestRedisKVLiveStateKeyMatchesWorkerLayout: api-vehicle and worker-live must
// agree on the live-state key layout (FR-2.1), otherwise the overlay silently
// reads empty keys.
func TestRedisKVLiveStateKeyMatchesWorkerLayout(t *testing.T) {
	def := NewRedisKVWithPrefix(nil, "")
	if got := def.LiveStateKey("DEV001", "864201040512345"); got != "adatrack_gps:dev001:vehicle:state:864201040512345" {
		t.Errorf("default prefix key = %q", got)
	}
	custom := NewRedisKVWithPrefix(nil, "adatrack:prod:")
	if got := custom.LiveStateKey("DEV001", "9"); got != "adatrack:prod:dev001:vehicle:state:9" {
		t.Errorf("custom prefix key = %q", got)
	}
	if NewRedisKV(nil).LiveStateKey("DEV001", "9") != def.LiveStateKey("DEV001", "9") {
		t.Error("NewRedisKV must default to the canonical prefix")
	}
}

// TestRedisKVMGetEmpty: an empty key list is a no-op (no round trip).
func TestRedisKVMGetEmpty(t *testing.T) {
	kv := NewRedisKVWithPrefix(nil, DefaultRedisKeyPrefix)
	out, err := kv.MGet(context.Background())
	if err != nil || len(out) != 0 {
		t.Errorf("MGet(empty) = %v, %v", out, err)
	}
}

// --- end-to-end overlay via the HTTP handlers -----------------------------

// TestVehicleDetailLiveOverlayFuel: detail merges fuel + real position.
func TestVehicleDetailLiveOverlayFuel(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 3, IMEI: "864201040512345", PlateNumber: "B 1 Z",
		Status: "active"})

	live := newStubLive()
	live.set("DEV001", "864201040512345", models.LiveState{
		IMEI: "864201040512345", FuelLevel: f64(61.25), FuelVolume: f64(275.5),
		FuelTempC: f64(29.75), Battery: 88, GsmSignal: 22,
		Lat: 3.5, Lon: 98.2, Speed: 48.5, Status: "ONLINE", VehicleID: 3,
		ACC: pBool(true), Fix: true, Heading: 90, Satellites: 9, Altitude: 40,
		Mileage: 1200, LastSeen: 1760000000, Timestamp: 1760000000,
	})

	svc := newTestServiceWithLive(store, live)
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles/3", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	svc.handleVehicleDetail(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data models.Vehicle `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode vehicle detail: %v", err)
	}
	ls := env.Data.Live
	if ls == nil {
		t.Fatal("live overlay missing on detail")
	}
	if ls.FuelLevel == nil || *ls.FuelLevel != 61.25 {
		t.Errorf("fuel_level = %v, want 61.25", ls.FuelLevel)
	}
	if ls.FuelVolume == nil || *ls.FuelVolume != 275.5 || ls.FuelTempC == nil || *ls.FuelTempC != 29.75 {
		t.Errorf("fuel volume/temp = %v/%v", ls.FuelVolume, ls.FuelTempC)
	}
	if ls.ACC == nil || !*ls.ACC {
		t.Errorf("acc = %v, want true", ls.ACC)
	}
	if ls.Battery != 88 || ls.GsmSignal != 22 {
		t.Errorf("battery/gsm = %d/%d, want 88/22", ls.Battery, ls.GsmSignal)
	}
	if ls.Status != "ONLINE" || ls.VehicleID != 3 || ls.Speed != 48.5 {
		t.Errorf("status/vehicle_id/speed = %q/%d/%v", ls.Status, ls.VehicleID, ls.Speed)
	}
	if env.Data.CurrentLat == nil || *env.Data.CurrentLat != 3.5 {
		t.Errorf("CurrentLat = %v, want 3.5 (overlaid by live state)", env.Data.CurrentLat)
	}
	if live.calls != 1 {
		t.Errorf("expected a single batched MGet, got %d", live.calls)
	}
}

// TestVehicleDetailWithoutLiveKeepsDBMirrors
func TestVehicleDetailWithoutLiveKeepsDBMirrors(t *testing.T) {
	lat, lon := 3.5, 98.2
	lastSeen := "2026-09-16T00:00:00Z"
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 3, IMEI: "864201040512345", PlateNumber: "B 1 Z",
		Status: "active", CurrentLat: &lat, CurrentLon: &lon, LastSeenAt: &lastSeen})

	svc := newTestServiceWithLive(store, newStubLive())
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles/3", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	svc.handleVehicleDetail(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data models.Vehicle `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode vehicle detail: %v", err)
	}
	if env.Data.Live != nil {
		t.Error("live block must be nil when Redis has no state")
	}
	if env.Data.CurrentLat == nil || *env.Data.CurrentLat != lat {
		t.Errorf("CurrentLat = %v, DB mirror must be preserved", env.Data.CurrentLat)
	}
	if env.Data.LastSeenAt == nil || *env.Data.LastSeenAt != lastSeen {
		t.Error("LastSeenAt must be preserved")
	}
}

// TestVehicleListBatchOverlay: two vehicles enriched in ONE MGet.
func TestVehicleListBatchOverlay(t *testing.T) {
	store := newFakeStore()
	store.seedListedVehicles(
		models.Vehicle{ID: 1, IMEI: "864201040512345", PlateNumber: "B 1 A", Status: "active"},
		models.Vehicle{ID: 2, IMEI: "864201040512346", PlateNumber: "B 2 B", Status: "active"},
	)

	live := newStubLive()
	live.set("DEV001", "864201040512345", models.LiveState{IMEI: "864201040512345", FuelLevel: f64(60)})
	live.set("DEV001", "864201040512346", models.LiveState{IMEI: "864201040512346", FuelLevel: f64(70)})

	svc := newTestServiceWithLive(store, live)
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())

	svc.handleListVehicles(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if live.calls != 1 {
		t.Errorf("expected one batched MGet, got %d", live.calls)
	}
	if len(live.lastKeys) != 2 {
		t.Errorf("expected 2 keys in the batch, got %d", len(live.lastKeys))
	}
	var env struct {
		Data []models.Vehicle `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode vehicle list: %v", err)
	}
	if len(env.Data) != 2 {
		t.Fatalf("vehicles = %d, want 2", len(env.Data))
	}
	if env.Data[0].Live == nil || env.Data[1].Live == nil {
		t.Error("both vehicles must be enriched")
	}
	if env.Data[0].Live.FuelLevel == nil || *env.Data[0].Live.FuelLevel != 60 {
		t.Errorf("v0 fuel = %v, want 60", env.Data[0].Live.FuelLevel)
	}
	if env.Data[1].Live.FuelLevel == nil || *env.Data[1].Live.FuelLevel != 70 {
		t.Errorf("v1 fuel = %v, want 70", env.Data[1].Live.FuelLevel)
	}
}

// TestLiveOverlayFuelOnlyDoesNotErasePosition: list endpoint, fuel-only state.
func TestLiveOverlayFuelOnlyDoesNotErasePosition(t *testing.T) {
	lat, lon, lastSeen := 3.5, 98.2, "2026-09-16T00:00:00Z"
	store := newFakeStore()
	store.seedListedVehicles(models.Vehicle{
		ID: 3, IMEI: "864201040512345", PlateNumber: "B 1 Z",
		Status: "active", CurrentLat: &lat, CurrentLon: &lon, LastSeenAt: &lastSeen,
	})

	live := newStubLive()
	live.set("DEV001", "864201040512345", models.LiveState{
		IMEI: "864201040512345", FuelLevel: f64(61.25), Status: "IDLE",
	})

	svc := newTestServiceWithLive(store, live)
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())

	svc.handleListVehicles(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []models.Vehicle `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode vehicle list: %v", err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("vehicles = %d, want 1", len(env.Data))
	}
	v := env.Data[0]
	if v.Live == nil || v.Live.FuelLevel == nil || *v.Live.FuelLevel != 61.25 {
		t.Errorf("fuel_level = %v, want 61.25", v.Live)
	}
	if v.CurrentLat == nil || *v.CurrentLat != lat {
		t.Errorf("CurrentLat = %v, DB position must be preserved on fuel-only", v.CurrentLat)
	}
}

// TestLiveOverlayDegradesGracefully: Redis error → 200, live absent.
func TestLiveOverlayDegradesGracefully(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 3, IMEI: "864201040512345", PlateNumber: "B 1 Z",
		Status: "active"})

	svc := newTestServiceWithLive(store, &errLiveState{})
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles/3", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	svc.handleVehicleDetail(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 on a Redis outage (read must degrade, not fail)", rec.Code)
	}
	var env struct {
		Data models.Vehicle `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode vehicle detail: %v", err)
	}
	if env.Data.Live != nil {
		t.Error("live must stay nil when Redis is unavailable")
	}
}

// TestLiveOverlaySkipsCorruptPayload: unreadable JSON is skipped.
func TestLiveOverlaySkipsCorruptPayload(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 5, IMEI: "864201040512345", PlateNumber: "B 1 A",
		Status: "active"})

	live := newStubLive()
	live.states[live.LiveStateKey("DEV001", "864201040512345")] = "{not-json"

	svc := newTestServiceWithLive(store, live)
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles/5", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "5"}}

	svc.handleVehicleDetail(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var env struct {
		Data models.Vehicle `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode vehicle detail: %v", err)
	}
	if env.Data.Live != nil {
		t.Error("corrupt live payload must be skipped, not surfaced")
	}
}

// TestLiveStateJSONRoundTrip pins the wire shape shared with worker-live /
// service-websocket: the Redis JSON must decode into models.LiveState as-is.
func TestLiveStateJSONRoundTrip(t *testing.T) {
	raw := `{"imei":"888","company_code":"DEV001","vehicle_id":3,"lat":-6.2,"lon":106.8,` +
		`"speed":48.5,"heading":90,"satellites":9,"altitude":40,"battery_level":88,` +
		`"gsm_signal":22,"acc":true,"mileage":1200,"fix":true,"status":"ONLINE",` +
		`"last_seen":1760000000,"timestamp":1760000000,"fuel_level":61.25,` +
		`"fuel_volume":275.5,"fuel_temp_c":29.75}`
	var ls models.LiveState
	if err := json.Unmarshal([]byte(raw), &ls); err != nil {
		t.Fatalf("decode worker-live payload: %v", err)
	}
	if ls.ACC == nil || !*ls.ACC {
		t.Errorf("acc = %v, want true", ls.ACC)
	}
	if ls.FuelLevel == nil || *ls.FuelLevel != 61.25 {
		t.Errorf("fuel_level = %v, want 61.25", ls.FuelLevel)
	}
	if ls.FuelVolume == nil || *ls.FuelVolume != 275.5 || ls.FuelTempC == nil || *ls.FuelTempC != 29.75 {
		t.Errorf("fuel volume/temp = %v/%v", ls.FuelVolume, ls.FuelTempC)
	}
	if ls.Battery != 88 || ls.GsmSignal != 22 {
		t.Errorf("battery/gsm = %d/%d, want 88/22", ls.Battery, ls.GsmSignal)
	}
	if ls.Status != "ONLINE" || ls.VehicleID != 3 || ls.Speed != 48.5 {
		t.Errorf("status/vehicle_id/speed = %q/%d/%v", ls.Status, ls.VehicleID, ls.Speed)
	}
}
