package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/worker-persistence/models"

	"github.com/nats-io/nats.go"
)

// resetPersistState mengembalikan global package ke kondisi bersama antar test.
func resetPersistState(t *testing.T) {
	t.Helper()
	mu.Lock()
	pending = nil
	mu.Unlock()
	flushCh = make(chan struct{}, 1)
	natsCli = &internal.NATSClient{} // zero value: Subject() aman tanpa koneksi
	tenantMgr = nil
	wg = sync.WaitGroup{}
	ctx, cancel = context.WithCancel(context.Background())
	t.Cleanup(func() {
		if cancel != nil {
			cancel()
		}
	})
}

// captureErrors memasang stub publishErrFn dan mengembalikan fungsi pembaca
// hasil tangkapan (imei, payload) — menggantikan publishError asli.
func captureErrors(t *testing.T) func() []struct {
	imei    string
	payload []byte
} {
	t.Helper()
	mu := &sync.Mutex{}
	captured := make([]struct {
		imei    string
		payload []byte
	}, 0)
	orig := publishErrFn
	publishErrFn = func(imei string, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, struct {
			imei    string
			payload []byte
		}{imei, payload})
	}
	t.Cleanup(func() { publishErrFn = orig })
	return func() []struct {
		imei    string
		payload []byte
	} {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

func mustMsg(t *testing.T, v interface{}) *nats.Msg {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &nats.Msg{Subject: "telemetry.raw.123", Data: b}
}

// ---------------------------------------------------------------------
// GroupByCompany — pengelompokan per tenant (routing key).
// ---------------------------------------------------------------------

func TestGroupByCompany(t *testing.T) {
	rows := []models.TelemetryRow{
		{IMEI: "1", CompanyCode: "dev001"},
		{IMEI: "2", CompanyCode: " DEV001 "}, // trim + case-insensitive → satu group dgn #1
		{IMEI: "3", CompanyCode: "logi002"},  // company lain → group sendiri
		{IMEI: "4", CompanyCode: ""},         // fallback default (lowercase, sesuai kode)
		{IMEI: "5"},                          // fallback default
	}
	got := GroupByCompany(rows)
	if len(got) != 3 {
		t.Fatalf("harus 3 group (DEV001, LOGI002, default), dapat %d: %v", len(got), got)
	}
	if len(got["DEV001"]) != 2 || got["DEV001"][0].IMEI != "1" || got["DEV001"][1].IMEI != "2" {
		t.Errorf("group DEV001 salah: %+v", got["DEV001"])
	}
	if len(got["LOGI002"]) != 1 || got["LOGI002"][0].IMEI != "3" {
		t.Errorf("group LOGI002 salah: %+v", got["LOGI002"])
	}
	if len(got["default"]) != 2 || got["default"][0].IMEI != "4" || got["default"][1].IMEI != "5" {
		t.Errorf("group default harus 2 baris (4,5): %+v", got["default"])
	}
}

func TestGroupByCompanyEmpty(t *testing.T) {
	got := GroupByCompany(nil)
	if len(got) != 0 {
		t.Fatalf("input nil harus menghasilkan map kosong, dapat %v", got)
	}
}

// ---------------------------------------------------------------------
// IsTransientError — klasifikasi error utk keputusan retry.
// ---------------------------------------------------------------------

func TestIsTransientError(t *testing.T) {
	transient := []string{
		"dial tcp 127.0.0.1:3306: connect: connection refused",
		"CONNECTION RESET by peer",
		"context deadline exceeded: Timeout",
		"Error 1040: Too many connections",
		"Lock wait timeout exceeded",
		"Error 1213: Deadlock found when trying to get lock",
		"MySQL server has gone away",
	}
	for _, msg := range transient {
		if !IsTransientError(errors.New(msg)) {
			t.Errorf("harus transient: %q", msg)
		}
		if !isTransientError(errors.New(msg)) {
			t.Errorf("alias isTransientError gagal: %q", msg)
		}
	}
	nonTransient := []string{
		"Error 1062: Duplicate entry 'x' for key 'PRIMARY'",
		"unknown column",
		"",
	}
	for _, msg := range nonTransient {
		if IsTransientError(errors.New(msg)) {
			t.Errorf("TIDAK boleh transient: %q", msg)
		}
	}
	if IsTransientError(nil) {
		t.Error("nil harus false")
	}
}

// ---------------------------------------------------------------------
// handleMsg — mapping payload → TelemetryRow + buffering.
// ---------------------------------------------------------------------

func TestHandleMsgValidMapping(t *testing.T) {
	resetPersistState(t)

	msg := mustMsg(t, models.TelemetryMessage{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 7,
		Lat: -6.2, Lon: 106.8, Speed: 42.5, Heading: 90,
		Battery: 88, Timestamp: 1700000000,
	})
	if err := handleMsg(msg); err != nil {
		t.Fatalf("handleMsg: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(pending) != 1 {
		t.Fatalf("buffer harus 1 baris, dapat %d", len(pending))
	}
	row := pending[0]
	if row.IMEI != "864201040512345" || row.CompanyCode != "DEV001" ||
		row.VehicleID != 7 || row.Lat != -6.2 || row.Lon != 106.8 ||
		row.Speed != 42.5 || row.Heading != 90 || row.Battery != 88 {
		t.Errorf("mapping row salah: %+v", row)
	}
	if row.EventTS.Unix() != 1700000000 {
		t.Errorf("EventTS = %v, ingin unix 1700000000", row.EventTS)
	}
}

func TestHandleMsgDefaultsTimestampAndCompany(t *testing.T) {
	resetPersistState(t)

	msg := mustMsg(t, models.TelemetryMessage{IMEI: "x", Lat: 1, Lon: 2})
	if err := handleMsg(msg); err != nil {
		t.Fatalf("handleMsg: %v", err)
	}

	mu.Lock()
	row := pending[0]
	mu.Unlock()

	if row.CompanyCode != "default" {
		t.Errorf("company kosong harus fallback 'default', dapat %q", row.CompanyCode)
	}
	if now := time.Now().Unix(); row.EventTS.Unix() < now-5 || row.EventTS.Unix() > now+5 {
		t.Errorf("timestamp <=0 harus diisi now, dapat %v", row.EventTS)
	}
}

func TestHandleMsgFlushPokeAtMaxBatchSize(t *testing.T) {
	resetPersistState(t)

	// 499 baris: belum penuh → tidak ada poke.
	for i := 0; i < models.MaxBatchSize-1; i++ {
		msg := mustMsg(t, models.TelemetryMessage{IMEI: fmt.Sprintf("d%d", i), Timestamp: 1})
		if err := handleMsg(msg); err != nil {
			t.Fatalf("handleMsg #%d: %v", i, err)
		}
	}
	if len(flushCh) != 0 {
		t.Fatalf("belum MaxBatchSize, flushCh tidak boleh berisi (len=%d)", len(flushCh))
	}
	// Baris ke-500 → penuh → satu poke masuk (non-blocking).
	msg := mustMsg(t, models.TelemetryMessage{IMEI: "d-last", Timestamp: 1})
	if err := handleMsg(msg); err != nil {
		t.Fatalf("handleMsg terakhir: %v", err)
	}
	if len(flushCh) != 1 {
		t.Errorf("setelah penuh, flushCh harus berisi 1 poke (len=%d)", len(flushCh))
	}
	// Poke tambahan saat channel penuh tidak boleh blokir.
	if err := handleMsg(msg); err != nil {
		t.Fatalf("handleMsg ekstra: %v", err)
	}
	if len(flushCh) != 1 {
		t.Errorf("poke kedua harus di-drop (len tetap 1), dapat %d", len(flushCh))
	}
}

func TestHandleMsgInvalidJSONGoesToErrorSubject(t *testing.T) {
	resetPersistState(t)
	captured := captureErrors(t)

	raw := &nats.Msg{Subject: "telemetry.raw.bad", Data: []byte("{bukan-json")}
	if err := handleMsg(raw); err != nil {
		t.Fatalf("bad json tidak boleh error ke NATS (sudah dipublikasikan): %v", err)
	}

	mu.Lock()
	n := len(pending)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("buffer harus tetap kosong, dapat %d", n)
	}
	got := captured()
	if len(got) != 1 || got[0].imei != "" {
		t.Fatalf("harus 1 error publish dengan imei kosong, dapat %+v", got)
	}
	if string(got[0].payload) != "{bukan-json" {
		t.Errorf("payload harus data mentah, dapat %q", got[0].payload)
	}
}

// ---------------------------------------------------------------------
// drainPending / Stop / flusher / insertCompanyBatch (routing gagal).
// ---------------------------------------------------------------------

func TestDrainPendingEmptyIsNoop(t *testing.T) {
	resetPersistState(t)
	captured := captureErrors(t)

	drainPending() // buffer kosong → langsung balik, tanpa insert/publish

	mu.Lock()
	n := len(pending)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("pending harus tetap kosong, dapat %d", n)
	}
	if got := captured(); len(got) != 0 {
		t.Fatalf("tidak boleh ada publish error, dapat %d", len(got))
	}
}

func TestStopEmptySettlesImmediately(t *testing.T) {
	resetPersistState(t)
	done := make(chan struct{})
	go func() {
		Stop()
		close(done)
	}()
	select {
	case <-done:
		// OK — cancel + wg.Wait segera selesai (tidak ada batch berjalan).
	case <-time.After(2 * models.BatchWait):
		t.Fatal("Stop dengan buffer kosong harus segera kembali")
	}
}

func TestInsertCompanyBatchRoutingFailurePublishesAllRows(t *testing.T) {
	resetPersistState(t) // tenantMgr sengaja nil → routing gagal
	captured := captureErrors(t)

	rows := []models.TelemetryRow{
		{IMEI: "111", CompanyCode: "DEV001"},
		{IMEI: "222", CompanyCode: "DEV001"},
	}
	insertCompanyBatch("DEV001", rows) // sinkron (pemanggilan langsung, bukan via insertBatch)

	got := captured()
	if len(got) != 2 {
		t.Fatalf("setiap row harus dipublish ke telemetry.error, dapat %d", len(got))
	}
	for i, want := range []string{"111", "222"} {
		if got[i].imei != want || string(got[i].payload) != "tenant:routing" {
			t.Errorf("publish #%d = {%q,%q}, ingin {%q,tenant:routing}",
				i, got[i].imei, got[i].payload, want)
		}
	}
}

func TestFlusherConsumesPokeAndStopsOnCancel(t *testing.T) {
	resetPersistState(t)
	go flusher()

	// Poke saat buffer kosong: drainPending no-op, goroutine tetap hidup.
	flushCh <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	cancel()
	stopped := make(chan struct{})
	go func() { <-time.After(2 * time.Second); close(stopped) }()

	// Tidak ada cara menunggu flusher keluar secara langsung; cukup pastikan
	// cancel tidak panic dan context benar-benar done.
	select {
	case <-ctx.Done():
		// OK
	case <-stopped:
		t.Fatal("ctx tidak done setelah cancel")
	}
}
