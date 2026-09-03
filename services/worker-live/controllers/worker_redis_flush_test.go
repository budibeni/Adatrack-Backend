package controllers

import (
	"encoding/json"
	"testing"
	"time"

	"ajb_gps/worker-live/models"
)

// ---------------------------------------------------------------------
// flushBuffer — flush isi buffer (tanpa silent drop) via Redis MSET.
// ---------------------------------------------------------------------
func TestFlushBufferMSetsToRedisAndClears(t *testing.T) {
	mr := newTestRedis(t)

	// Tulis 2 entri ke buffer.
	mu.Lock()
	buffer["adatrack_gps:a:vehicle:state:1"] = `{"imei":"1","status":"ONLINE"}`
	buffer["adatrack_gps:a:vehicle:state:2"] = `{"imei":"2","status":"IDLE"}`
	mu.Unlock()

	flushBuffer() // snapshot sinkron; MSET dilakukan di goroutine

	// Buffer harus segera kosong (setelah snapshot diambil).
	mu.Lock()
	l := len(buffer)
	mu.Unlock()
	if l != 0 {
		t.Fatalf("buffer harus kosong setelah flush, dapat %d", l)
	}

	// Redis harus menerima 2 key (async goroutine) — tunggu singkat.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(mr.Keys()) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(mr.Keys()); got != 2 {
		t.Errorf("redis keys harus 2, dapat %d (%v)", got, mr.Keys())
	}
}

func TestFlushBufferNoContentSkips(t *testing.T) {
	newTestRedis(t)
	mu.Lock()
	buffer = make(map[string]string)
	mu.Unlock()
	flushBuffer() // buffer kosong → early return, tanpa menyentuh Redis
	mu.Lock()
	l := len(buffer)
	mu.Unlock()
	if l != 0 {
		t.Errorf("buffer harus kosong, dapat %d", l)
	}
}

// ---------------------------------------------------------------------
// handleMsg fuel-only path (B5a): merge parsial, posisi tidak tertimpa.
// ---------------------------------------------------------------------
func TestHandleMsgFuelOnlyMergesPartial(t *testing.T) {
	mr := newTestRedis(t)

	key := "adatrack_gps:fuelx:vehicle:state:111111"
	acc := false
	existing, _ := json.Marshal(models.LiveState{
		IMEI: "111111", CompanyCode: "fuelx",
		Lat: 1.5, Lon: 2.5, Speed: 55, Status: "ONLINE", ACC: &acc,
	})
	mr.Set(key, string(existing))

	fuel := 33.0
	msg := liveMsg(t, models.TelemetryMessage{
		IMEI: "111111", CompanyCode: "fuelx",
		FuelLevel: &fuel,
	})
	if err := handleMsg(msg); err != nil {
		t.Fatalf("handleMsg fuel: %v", err)
	}

	mu.Lock()
	raw := buffer[key]
	mu.Unlock()
	if raw == "" {
		t.Fatal("fuel-only msg tidak masuk buffer")
	}
	var st models.LiveState
	_ = json.Unmarshal([]byte(raw), &st)
	if st.Lat != 1.5 || st.Speed != 55 {
		t.Errorf("fuel merge menimpa posisi: %+v", st)
	}
	if st.FuelLevel == nil || *st.FuelLevel != 33 {
		t.Errorf("fuel tidak temerge: %+v", st.FuelLevel)
	}
}