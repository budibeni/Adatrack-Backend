package controllers

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"ajb_gps/worker-persistence/models"

	"github.com/prometheus/client_golang/prometheus"
)

var errRoutingDummy = errors.New("tenant:routing:fail")

// stubCompanyDBRoutingFail membuat resolve DB selalu gagal sehingga insert
// path melewati jalur tenant-routing-failure (tanpa sqlmock Exec), cocok
// untuk meng-cover flusher/drainPending/insertBatch wrapper.
func stubCompanyDBRoutingFail() func() {
	orig := companyDBFn
	companyDBFn = func(code string) (*sql.DB, error) { return nil, errRoutingDummy }
	return func() { companyDBFn = orig }
}

// ---------------------------------------------------------------------
// flusher — branch non-empty (tick) mengarah ke drainPending.
// ---------------------------------------------------------------------

func TestFlusherDrainsNonEmptyOnTick(t *testing.T) {
	resetPersistState(t)
	restore := stubCompanyDBRoutingFail()
	defer restore()
	captureErrors(t)

	mu.Lock()
	pending = []models.TelemetryRow{mkRow("d1", "dev001")}
	mu.Unlock()

	go flusher()
	time.Sleep(models.FlushInterval + 50*time.Millisecond)

	cancel()
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	n := len(pending) + len(pendingFuel)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("drainPending harus mengosongkan buffer, sisa %d", n)
	}
}

func Test_flusherStopsOnCancel(t *testing.T) {
	resetPersistState(t)
	go flusher()
	cancel()
	time.Sleep(20 * time.Millisecond)
}

// ---------------------------------------------------------------------
// drainPending — jalur non-empty memanggil insertBatch + insertFuelBatch.
// ---------------------------------------------------------------------

func Test_drainPendingNonEmptyRunsInsertWrappers(t *testing.T) {
	resetPersistState(t)
	restore := stubCompanyDBRoutingFail()
	defer restore()
	gl := captureErrors(t)

	lvl := 40.0
	mu.Lock()
	pending = []models.TelemetryRow{mkRow("t1", "dev001")}
	pendingFuel = []models.TelemetryRow{{IMEI: "f1", CompanyCode: "dev001", FuelLevel: &lvl}}
	mu.Unlock()

	drainPending()
	time.Sleep(30 * time.Millisecond)

	if got := len(gl()); got != 2 {
		t.Fatalf("drainPending non-empty harus publish 2 error (1 telemetry + 1 fuel), dapat %d", got)
	}
}

// ---------------------------------------------------------------------
// insertBatch / insertFuelBatch — wrapper goroutine.
// ---------------------------------------------------------------------

func Test_insertBatchWrapperGroupsAndPublishes(t *testing.T) {
	resetPersistState(t)
	restore := stubCompanyDBRoutingFail()
	defer restore()
	gl := captureErrors(t)

	insertBatch([]models.TelemetryRow{
		mkRow("a", "LOGIA"),
		mkRow("b", "LOGIB"),
	})
	time.Sleep(30 * time.Millisecond)

	if got := len(gl()); got != 2 {
		t.Fatalf("insertBatch wrapper harus publish 2 error, dapat %d", got)
	}
}

func Test_insertFuelBatchWrapper(t *testing.T) {
	resetPersistState(t)
	restore := stubCompanyDBRoutingFail()
	defer restore()
	gl := captureErrors(t)

	insertFuelBatch([]models.TelemetryRow{mkRow("f", "LOGIF")})
	time.Sleep(30 * time.Millisecond)

	if got := len(gl()); got != 1 {
		t.Fatalf("insertFuelBatch wrapper harus publish 1 error, dapat %d", got)
	}
}

// ---------------------------------------------------------------------
// RegisterMetrics — terdaftar tanpa panic/double-register.
// ---------------------------------------------------------------------

func Test_registerPersistenceAllMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterMetrics(reg)
	mf, _ := reg.Gather()
	if len(mf) == 0 {
		t.Fatal("registry harus berisi metric")
	}
}

// ---------------------------------------------------------------------
// resolveCompanyDB — jalur tenantMgr nil fail-fast.
// ---------------------------------------------------------------------

func Test_resolveCompanyDBNilManager(t *testing.T) {
	tenantMgr = nil
	if db, err := resolveCompanyDB("DEV001"); err == nil || db != nil {
		t.Fatalf("dgn tenantMgr nil harus error, dapat db=%v err=%v", db, err)
	}
}

// ---------------------------------------------------------------------
// Configure — global ter-init aman tanpa NATS nyata.
// ---------------------------------------------------------------------

func Test_configureSetsGlobal(t *testing.T) {
	resetPersistState(t)
	Configure(nil, nil)
	if flushCh == nil {
		t.Fatal("flushCh harus dibuat Configure")
	}
	if ctx == nil {
		t.Fatal("ctx harus dibuat Configure")
	}
}