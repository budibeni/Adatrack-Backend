package controllers

// runtime_test.go (B4 coverage sesi 2026-08-31, lanjutan): jalur runtime —
// healthHandler/joinChecks/Stop, publishError, escalateCompany (counter/cap/
// publish re-notify), recordTTA (sqlmock), offlineScanCompany (sqlmock),
// Start end-to-end via embedded NATS. Infra: miniredis + embedded NATS +
// sqlmock — tanpa DB/Redis/NATS eksternal.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// Helper injeksi (test seams) — pola sama dengan worker-persistence.
// ---------------------------------------------------------------------------

// overrideStoreFn menyuntik store tiruan untuk semua panggilan newStoreFn
// selama test berjalan (restore otomatis via t.Cleanup).
func overrideStoreFn(t *testing.T, f store) {
	t.Helper()
	orig := newStoreFn
	newStoreFn = func(_ *WorkerAlert, _ string) (store, error) { return f, nil }
	t.Cleanup(func() { newStoreFn = orig })
}

// overrideCompaniesFn menyuntik daftar company untuk monitor berkala
// (offline scan / SOS escalation / route refresh).
func overrideCompaniesFn(t *testing.T, cs []tenant.Company) {
	t.Helper()
	orig := companiesFn
	companiesFn = func(_ *tenant.Manager) []tenant.Company { return cs }
	t.Cleanup(func() { companiesFn = orig })
}

// newMockCompanyStore: *companyStore nyata di atas sqlmock — untuk jalur yang
// melakukan type-assert ke *companyStore (recordTTA / offlineScanCompany).
func newMockCompanyStore(t *testing.T) (*companyStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &companyStore{code: "DEV001", db: db, ro: tenant.NewSingleRouter(db)}, mock
}

// attachNATS menempelkan NATS client nyata (embedded server) ke wa yang sudah
// punya Redis miniredis — dibutuhkan jalur yang memakai redis + publish.
func attachNATS(t *testing.T, wa *WorkerAlert) *nats.Conn {
	t.Helper()
	url := startTestNATS(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(func() { nc.Close() })
	cfg := wa.cfg
	cfg.NATS.URL = url
	nac, err := internal.NewNATSClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNATSClient: %v", err)
	}
	wa.nac = nac
	return nc
}

// ---------------------------------------------------------------------------
// healthHandler / joinChecks / Stop
// ---------------------------------------------------------------------------

func TestJoinChecks(t *testing.T) {
	if got := joinChecks([]string{"a", "b", "c"}); got != "a,b,c" {
		t.Fatalf("joinChecks = %q", got)
	}
	if got := joinChecks(nil); got != "" {
		t.Fatalf("joinChecks(nil) = %q", got)
	}
}

func TestHealthHandler_ChecksAndStatus(t *testing.T) {
	wa, _ := newTestWA(t)
	attachNATS(t, wa) // agar nats:ok terverifikasi (bukan jalur nac nil)
	rr := httptest.NewRecorder()
	wa.healthHandler(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "redis:ok") || !strings.Contains(body, "nats:ok") {
		t.Fatalf("health body missing checks: %q", body)
	}
	if !strings.Contains(body, "mysql:") {
		t.Fatalf("health body missing mysql check: %q", body)
	}
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected code %d", rr.Code)
	}
	if strings.HasPrefix(body, "ok") && rr.Code != http.StatusOK {
		t.Fatalf("ok body with code %d", rr.Code)
	}
	if strings.HasPrefix(body, "degraded") && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded body with code %d", rr.Code)
	}
}

func TestStop_CancelsAndDrains(t *testing.T) {
	wa, _ := newTestWA(t)
	ctx, cancel := context.WithCancel(context.Background())
	wa.SetContext(ctx, cancel)
	done := make(chan struct{})
	wa.wg.Add(1)
	go func() { defer wa.wg.Done(); <-wa.ctx.Done(); close(done) }()
	wa.Stop() // harus kembali setelah goroutine drain
	select {
	case <-done:
	default:
		t.Fatal("Stop tidak menunggu goroutine selesai")
	}
}

// ---------------------------------------------------------------------------
// publishError → telemetry.error.<IMEI>
// ---------------------------------------------------------------------------

func TestPublishError_Guards(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.publishError("", []byte("x")) // subject kosong → no-op
	wa.publishError("telemetry.raw.1", []byte("x")) // nac nil → no-op
}

func TestPublishError_PublishesToErrorSubject(t *testing.T) {
	wa, _ := newTestWA(t)
	nc := attachNATS(t, wa)
	sub, err := nc.SubscribeSync("telemetry.error.999")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	wa.publishError("telemetry.raw.999", []byte("bad-json"))
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("no error publish received: %v", err)
	}
	if string(msg.Data) != "bad-json" {
		t.Fatalf("payload = %q", msg.Data)
	}
}

// ---------------------------------------------------------------------------
// escalateCompany — counter Redis / cap / re-notify publish
// ---------------------------------------------------------------------------

func sosEntry(id, vehicle uint64) models.OpenSOSAlert {
	return models.OpenSOSAlert{
		ID:        id,
		VehicleID: vehicle,
		CreatedAt: time.Now().Add(-10 * time.Minute),
	}
}

func TestEscalateCompany_CounterCapAndCounterError(t *testing.T) {
	wa, mr := newTestWA(t)
	f := &fakeStore{sosList: []models.OpenSOSAlert{sosEntry(501, 7)}}
	overrideStoreFn(t, f)

	// (1) counter bertambah & eskalasi jalan (nac nil → publish di-skip guard).
	wa.escalateCompany("DEV001", 2*time.Minute, 3)
	if v, _ := mr.Get(wa.sosEscalationKey("DEV001", 501)); v != "1" {
		t.Fatalf("counter = %q, want 1", v)
	}

	// (2) cap: counter 3 → increment jadi 4 > maxRounds → berhenti eskalasi.
	_ = mr.Set(wa.sosEscalationKey("DEV001", 501), "3")
	wa.escalateCompany("DEV001", 2*time.Minute, 3)
	if v, _ := mr.Get(wa.sosEscalationKey("DEV001", 501)); v != "4" {
		t.Fatalf("counter after cap = %q, want 4", v)
	}

	// (3) counter error (redis tutup) → metrics path tanpa panic.
	mr.Close()
	wa.escalateCompany("DEV001", 2*time.Minute, 3)
}

func TestEscalateCompany_PublishesReNotify(t *testing.T) {
	wa, _ := newTestWA(t)
	nc := attachNATS(t, wa)
	f := &fakeStore{sosList: []models.OpenSOSAlert{sosEntry(502, 7)}}
	overrideStoreFn(t, f)
	sub, err := nc.SubscribeSync("alert.sos.dev001")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	wa.escalateCompany("DEV001", 2*time.Minute, 3)
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("escalation publish missing: %v", err)
	}
	if !strings.Contains(string(msg.Data), `"escalation":1`) {
		t.Fatalf("payload = %s", msg.Data)
	}
}

// ---------------------------------------------------------------------------
// recordTTA — dialek-aware, sekali per alert (sqlmock)
// ---------------------------------------------------------------------------

func ttaRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "sec"}).AddRow("700251", "42")
}

func TestRecordTTA_ObservesOnce(t *testing.T) {
	wa, mr := newTestWA(t)
	cs, mock := newMockCompanyStore(t)
	overrideStoreFn(t, cs)
	mock.ExpectQuery("FROM alerts").WillReturnRows(ttaRows())
	mock.ExpectQuery("FROM alerts").WillReturnRows(ttaRows())

	wa.recordTTA("DEV001", time.Minute) // pertama → observe
	key := wa.redisKeyPrefix() + "dev001:sos_tta_seen"
	if members, _ := mr.SMembers(key); len(members) != 1 {
		t.Fatalf("seen set = %v, want 1 member", members)
	}
	wa.recordTTA("DEV001", time.Minute) // duplikat → SAdd 0 → skip
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRecordTTA_QueryErrorSilent(t *testing.T) {
	wa, _ := newTestWA(t)
	cs, mock := newMockCompanyStore(t)
	overrideStoreFn(t, cs)
	mock.ExpectQuery("FROM alerts").WillReturnError(errors.New("db down"))
	wa.recordTTA("DEV001", time.Minute) // error → return tanpa panic
}

// ---------------------------------------------------------------------------
// offlineScanCompany — dedup open-alert + dedup error (sqlmock)
// ---------------------------------------------------------------------------

func TestOfflineScanCompany_DedupAndDedupError(t *testing.T) {
	wa, _ := newTestWA(t) // redis kosong → semua device silent
	// offlineScanCompany memeriksa wa.ctx.Done() per-vehicle (seperti runtime)
	ctx, cancel := context.WithCancel(context.Background())
	wa.SetContext(ctx, cancel)
	cs, mock := newMockCompanyStore(t)
	overrideStoreFn(t, cs)

	vehicles := sqlmock.NewRows([]string{"id", "imei"}).
		AddRow("7", "1").AddRow("8", "2")
	mock.ExpectQuery("FROM vehicles").WillReturnRows(vehicles)
	// v7: sudah ada open OFFLINE → skip (dedup).
	mock.ExpectQuery("FROM alerts").WithArgs(int64(7), "OFFLINE").
		WillReturnRows(sqlmock.NewRows([]string{"cnt"}).AddRow("1"))
	// v8: dedup check gagal → metrics path.
	mock.ExpectQuery("FROM alerts").WithArgs(int64(8), "OFFLINE").
		WillReturnError(errors.New("dedup boom"))

	wa.offlineScanCompany("DEV001") // tidak panic & tidak insert
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// handleTelemetry — branch lookup/inactive/SOS-eksklusif/DB-trust
// ---------------------------------------------------------------------------

func TestHandleTelemetry_VehicleLookupErrorAndInactive(t *testing.T) {
	wa, _ := newTestWA(t)

	// (1) lookup error generik → metrics, tanpa alert.
	fErr := &fakeStore{vehErr: errors.New("db boom")}
	overrideStoreFn(t, fErr)
	wa.handleTelemetry(&natsMsg{Data: []byte(`{"imei":"1","company_code":"DEV001","speed":90}`)})
	if len(fErr.inserts) != 0 {
		t.Fatalf("lookup error must not insert: %+v", fErr.inserts)
	}

	// (2) IMEI tanpa row vehicle (ErrUnknownVehicle) → drop ter-log.
	fUnk := &fakeStore{vehErr: ErrUnknownVehicle}
	overrideStoreFn(t, fUnk)
	wa.handleTelemetry(&natsMsg{Data: []byte(`{"imei":"1","company_code":"DEV001"}`)})

	// (3) vehicle inactive/maintenance → tidak memicu alert apa pun.
	fIna := &fakeStore{vehID: 7, vehStatus: "maintenance", speedOK: true}
	overrideStoreFn(t, fIna)
	wa.handleTelemetry(&natsMsg{Data: []byte(`{"imei":"1","company_code":"DEV001","speed":120}`)})
	if len(fIna.inserts) != 0 {
		t.Fatalf("inactive vehicle must not alert: %+v", fIna.inserts)
	}
}

func TestHandleTelemetry_SOSExclusiveAndDBTrust(t *testing.T) {
	wa, _ := newTestWA(t)

	// (1) SOS eksklusif: hanya alert SOS (check lain tidak jalan).
	fSOS := &fakeStore{vehID: 7, vehStatus: "active", speedOK: true,
		speedCfg: models.SpeedConfig{SpeedLimitKMH: 50}}
	overrideStoreFn(t, fSOS)
	wa.handleTelemetry(&natsMsg{Data: []byte(
		`{"imei":"1","company_code":"DEV001","speed":120,"raw":"SOS pressed"}`)})
	if len(fSOS.inserts) != 1 || fSOS.inserts[0].AlertType != models.AlertTypeSOS {
		t.Fatalf("expected exactly one SOS alert, got %+v", fSOS.inserts)
	}
	if fSOS.inserts[0].Severity != "critical" {
		t.Fatalf("SOS severity = %q", fSOS.inserts[0].Severity)
	}

	// (2) vehicle_id mismatch → percayai DB perusahaan (7, bukan 99).
	fTrust := &fakeStore{vehID: 7, vehStatus: "active", speedOK: true,
		speedCfg: models.SpeedConfig{SpeedLimitKMH: 50}}
	overrideStoreFn(t, fTrust)
	wa.handleTelemetry(&natsMsg{Data: []byte(
		`{"imei":"1","company_code":"DEV001","speed":120,"vehicle_id":99}`)})
	if len(fTrust.inserts) != 1 || fTrust.inserts[0].VehicleID != 7 {
		t.Fatalf("expected DB-trusted vehicle_id 7, got %+v", fTrust.inserts)
	}
}

// ---------------------------------------------------------------------------
// Start end-to-end: subscribe → handleTelemetry → alert + Stop drain
// ---------------------------------------------------------------------------

func TestStart_EndToEndTelemetryAndStop(t *testing.T) {
	wa, _ := newTestWA(t)
	nc := attachNATS(t, wa)
	f := &fakeStore{vehID: 7, vehStatus: "active", speedOK: true,
		speedCfg: models.SpeedConfig{SpeedLimitKMH: 50}}
	overrideStoreFn(t, f)
	overrideCompaniesFn(t, []tenant.Company{{Code: "DEV001", IsActive: true}})

	ctx, cancel := context.WithCancel(context.Background())
	wa.SetContext(ctx, cancel)
	if err := wa.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	sub, err := nc.SubscribeSync("alert.speed.dev001")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	if err := nc.Publish(wa.cfg.Subject("raw", "1"),
		[]byte(`{"imei":"1","company_code":"DEV001","speed":120}`)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for len(f.inserts) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(f.inserts) != 1 || f.inserts[0].AlertType != models.AlertTypeSpeed {
		t.Fatalf("expected 1 OVERSPEEDING via full pipeline, got %+v", f.inserts)
	}
	if _, err := sub.NextMsg(2 * time.Second); err != nil {
		t.Fatalf("expected alert.speed publish: %v", err)
	}
	_ = sub.Unsubscribe()
	wa.Stop() // drain subscriber + 3 monitor goroutine
}