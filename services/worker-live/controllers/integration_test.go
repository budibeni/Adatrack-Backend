package controllers

// Integration suite for the live-state writer (B4: unit/integration coverage).
//
// These tests exercise the REAL Redis + NATS collaborators of worker-live
// (batch MSET, fuel merge, staleness sweep, fan-out) against the dev infra
// started by scripts/start-services.sh. They are OPT-IN so `go test ./...`
// stays hermetic: run them with ADATRACK_IT=1 (Makefile: coverage /
// scripts/b4-verify.sh export the variable when the infra ports are reachable).
//
// Every key this suite writes lives under a dedicated Redis namespace
// (adatrack_gps_it:), so the sweeper scans only its own fixtures and no state
// of a running worker-live instance is touched.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/worker-live/models"
)

const itKeyPrefix = "adatrack_gps_it:"

// itEnv reads an opt-in override or falls back to the local dev bind port.
func itEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// itConfig points the shared config at the host-published infra ports
// (HOST_PG_PORT/HOST_REDIS_PORT in .env.local) and isolates the Redis namespace.
func itConfig(t *testing.T) *internal.Config {
	t.Helper()
	if os.Getenv("ADATRACK_IT") != "1" {
		t.Skip("integration test — set ADATRACK_IT=1 with live Redis/NATS (see Makefile: coverage)")
	}
	t.Setenv("POSTGRES_HOST", itEnv("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", itEnv("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", itEnv("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", itEnv("ADATRACK_IT_REDIS_PORT", "6380"))
	t.Setenv("NATS_URL", itEnv("ADATRACK_IT_NATS_URL", "nats://127.0.0.1:4222"))
	t.Setenv("REDIS_KEY_PREFIX", itKeyPrefix)
	t.Setenv("LIVE_MAX_BATCH", "4")
	t.Setenv("LIVE_BATCH_INTERVAL_MS", "50")

	cfg := internal.LoadConfig()
	cfg.Live.MaxBatch = 4
	cfg.Live.BatchInterval = 50 * time.Millisecond
	cfg.Live.IdleAfter = 90 * time.Second
	cfg.Live.OfflineAfterMinutes = 3
	return cfg
}

// itWorker bundles the worker under test with its live collaborators.
type itWorker struct {
	t   *testing.T
	cfg *internal.Config
	red *internal.RedisClient
	nac *internal.NATSClient
	w   *Worker
}

// newITWorker wires a worker on real Redis + NATS and tears everything down
// (including the test namespace) after the test.
func newITWorker(t *testing.T) *itWorker {
	t.Helper()
	cfg := itConfig(t)

	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		t.Fatalf("redis unavailable on %s: %v", cfg.RedisAddr(), err)
	}
	nac, err := internal.NewNATSClient(cfg)
	if err != nil {
		_ = red.Close()
		t.Fatalf("nats unavailable on %s: %v", cfg.NATS.URL, err)
	}
	w := New(cfg, red, nac, nil)

	it := &itWorker{t: t, cfg: cfg, red: red, nac: nac, w: w}
	t.Cleanup(func() {
		w.Stop()
		it.purge()
		nac.Close()
		_ = red.Close()
	})
	return it
}

// purge removes every key of the test namespace.
func (it *itWorker) purge() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys, err := it.red.ScanKeys(ctx, it.cfg.Redis.KeyPrefix+"*", 1000)
	if err != nil {
		return
	}
	for _, k := range keys {
		_ = it.red.Del(ctx, k)
	}
}

// stateKey is the live-state key of one fixture device.
func (it *itWorker) stateKey(imei string) string {
	return it.red.LiveStateKey("DEV001", imei)
}

// put writes a raw value into the test namespace.
func (it *itWorker) put(key, value string) {
	it.t.Helper()
	if err := it.red.Set(context.Background(), key, value, time.Minute); err != nil {
		it.t.Fatalf("seed redis %s: %v", key, err)
	}
}

// get reads a raw value from the test namespace.
func (it *itWorker) get(key string) string {
	it.t.Helper()
	raw, err := it.red.Get(context.Background(), key)
	if err != nil {
		it.t.Fatalf("read redis %s: %v", key, err)
	}
	return raw
}

// waitState polls Redis until the key holds a decodable live state.
func (it *itWorker) waitState(key string) models.LiveState {
	it.t.Helper()
	var st models.LiveState
	it.waitFor("live state "+key, func() bool {
		raw, err := it.red.Get(context.Background(), key)
		if err != nil || raw == "" {
			return false
		}
		return json.Unmarshal([]byte(raw), &st) == nil
	})
	return st
}

// waitFor polls cond until it holds or the deadline expires.
func (it *itWorker) waitFor(what string, cond func() bool) {
	it.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	it.t.Fatalf("timed out waiting for %s", what)
}

// waitStateFor polls Redis until the stored live state satisfies cond (used when
// a seed already holds a decodable — but not yet updated — value).
func (it *itWorker) waitStateFor(key string, cond func(models.LiveState) bool) models.LiveState {
	it.t.Helper()
	var st models.LiveState
	it.waitFor("live state "+key, func() bool {
		raw, err := it.red.Get(context.Background(), key)
		if err != nil || raw == "" {
			return false
		}
		if json.Unmarshal([]byte(raw), &st) != nil {
			return false
		}
		return cond(st)
	})
	return st
}

// liveFrames subscribes to `telemetry.live.<imei>` and records every frame.
func (it *itWorker) liveFrames(imei string) *frameSink {
	it.t.Helper()
	sink := &frameSink{ch: make(chan map[string]any, 32)}
	sub, err := it.nac.Subscribe(it.nac.Subject("live", imei), "it-live-"+imei, func(msg *nats.Msg) error {
		var frame map[string]any
		if err := json.Unmarshal(msg.Data, &frame); err != nil {
			return err
		}
		select {
		case sink.ch <- frame:
		default:
		}
		return nil
	})
	if err != nil {
		it.t.Fatalf("subscribe live subject: %v", err)
	}
	it.t.Cleanup(func() { it.nac.Unsubscribe(sub) })
	return sink
}

// frameSink collects telemetry.live frames for assertions.
type frameSink struct {
	mu       sync.Mutex
	received []map[string]any
	ch       chan map[string]any
}

// drain moves every buffered frame into the slice.
func (s *frameSink) drain() {
	for {
		select {
		case f := <-s.ch:
			s.mu.Lock()
			s.received = append(s.received, f)
			s.mu.Unlock()
		default:
			return
		}
	}
}

// count reports how many frames arrived (after draining the channel).
func (s *frameSink) count() int {
	s.drain()
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.received)
}

// first returns the first received frame (nil when none arrived).
func (s *frameSink) first() map[string]any {
	s.drain()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.received) == 0 {
		return nil
	}
	return s.received[0]
}

// telemetryFrame builds the NATS raw message for one telemetry payload.
func telemetryFrame(t *testing.T, subject string, msg models.TelemetryMessage) *nats.Msg {
	t.Helper()
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal telemetry: %v", err)
	}
	return &nats.Msg{Subject: subject, Data: payload}
}

// positionMessage is a moving position frame.
func positionMessage(imei string) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI: imei, CompanyCode: "DEV001", VehicleID: 71,
		Lat: -6.2088, Lon: 106.8456, Speed: 42.5, Heading: 90,
		Satellites: 9, Altitude: 120, Battery: 13, GsmSignal: 4,
		ACC: models.BoolPtr(true), Mileage: 1000, Fix: true,
		Timestamp: time.Now().Unix(),
	}
}

// TestITHandleMessageFlushesLiveState covers the FR-2.3 batch path end to end:
// decode → buffer → ONE MSET flush → Redis value + telemetry.live fan-out.
func TestITHandleMessageFlushesLiveState(t *testing.T) {
	it := newITWorker(t)
	sink := it.liveFrames("860000009900001")

	msg := positionMessage("860000009900001")
	if err := it.w.handleMessage(telemetryFrame(t, it.nac.Subject("raw", msg.IMEI), msg)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	it.w.mu.Lock()
	buffered := len(it.w.buffer)
	it.w.mu.Unlock()
	if buffered != 1 {
		t.Fatalf("buffered %d live states, want 1", buffered)
	}

	it.w.flushBuffer()
	state := it.waitState(it.stateKey(msg.IMEI))

	if state.Lat != msg.Lat || state.Lon != msg.Lon || state.Speed != msg.Speed {
		t.Errorf("position/speed not persisted: %+v", state)
	}
	if state.Status != models.StatusOnline {
		t.Errorf("status = %s, want ONLINE", state.Status)
	}
	if state.ACC == nil || !*state.ACC {
		t.Errorf("ACC lost: %+v", state)
	}
	if state.Satellites != 9 || state.Altitude != 120 || state.GsmSignal != 4 || state.Battery != 13 {
		t.Errorf("telemetry detail lost: %+v", state)
	}
	if state.LastSeen == 0 || state.CompanyCode != "DEV001" {
		t.Errorf("LastSeen/CompanyCode not stamped: %+v", state)
	}

	it.w.mu.Lock()
	remaining := len(it.w.buffer)
	it.w.mu.Unlock()
	if remaining != 0 {
		t.Errorf("buffer not cleared after flush (%d entries)", remaining)
	}

	it.waitFor("telemetry.live frame", func() bool { return sink.count() > 0 })
	frame := sink.first()
	if frame["imei"] != msg.IMEI {
		t.Errorf("live frame imei = %v, want %s", frame["imei"], msg.IMEI)
	}
	if lat, _ := frame["lat"].(float64); lat != msg.Lat {
		t.Errorf("live frame lat = %v, want %v", frame["lat"], msg.Lat)
	}
}

// TestITHandleMessageMalformedPayloadIsIgnored documents that a corrupt NATS
// payload never reaches the live state (it is counted at ingestion) and that the
// handler stays error-free so processing continues.
func TestITHandleMessageMalformedPayloadIsIgnored(t *testing.T) {
	it := newITWorker(t)
	if err := it.w.handleMessage(&nats.Msg{Subject: "telemetry.raw.bad", Data: []byte("{not json")}); err != nil {
		t.Fatalf("handleMessage returned %v, want nil for a malformed payload", err)
	}
	it.w.mu.Lock()
	buffered := len(it.w.buffer)
	it.w.mu.Unlock()
	if buffered != 0 {
		t.Errorf("malformed payload buffered %d entries", buffered)
	}
}

// TestITHandleMessageStampsMissingTimestamp covers the server-side fallback for
// a payload without a device timestamp.
func TestITHandleMessageStampsMissingTimestamp(t *testing.T) {
	it := newITWorker(t)
	msg := positionMessage("860000009900002")
	msg.Timestamp = 0
	if err := it.w.handleMessage(telemetryFrame(t, "telemetry.raw."+msg.IMEI, msg)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	it.w.flushBuffer()
	state := it.waitState(it.stateKey(msg.IMEI))
	if state.Timestamp == 0 {
		t.Error("Timestamp must fall back to the server receive time")
	}
}

// TestITFuelOnlyMergeKeepsPosition covers FR-2.3 partial merge: a fuel-only
// frame updates fuel + LastSeen without touching position/speed/status.
func TestITFuelOnlyMergeKeepsPosition(t *testing.T) {
	it := newITWorker(t)
	imei := "860000009900003"
	key := it.stateKey(imei)

	seed := models.LiveState{
		IMEI: imei, CompanyCode: "DEV001", VehicleID: 71,
		Lat: -6.2, Lon: 106.8, Speed: 30, Status: models.StatusOnline,
		LastSeen: time.Now().Unix() - 5, Timestamp: time.Now().Unix() - 5,
	}
	raw, _ := json.Marshal(seed)
	it.put(key, string(raw))

	level := 41.5
	temp := 27.0
	fuelOnly := models.TelemetryMessage{
		IMEI: imei, CompanyCode: "DEV001", VehicleID: 71,
		FuelLevel: &level, FuelTempC: &temp, Timestamp: time.Now().Unix(),
	}
	if err := it.w.handleMessage(telemetryFrame(t, "telemetry.raw."+imei, fuelOnly)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	it.w.flushBuffer()
	state := it.waitStateFor(key, func(st models.LiveState) bool {
		return st.FuelLevel != nil && *st.FuelLevel == level
	})

	if state.Lat != seed.Lat || state.Lon != seed.Lon || state.Speed != seed.Speed {
		t.Errorf("fuel-only frame overwrote the position: %+v", state)
	}
	if state.FuelLevel == nil || *state.FuelLevel != level {
		t.Errorf("fuel level = %v, want %v", state.FuelLevel, level)
	}
	if state.FuelTempC == nil || *state.FuelTempC != temp {
		t.Errorf("fuel temp = %v, want %v", state.FuelTempC, temp)
	}
	if state.Status != models.StatusOnline {
		t.Errorf("status = %s, want the existing ONLINE preserved", state.Status)
	}
	if state.LastSeen <= seed.LastSeen {
		t.Errorf("LastSeen not refreshed: %d <= %d", state.LastSeen, seed.LastSeen)
	}
}

// TestITFuelOnlyWithoutStateWritesFreshState covers the merge fallback when the
// device has no usable live state yet (missing key AND a corrupt leftover).
func TestITFuelOnlyWithoutStateWritesFreshState(t *testing.T) {
	it := newITWorker(t)
	imei := "860000009900004"
	key := it.stateKey(imei)

	// A corrupt leftover must be replaced by a decodable state, never propagated.
	it.put(key, "{not-json")

	level := 12.0
	fuelOnly := models.TelemetryMessage{IMEI: imei, CompanyCode: "DEV001", VehicleID: 72, FuelLevel: &level}
	if err := it.w.handleMessage(telemetryFrame(t, "telemetry.raw."+imei, fuelOnly)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	it.w.flushBuffer()

	// The corrupt seed is replaced by a decodable state: poll until the merge
	// for this device is visible (the flush is asynchronous).
	state := it.waitStateFor(key, func(st models.LiveState) bool {
		return st.VehicleID == 72 && st.FuelLevel != nil
	})
	if state.IMEI != imei || state.CompanyCode != "DEV001" || state.VehicleID != 72 {
		t.Errorf("identity fields not refreshed: %+v", state)
	}
	if state.FuelLevel == nil || *state.FuelLevel != level {
		t.Errorf("fuel level = %v, want %v", state.FuelLevel, level)
	}
	if state.Status != models.StatusOnline {
		t.Errorf("status = %s, want ONLINE for a fresh merge", state.Status)
	}
	if state.Lat != 0 || state.Speed != 0 {
		t.Errorf("a partial frame must not invent a position: %+v", state)
	}
}

// TestITStopDrainsBufferedUpdates covers the graceful-shutdown contract: a
// buffered update is still written after Stop (no silent loss) and a second
// Stop stays idempotent.
func TestITStopDrainsBufferedUpdates(t *testing.T) {
	it := newITWorker(t)
	imei := "860000009900005"
	msg := positionMessage(imei)
	if err := it.w.handleMessage(telemetryFrame(t, "telemetry.raw."+imei, msg)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	it.w.Stop()
	it.w.Stop() // idempotent

	raw := it.get(it.stateKey(imei))
	var state models.LiveState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("drained state not decodable: %v", err)
	}
	if state.IMEI != imei || state.Speed != msg.Speed {
		t.Errorf("drained state incomplete: %+v", state)
	}
}

// TestITStopWithEmptyBufferIsSafe covers the flush no-op branch: stopping
// without buffered work must not write anything.
func TestITStopWithEmptyBufferIsSafe(t *testing.T) {
	it := newITWorker(t)
	it.w.Stop()
	keys, err := it.red.ScanKeys(context.Background(), it.cfg.Redis.KeyPrefix+"*", 10)
	if err == nil && len(keys) != 0 {
		t.Errorf("Stop wrote %d keys with an empty buffer", len(keys))
	}
}

// TestITSweepOnceMarksStaleStateOffline covers FR-2.2: the staleness sweeper
// flips a stale ONLINE state to OFFLINE and notifies subscribers immediately.
func TestITSweepOnceMarksStaleStateOffline(t *testing.T) {
	it := newITWorker(t)
	imei := "860000009900006"
	key := it.stateKey(imei)
	sink := it.liveFrames(imei)

	stale := models.LiveState{
		IMEI: imei, CompanyCode: "DEV001", VehicleID: 73,
		Lat: -6.2, Lon: 106.8, Speed: 0, Status: models.StatusOnline,
		LastSeen: time.Now().Unix() - 3600,
	}
	raw, _ := json.Marshal(stale)
	it.put(key, string(raw))

	it.w.sweepOnce()

	state := it.waitState(key)
	if state.Status != models.StatusOffline {
		t.Errorf("status = %s, want OFFLINE after the staleness sweep", state.Status)
	}
	if state.Lat != stale.Lat || state.Lon != stale.Lon {
		t.Errorf("sweep lost the last known position: %+v", state)
	}

	it.waitFor("OFFLINE live frame", func() bool { return sink.count() > 0 })
	if frame := sink.first(); frame["status"] != models.StatusOffline {
		t.Errorf("live frame status = %v, want OFFLINE", frame["status"])
	}
}

// TestITSweepOnceIgnoresFreshOfflineAndUnreadableStates documents the skip
// branches: fresh states, already-OFFLINE states and corrupt values are left
// untouched (no re-flagging, no publish).
func TestITSweepOnceIgnoresFreshOfflineAndUnreadableStates(t *testing.T) {
	it := newITWorker(t)
	now := time.Now().Unix()

	freshImei, offlineImei, corruptImei := "860000009900007", "860000009900008", "860000009900009"
	freshKey, offlineKey, corruptKey := it.stateKey(freshImei), it.stateKey(offlineImei), it.stateKey(corruptImei)
	sink := it.liveFrames(freshImei)

	freshRaw, _ := json.Marshal(models.LiveState{
		IMEI: freshImei, CompanyCode: "DEV001", Status: models.StatusOnline, LastSeen: now, Speed: 10,
	})
	offlineRaw, _ := json.Marshal(models.LiveState{
		IMEI: offlineImei, CompanyCode: "DEV001", Status: models.StatusOffline, LastSeen: now - 7200,
	})
	it.put(freshKey, string(freshRaw))
	it.put(offlineKey, string(offlineRaw))
	it.put(corruptKey, "{not-json")

	it.w.sweepOnce()

	if state := it.waitState(freshKey); state.Status != models.StatusOnline {
		t.Errorf("fresh state flagged %s, want ONLINE", state.Status)
	}
	if state := it.waitState(offlineKey); state.Status != models.StatusOffline {
		t.Errorf("OFFLINE state changed to %s", state.Status)
	}
	if raw := it.get(corruptKey); raw != "{not-json" {
		t.Errorf("unreadable state was overwritten (%s)", raw)
	}
	if n := sink.count(); n != 0 {
		t.Errorf("%d live frames published for skipped states, want 0", n)
	}
}

// TestITSweepOnceWithEmptyNamespaceIsNoop covers the empty-scan guard.
func TestITSweepOnceWithEmptyNamespaceIsNoop(t *testing.T) {
	it := newITWorker(t)
	it.w.sweepOnce() // nothing seeded: must return quietly
	keys, err := it.red.ScanKeys(context.Background(), it.cfg.Redis.KeyPrefix+"*", 10)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("sweeper created %d keys in an empty namespace", len(keys))
	}
}

// TestITStartWiresRawSubscription covers the production wiring: Start subscribes
// `telemetry.raw.>` on the "live" queue group and the flusher goroutine it
// launches drains buffered updates into Redis. The broker round trip is NOT
// asserted here because a running dev worker-live instance shares that queue
// group and would legitimately consume the message.
func TestITStartWiresRawSubscription(t *testing.T) {
	it := newITWorker(t)
	sub, err := it.w.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { it.nac.Unsubscribe(sub) })

	if got, want := sub.Subject, it.nac.Subject("raw", ">"); got != want {
		t.Errorf("subscription subject = %q, want %q", got, want)
	}
	if got := sub.Queue; got != "live" {
		t.Errorf("queue group = %q, want live", got)
	}

	// The ticker-driven flusher must persist a message buffered by the handler.
	imei := "860000009900010"
	if err := it.w.handleMessage(telemetryFrame(t, it.nac.Subject("raw", imei), positionMessage(imei))); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	state := it.waitState(it.stateKey(imei))
	if state.IMEI != imei || state.Speed == 0 {
		t.Errorf("flusher did not persist the buffered state: %+v", state)
	}

	it.w.Stop()
	it.nac.Unsubscribe(sub)
}

// TestITSweeperLoopTransitionsStaleState covers the periodic sweeper goroutine
// (not just sweepOnce): a stale state must flip to OFFLINE without any manual
// call once the loop ticks.
func TestITSweeperLoopTransitionsStaleState(t *testing.T) {
	it := newITWorker(t)
	t.Setenv("LIVE_SWEEP_INTERVAL_SEC", "1")

	imei := "860000009900012"
	key := it.stateKey(imei)
	raw, _ := json.Marshal(models.LiveState{
		IMEI: imei, CompanyCode: "DEV001", Status: models.StatusIdle, LastSeen: time.Now().Unix() - 3600,
	})
	it.put(key, string(raw))

	go it.w.sweeper()
	it.waitFor("sweeper OFFLINE transition", func() bool {
		return it.waitStateQuiet(key) == models.StatusOffline
	})
}

// waitStateQuiet reads a live state without failing the test when it is still
// unreadable (used inside polling predicates).
func (it *itWorker) waitStateQuiet(key string) string {
	raw, err := it.red.Get(context.Background(), key)
	if err != nil || raw == "" {
		return ""
	}
	var st models.LiveState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return ""
	}
	return st.Status
}

// TestITLiveStateKeyIsTenantScoped guards the Redis layout shared by the live
// writer, the REST overlay and the websocket fan-out (FR-2.1).
func TestITLiveStateKeyIsTenantScoped(t *testing.T) {
	it := newITWorker(t)
	a := it.stateKey("860000009900011")
	b := it.red.LiveStateKey("ACME", "860000009900011")
	if a == b {
		t.Fatalf("tenant-scoped keys collide: %s", a)
	}
	if want := fmt.Sprintf("%sdev001:vehicle:state:860000009900011", it.cfg.Redis.KeyPrefix); a != want {
		t.Errorf("live key = %q, want %q", a, want)
	}
}
