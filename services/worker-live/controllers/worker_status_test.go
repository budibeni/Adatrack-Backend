package controllers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ajb_gps/worker-live/models"

	"github.com/nats-io/nats.go"
)

// resetLiveState mengembalikan global worker-live ke kondisi bersama antar test.
func resetLiveState(t *testing.T) {
	t.Helper()
	Configure(nil, nil) // reset buffer/flushCh/ctx/cancel; client nil aman utk jalur tanpa Redis/NATS
	t.Cleanup(func() {
		if cancel != nil {
			cancel()
		}
	})
}

func liveMsg(t *testing.T, v models.TelemetryMessage) *nats.Msg {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &nats.Msg{Subject: "telemetry.raw.x", Data: b}
}

// ---------------------------------------------------------------------
// CalculateStatus — FR-2.2 (ONLINE / IDLE / OFFLINE).
// ---------------------------------------------------------------------

func TestCalculateStatusMovingIsOnline(t *testing.T) {
	now := time.Now().Unix()
	if got := CalculateStatus(42.5, now); got != "ONLINE" {
		t.Errorf("speed>0 = %s, ingin ONLINE", got)
	}
}

func TestCalculateStatusStoppedAccOnIsIdle(t *testing.T) {
	now := time.Now().Unix()
	if got := CalculateStatus(0, now); got != "IDLE" {
		t.Errorf("speed=0 segar = %s, ingin IDLE", got)
	}
}

func TestCalculateStatusStaleIsOffline(t *testing.T) {
	now := time.Now().Unix()
	stale := now - int64(models.OfflineAfter.Seconds()) - 10 // 5+ menit lalu
	if got := CalculateStatus(80, stale); got != "OFFLINE" {
		t.Errorf("event basi = %s, ingin OFFLINE", got)
	}
}

func TestCalculateStatusBoundaryStillFresh(t *testing.T) {
	now := time.Now().Unix()
	fresh := now - int64(models.OfflineAfter.Seconds()) + 30 // 30 dtk sebelum ambang
	if got := CalculateStatus(0, fresh); got == "OFFLINE" {
		t.Errorf("masih dalam window 3 mnt tidak boleh OFFLINE, dapat %s", got)
	}
}

// ---------------------------------------------------------------------
// handleMsg — buffering live state.
// ---------------------------------------------------------------------

func TestHandleMsgBuffersStateWithKeyAndStatus(t *testing.T) {
	resetLiveState(t)

	msg := liveMsg(t, models.TelemetryMessage{
		IMEI: "864201040512345", CompanyCode: "DEV001",
		Lat: -6.2, Lon: 106.8, Speed: 33, Timestamp: time.Now().Unix(),
	})
	if err := handleMsg(msg); err != nil {
		t.Fatalf("handleMsg: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(buffer) != 1 {
		t.Fatalf("buffer harus 1 entri, dapat %d", len(buffer))
	}
	key := "adatrack_gps:dev001:vehicle:state:864201040512345"
	raw, ok := buffer[key]
	if !ok {
		t.Fatalf("key %q tidak ada di buffer: %v", key, keys(buffer))
	}
	var st models.LiveState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("buffer bukan JSON valid: %v", err)
	}
	if st.Status != "ONLINE" || st.Speed != 33 || st.IMEI != "864201040512345" {
		t.Errorf("live state salah: %+v", st)
	}
}

func TestHandleMsgIdleForZeroSpeed(t *testing.T) {
	resetLiveState(t)
	msg := liveMsg(t, models.TelemetryMessage{IMEI: "x", CompanyCode: "DEV001", Timestamp: time.Now().Unix()})
	if err := handleMsg(msg); err != nil {
		t.Fatalf("handleMsg: %v", err)
	}
	mu.Lock()
	var raw string
	for _, v := range buffer {
		raw = v
	}
	mu.Unlock()
	if !strings.Contains(raw, `"status":"IDLE"`) {
		t.Errorf("speed 0 harus IDLE, dapat %s", raw)
	}
}

func TestHandleMsgInvalidJSONSkipsBuffer(t *testing.T) {
	resetLiveState(t)
	raw := &nats.Msg{Subject: "telemetry.raw.bad", Data: []byte("{rusak")}
	if err := handleMsg(raw); err != nil {
		t.Fatalf("bad json tidak boleh error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(buffer) != 0 {
		t.Fatalf("buffer harus kosong, dapat %d", len(buffer))
	}
}

func TestHandleMsgPokesAtMaxBuffer(t *testing.T) {
	resetLiveState(t)

	for i := 0; i < models.MaxBuffer-1; i++ {
		msg := liveMsg(t, models.TelemetryMessage{
			IMEI: fmt.Sprintf("%015d", i), CompanyCode: "D", Timestamp: 1,
		})
		if err := handleMsg(msg); err != nil {
			t.Fatalf("#%d: %v", i, err)
		}
	}
	if len(flushCh) != 0 {
		t.Fatalf("belum MaxBuffer, flushCh harus kosong (len=%d)", len(flushCh))
	}
	last := liveMsg(t, models.TelemetryMessage{IMEI: "last", CompanyCode: "D", Timestamp: 1})
	if err := handleMsg(last); err != nil {
		t.Fatalf("terakhir: %v", err)
	}
	if len(flushCh) != 1 {
		t.Errorf("setelah penuh harus ada 1 poke (len=%d)", len(flushCh))
	}
	// Poke berikutnya non-blocking (di-drop).
	poke()
	if len(flushCh) != 1 {
		t.Errorf("poke ekstra harus di-drop, len=%d", len(flushCh))
	}
}

func TestFlushBufferEmptyIsSafeWithoutRedis(t *testing.T) {
	resetLiveState(t) // redCli nil
	flushBuffer()     // buffer kosong → early return sebelum menyentuh Redis
	mu.Lock()
	defer mu.Unlock()
	if len(buffer) != 0 {
		t.Fatal("buffer harus tetap kosong")
	}
}

func TestStopEmptyDrainsQuickly(t *testing.T) {
	resetLiveState(t)
	done := make(chan struct{})
	go func() { Stop(); close(done) }()
	select {
	case <-done:
		// OK
	case <-time.After(6 * time.Second):
		t.Fatal("Stop dengan buffer kosong harus segera kembali")
	}
}

func TestFlusherConsumesPokeThenCancels(t *testing.T) {
	resetLiveState(t)
	go flusher()

	flushCh <- struct{}{} // buffer kosong → no-op, goroutine tetap hidup
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-ctx.Done():
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("ctx harus done setelah cancel")
	}
}

// keys util kecil utk pesan error yang informatif.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}