package controllers

// Integration suite for the persistence worker (B4).
//
// It exercises the REAL tenant manager + PostgreSQL insert path (FR-3.1..FR-3.4):
// tenant routing, BatchInsert, the fuel table split and the dead-letter
// publication. Opt-in via ADATRACK_IT=1 so `go test ./...` stays hermetic.
//
// Every fixture row is written with a unique IMEI marker and deleted again in
// t.Cleanup, so the dev dataset is left untouched.

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/worker-persistence/models"
)

// itCompany is the tenant the suite writes into (provider DEV company seeded by
// database/seed + scripts/provision-tenant.sh).
const itCompany = "DEV001"

// itEnv reads an opt-in override or falls back to the local dev bind port.
func itEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// itConfig points the shared config at the host-published infra ports.
func itConfig(t *testing.T) *internal.Config {
	t.Helper()
	if os.Getenv("ADATRACK_IT") != "1" {
		t.Skip("integration test — set ADATRACK_IT=1 with live PostgreSQL/NATS (see Makefile: coverage)")
	}
	t.Setenv("POSTGRES_HOST", itEnv("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", itEnv("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", itEnv("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", itEnv("ADATRACK_IT_REDIS_PORT", "6380"))
	t.Setenv("NATS_URL", itEnv("ADATRACK_IT_NATS_URL", "nats://127.0.0.1:4222"))

	cfg := internal.LoadConfig()
	cfg.Persistence.BatchSize = 500
	cfg.Persistence.BatchTimeout = time.Hour // no background ticker during tests
	return cfg
}

// itPersister bundles the persister under test with the live collaborators.
type itPersister struct {
	t    *testing.T
	cfg  *internal.Config
	tm   *tenant.Manager
	nac  *internal.NATSClient
	p    *Persister
	imei string
}

// newITPersister wires a persister on a live tenant manager + NATS client.
func newITPersister(t *testing.T, imei string) *itPersister {
	t.Helper()
	cfg := itConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tm, err := tenant.New(ctx, cfg, tenant.ConfigFromEnv(cfg), nil, nil)
	if err != nil {
		t.Fatalf("tenant manager unavailable (is compose up + migrated?): %v", err)
	}
	if _, err := tm.DB(itCompany); err != nil {
		t.Fatalf("tenant %s not provisioned: %v", itCompany, err)
	}

	nac, err := internal.NewNATSClient(cfg)
	if err != nil {
		t.Fatalf("nats unavailable on %s: %v", cfg.NATS.URL, err)
	}
	p := New(cfg, tm, nac)

	it := &itPersister{t: t, cfg: cfg, tm: tm, nac: nac, p: p, imei: imei}
	t.Cleanup(func() {
		it.purgeRows()
		nac.Close()
		tm.Close()
	})
	return it
}

// pool returns the tenant pool, failing the test when it is unavailable.
func (it *itPersister) pool() *internal.DBPool {
	it.t.Helper()
	pool, err := it.tm.DB(itCompany)
	if err != nil {
		it.t.Fatalf("tenant pool %s: %v", itCompany, err)
	}
	return pool
}

// purgeRows deletes every fixture row written by the suite.
func (it *itPersister) purgeRows() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := it.tm.DB(itCompany)
	if err != nil {
		return
	}
	if _, err := pool.DB.ExecContext(ctx,
		`DELETE FROM th_telemetry_logs WHERE imei = $1`, it.imei); err != nil {
		it.t.Logf("cleanup th_telemetry_logs: %v", err)
	}
	if _, err := pool.DB.ExecContext(ctx,
		`DELETE FROM td_fuel_logs WHERE imei = $1`, it.imei); err != nil {
		it.t.Logf("cleanup td_fuel_logs: %v", err)
	}
}

// countTelemetry counts the fixture rows in th_telemetry_logs.
func (it *itPersister) countTelemetry() int {
	it.t.Helper()
	var n int
	err := it.pool().DB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM th_telemetry_logs WHERE imei = $1`, it.imei).Scan(&n)
	if err != nil {
		it.t.Fatalf("count th_telemetry_logs: %v", err)
	}
	return n
}

// countFuel counts the fixture rows in td_fuel_logs.
func (it *itPersister) countFuel() int {
	it.t.Helper()
	var n int
	err := it.pool().DB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM td_fuel_logs WHERE imei = $1`, it.imei).Scan(&n)
	if err != nil {
		it.t.Fatalf("count td_fuel_logs: %v", err)
	}
	return n
}

// errorFrames subscribes to the dead-letter subject family and records payloads.
func (it *itPersister) errorFrames() *errorSink {
	it.t.Helper()
	sink := &errorSink{ch: make(chan string, 32)}
	sub, err := it.nac.Subscribe(it.nac.Subject("error", ">"), "", func(msg *nats.Msg) error {
		select {
		case sink.ch <- string(msg.Data):
		default:
		}
		return nil
	})
	if err != nil {
		it.t.Fatalf("subscribe error subject: %v", err)
	}
	it.t.Cleanup(func() { it.nac.Unsubscribe(sub) })
	return sink
}

// errorSink records dead-letter payloads.
type errorSink struct {
	mu   sync.Mutex
	seen []string
	ch   chan string
}

// drain moves every buffered payload into the slice.
func (s *errorSink) drain() {
	for {
		select {
		case p := <-s.ch:
			s.mu.Lock()
			s.seen = append(s.seen, p)
			s.mu.Unlock()
		default:
			return
		}
	}
}

// waitFor polls until at least want payloads arrived.
func (s *errorSink) waitFor(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.drain()
		s.mu.Lock()
		n := len(s.seen)
		s.mu.Unlock()
		if n >= want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.drain()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// vehicleID returns a real vehicle id of the tenant (th_telemetry_logs carries
// no FK, but using a real device keeps the fixture meaningful).
func (it *itPersister) vehicleID() int64 {
	it.t.Helper()
	var id int64
	err := it.pool().DB.QueryRowContext(context.Background(),
		`SELECT coalesce(min(id), 1) FROM tm_vehicles WHERE deleted_at IS NULL`).Scan(&id)
	if err != nil {
		it.t.Fatalf("lookup vehicle: %v", err)
	}
	if id <= 0 {
		id = 1
	}
	return id
}

// rows builds n telemetry rows for the fixture IMEI.
func (it *itPersister) rows(n int) []models.Row {
	vehicle := it.vehicleID()
	rows := make([]models.Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, models.ToRow(models.TelemetryMessage{
			IMEI: it.imei, CompanyCode: itCompany, VehicleID: vehicle,
			Lat: -6.2088 + float64(i)/1000, Lon: 106.8456, Speed: 40,
			ACC: models.BoolPtr(true), Battery: 12, Timestamp: time.Now().Unix() + int64(i),
		}))
	}
	return rows
}

// fuelRows builds n fuel rows for the fixture IMEI.
func (it *itPersister) fuelRows(n int) []models.FuelRow {
	vehicle := it.vehicleID()
	rows := make([]models.FuelRow, 0, n)
	for i := 0; i < n; i++ {
		level := 50.0 - float64(i)
		rows = append(rows, models.ToFuelRow(models.TelemetryMessage{
			IMEI: it.imei, CompanyCode: itCompany, VehicleID: vehicle,
			FuelLevel: &level, ACC: models.BoolPtr(true), Timestamp: time.Now().Unix() + int64(i),
		}))
	}
	return rows
}

// TestITPersistCompanyInsertsBatch covers the real insert path (FR-3.1/FR-3.2):
// tenant routing → parameterized multi-row INSERT → th_telemetry_logs.
func TestITPersistCompanyInsertsBatch(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000001")

	it.p.persistCompany(itCompany, it.rows(3))

	if got := it.countTelemetry(); got != 3 {
		t.Fatalf("th_telemetry_logs rows for the fixture IMEI = %d, want 3", got)
	}

	// Values must round-trip (parameter binding + DECIMAL projection).
	var (
		speed float64
		acc   int
		lat   float64
	)
	err := it.pool().DB.QueryRowContext(context.Background(),
		`SELECT speed, acc_status, latitude FROM th_telemetry_logs
		 WHERE imei = $1 ORDER BY "timestamp" LIMIT 1`, it.imei).Scan(&speed, &acc, &lat)
	if err != nil {
		t.Fatalf("read back the inserted row: %v", err)
	}
	if speed != 40 || acc != 1 {
		t.Errorf("round-trip mismatch: speed=%v acc=%d want 40/1", speed, acc)
	}
	if lat == 0 {
		t.Error("latitude was not persisted")
	}
}

// TestITHandleMessageFlushReachesTheDatabase covers the whole worker path with a
// live database: NATS payload → buffer → flush → tenant batch insert.
func TestITHandleMessageFlushReachesTheDatabase(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000002")
	it.p.resolveCompanyDB = func(code string) (*internal.DBPool, error) { return it.tm.DB(code) }

	for i := 0; i < 2; i++ {
		msg := positionMessage(it.imei, itCompany)
		msg.VehicleID = it.vehicleID()
		msg.Timestamp = time.Now().Unix() + int64(i)
		if err := it.p.handleMessage(telemetryMsg(t, msg)); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	}
	it.p.flush()
	it.p.wg.Wait()

	if got := it.countTelemetry(); got != 2 {
		t.Fatalf("flush persisted %d rows, want 2", got)
	}
}

// TestITPersistFuelCompanyInsertsBatch covers the B5a fuel split into
// td_fuel_logs (FR-7.4).
func TestITPersistFuelCompanyInsertsBatch(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000003")

	it.p.persistFuelCompany(itCompany, it.fuelRows(2))

	if got := it.countFuel(); got != 2 {
		t.Fatalf("td_fuel_logs rows for the fixture IMEI = %d, want 2", got)
	}
	var level *float64
	err := it.pool().DB.QueryRowContext(context.Background(),
		`SELECT fuel_level FROM td_fuel_logs WHERE imei = $1 ORDER BY fuel_level DESC LIMIT 1`,
		it.imei).Scan(&level)
	if err != nil {
		t.Fatalf("read back the fuel row: %v", err)
	}
	if level == nil || *level != 50 {
		t.Errorf("fuel_level round-trip = %v, want 50", level)
	}
}

// TestITPersistUnknownTenantDeadLetters covers a real routing miss: rows of an
// unknown company are dead-lettered on telemetry.error.<IMEI> (no silent drop).
func TestITPersistUnknownTenantDeadLetters(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000004")
	sink := it.errorFrames()

	it.p.persistCompany("ITGHOST", it.rows(2))

	payloads := sink.waitFor(t, 2)
	if len(payloads) != 2 {
		t.Fatalf("dead letters = %d, want 2 (%v)", len(payloads), payloads)
	}
	for _, p := range payloads {
		if p != "tenant:routing" {
			t.Errorf("dead-letter payload = %q, want tenant:routing", p)
		}
	}
}

// TestITPersistDeadLettersWhenInsertFails covers FR-3.4 step 6 with a real (but
// already closed) pool: the insert fails, so every row is published for replay.
func TestITPersistDeadLettersWhenInsertFails(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000005")
	sink := it.errorFrames()

	closed, err := internal.OpenPostgresPool(it.cfg, it.tm.Schema(itCompany), "it-closed")
	if err != nil {
		t.Fatalf("open scratch pool: %v", err)
	}
	if cerr := closed.Close(); cerr != nil {
		t.Fatalf("close scratch pool: %v", cerr)
	}
	it.p.resolveCompanyDB = func(string) (*internal.DBPool, error) { return closed, nil }

	it.p.persistCompany(itCompany, it.rows(2))

	payloads := sink.waitFor(t, 2)
	if len(payloads) != 2 {
		t.Fatalf("dead letters = %d, want 2 (%v)", len(payloads), payloads)
	}
	for _, p := range payloads {
		if p != "batch:fail" {
			t.Errorf("dead-letter payload = %q, want batch:fail", p)
		}
	}
}

// TestITPersistFuelDeadLettersWhenRoutingFails covers the fuel routing guard:
// fuel rows of an unknown company are dead-lettered (no silent drop).
func TestITPersistFuelDeadLettersWhenRoutingFails(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000006")
	sink := it.errorFrames()

	rows := it.fuelRows(2)
	for i := range rows {
		rows[i].CompanyCode = "ITGHOST"
	}
	it.p.persistFuel(fuelGroups(rows))

	payloads := sink.waitFor(t, 2)
	if len(payloads) != 2 {
		t.Fatalf("fuel dead letters = %d, want 2 (%v)", len(payloads), payloads)
	}
	for _, p := range payloads {
		if p != "tenant:routing" {
			t.Errorf("dead-letter payload = %q, want tenant:routing", p)
		}
	}
}

// TestITStartWiresPersistenceSubscription covers the production wiring: the
// persister consumes `telemetry.raw.>` in the "persistence" queue group
// (alongside the durable JetStream consumer) and Stop drains the buffer.
func TestITStartWiresPersistenceSubscription(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000007")

	sub, err := it.p.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { it.nac.Unsubscribe(sub) })

	if want := it.nac.Subject("raw", ">"); sub.Subject != want {
		t.Errorf("subscription subject = %q, want %q", sub.Subject, want)
	}
	if sub.Queue != "persistence" {
		t.Errorf("queue group = %q, want persistence", sub.Queue)
	}

	it.p.resolveCompanyDB = func(code string) (*internal.DBPool, error) { return it.tm.DB(code) }
	msg := positionMessage(it.imei, itCompany)
	msg.VehicleID = it.vehicleID()
	if err := it.p.handleMessage(telemetryMsg(t, msg)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	it.p.Stop() // drains the buffered row synchronously

	if got := it.countTelemetry(); got != 1 {
		t.Fatalf("Stop did not drain the buffered row (%d rows in th_telemetry_logs)", got)
	}
}

// TestITTenantSchemaAndTablesExist guards the schema contract the worker depends
// on (migrations 007 + 014) plus the tenant pool resolution.
func TestITTenantSchemaAndTablesExist(t *testing.T) {
	it := newITPersister(t, "ITPERSIST0000000000000008")
	pool := it.pool()

	if schema := it.tm.Schema(itCompany); schema == "" {
		t.Error("tenant schema not resolved for " + itCompany)
	}
	for _, table := range []string{models.TableName, models.FuelTableName} {
		var n int
		if err := pool.DB.QueryRowContext(context.Background(),
			"SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Errorf("table %s not queryable: %v", table, err)
		}
	}
}
