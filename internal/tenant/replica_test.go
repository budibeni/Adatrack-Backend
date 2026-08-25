package tenant

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Stub SQL driver — memungkinkan unit test routing ReadRouter TANPA infra
// database nyata. Setiap query tercatat per-DSN sehingga test dapat memveri-
// fikasi ke pool mana (primary vs replica) trafik benar-benar dikirim.
// ---------------------------------------------------------------------------

const (
	stubDriverName = "tenant-stub"
	stubPrimaryDSN = "stub://primary"
	stubReplicaDSN = "stub://replica"
)

var (
	stubMu      sync.Mutex
	stubFailDSN = map[string]bool{} // dsn → selalu gagal
	stubQueries = []string{}       // urutan "<dsn>|<query>"
	stubOnce    sync.Once
)

func stubRegister() {
	stubOnce.Do(func() {
		sql.Register(stubDriverName, stubDriver{})
	})
}

func stubReset(failReplica bool) {
	stubRegister()
	stubMu.Lock()
	defer stubMu.Unlock()
	stubFailDSN = map[string]bool{}
	if failReplica {
		stubFailDSN[stubReplicaDSN] = true
	}
	stubQueries = nil
}

func stubLog(dsn, query string) {
	stubMu.Lock()
	defer stubMu.Unlock()
	stubQueries = append(stubQueries, dsn+"|"+query)
}

func stubQueriedDSNs() []string {
	stubMu.Lock()
	defer stubMu.Unlock()
	out := append([]string(nil), stubQueries...)
	return out
}

type stubDriver struct{}

func (stubDriver) Open(name string) (driver.Conn, error) { return &stubConn{dsn: name}, nil }

type stubConn struct{ dsn string }

func (c *stubConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("stub: Prepare not supported")
}
func (c *stubConn) Close() error              { return nil }
func (c *stubConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

// QueryContext makes *sql.DB use this path for queries (no Prepare roundtrip).
func (c *stubConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	stubMu.Lock()
	fail := stubFailDSN[c.dsn]
	stubMu.Unlock()
	stubLog(c.dsn, query)
	if fail {
		return nil, errors.New("stub: simulated replica outage (" + c.dsn + ")")
	}
	return &stubRows{}, nil
}

// ExecContext covers the write path (*sql.DB Exec prefers driver.ExecerContext).
func (c *stubConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	stubMu.Lock()
	fail := stubFailDSN[c.dsn]
	stubMu.Unlock()
	stubLog(c.dsn, query)
	if fail {
		return nil, errors.New("stub: simulated outage (" + c.dsn + ")")
	}
	return driver.RowsAffected(1), nil
}

type stubRows struct{ sent bool }

func (r *stubRows) Columns() []string { return []string{"n"} }
func (r *stubRows) Close() error      { return nil }
func (r *stubRows) Next(dest []driver.Value) error {
	if r.sent {
		return io.EOF
	}
	r.sent = true
	dest[0] = int64(1)
	return nil
}

// stubOpenDB opens a *sql.DB against the stub driver for a given logical DSN.
func stubOpenDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(stubDriverName, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestRouter builds a ReadRouter over stub pools. now dipakai BERSAMA
// oleh router dan breaker agar half-open bisa diuji deterministik.
func newTestRouter(t *testing.T, failReplica bool, now func() time.Time) *ReadRouter {
	t.Helper()
	stubReset(failReplica)
	return &ReadRouter{
		key:     "DEV001",
		primary: stubOpenDB(t, stubPrimaryDSN),
		replica: stubOpenDB(t, stubReplicaDSN),
		brk:     &breakerState{nowFn: now},
		now:     now,
	}
}

func countDSN(logs []string, dsn string) int {
	n := 0
	for _, l := range logs {
		if strings.HasPrefix(l, dsn+"|") {
			n++
		}
	}
	return n
}

func TestReadRouterRoutesReadsToReplica(t *testing.T) {
	r := newTestRouter(t, false, time.Now)
	var got int64
	if err := r.QueryRow(`SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("query via replica should succeed: %v", err)
	}
	if got != 1 {
		t.Fatalf("unexpected value %d", got)
	}
	logs := stubQueriedDSNs()
	if n := countDSN(logs, stubReplicaDSN); n != 1 {
		t.Fatalf("expected 1 replica query, got %d (%v)", n, logs)
	}
	if n := countDSN(logs, stubPrimaryDSN); n != 0 {
		t.Fatalf("primary must not be hit when replica healthy (logs=%v)", logs)
	}
	if r.brk.failuresCount() != 0 {
		t.Fatalf("healthy replica must reset breaker, failures=%d", r.brk.failuresCount())
	}
}

func TestReadRouterFallsBackToPrimaryOnReplicaError(t *testing.T) {
	r := newTestRouter(t, true /* replica failing */, time.Now)
	var got int64
	if err := r.QueryRow(`SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("fallback to primary should succeed: %v", err)
	}
	logs := stubQueriedDSNs()
	if countDSN(logs, stubReplicaDSN) != 1 || countDSN(logs, stubPrimaryDSN) != 1 {
		t.Fatalf("expected 1 replica attempt + 1 primary fallback, logs=%v", logs)
	}
	if r.brk.failuresCount() != 1 {
		t.Fatalf("failure must be recorded on breaker, got %d", r.brk.failuresCount())
	}

	// Trip breaker: setiap query berikutnya tetap mencoba (half-open slot
	// belum habis karena failures < threshold), lalu terbuka di threshold.
	for i := 2; i <= replicaTripThreshold; i++ {
		_ = r.QueryRow(`SELECT 1`).Scan(&got)
	}
	logs = stubQueriedDSNs()
	if countDSN(logs, stubReplicaDSN) != replicaTripThreshold {
		t.Fatalf("want %d replica attempts before trip, logs=%v", replicaTripThreshold, logs)
	}

	// Query berikutnya harus BYPASS replica sepenuhnya (breaker open).
	before := len(stubQueriedDSNs())
	if err := r.QueryRow(`SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("post-trip query must succeed via primary: %v", err)
	}
	logs = stubQueriedDSNs()[before:]
	if countDSN(logs, stubReplicaDSN) != 0 || countDSN(logs, stubPrimaryDSN) != 1 {
		t.Fatalf("open breaker must bypass replica & serve primary, logs=%v", logs)
	}
}

func TestReadRouterHalfOpenRecoveryAfterCooldown(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	r := newTestRouter(t, true /* failing */, func() time.Time { return now })

	var got int64
	for i := 0; i < replicaTripThreshold; i++ {
		if err := r.QueryRow(`SELECT 1`).Scan(&got); err != nil {
			t.Fatalf("fallback query %d: %v", i, err)
		}
	}
	if f := r.brk.failuresCount(); f < replicaTripThreshold {
		t.Fatalf("breaker should be at/above threshold, failures=%d", f)
	}

	// Replika pulih; waktu melampaui cooldown → half-open mengizinkan satu
	// percobaan dan sukses me-reset breaker.
	stubReset(false) // replika sehat lagi (reset juga log)
	now = base.Add(defaultReplicaCooldown + time.Second)
	if err := r.QueryRow(`SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("half-open probe should succeed: %v", err)
	}
	logs := stubQueriedDSNs()
	if countDSN(logs, stubReplicaDSN) != 1 {
		t.Fatalf("half-open attempt must hit replica once, logs=%v", logs)
	}
	if f := r.brk.failuresCount(); f != 0 {
		t.Fatalf("success in half-open must close breaker, failures=%d", f)
	}
}

func TestReadRouterExecAlwaysGoesToPrimary(t *testing.T) {
	r := newTestRouter(t, false, time.Now)
	if _, err := r.Exec(`UPDATE t SET x=1`); err != nil {
		t.Fatalf("exec via primary should succeed: %v", err)
	}
	logs := stubQueriedDSNs()
	if countDSN(logs, stubPrimaryDSN) != 1 || countDSN(logs, stubReplicaDSN) != 0 {
		t.Fatalf("Exec MUST target primary only, logs=%v", logs)
	}
}

func TestNewSingleRouterServesEverythingFromPrimary(t *testing.T) {
	stubReset(false)
	r := NewSingleRouter(stubOpenDB(t, stubPrimaryDSN))
	var got int64
	if err := r.QueryRow(`SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("single router query: %v", err)
	}
	if _, err := r.Exec(`UPDATE t SET x=1`); err != nil {
		t.Fatalf("single router exec: %v", err)
	}
	logs := stubQueriedDSNs()
	if len(logs) != 2 || countDSN(logs, stubPrimaryDSN) != 2 {
		t.Fatalf("single router must use primary for everything, logs=%v", logs)
	}
}

func TestBreakerStateTransitions(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cur := base
	b := &breakerState{nowFn: func() time.Time { return cur }}

	if !b.allows(cur) {
		t.Fatal("fresh breaker must allow")
	}
	for i := 1; i < replicaTripThreshold; i++ {
		b.recordFailure()
		if !b.allows(base.Add(time.Duration(i) * time.Second)) {
			t.Fatal("below threshold must still allow")
		}
	}
	b.recordFailure() // capai threshold → open pada cur (= base)
	if b.allows(base.Add(time.Second)) {
		t.Fatal("open breaker within cooldown must deny")
	}
	cur = base.Add(defaultReplicaCooldown + time.Second)
	halfAt := cur
	if !b.allows(halfAt) {
		t.Fatal("after cooldown a half-open slot must be granted")
	}
	b.recordFailure() // failure saat half-open → re-open pada openedAt = halfAt
	if b.allows(halfAt.Add(time.Second)) {
		t.Fatal("failed half-open must re-open the breaker")
	}
	b.recordSuccess()
	if f := b.failuresCount(); f != 0 {
		t.Fatalf("recordSuccess must zero failures, got %d", f)
	}
}

func TestNilBreakerAndRouterAreSafe(t *testing.T) {
	var b *breakerState
	if b.allows(time.Now()) {
		t.Fatal("nil breaker must deny replica traffic")
	}
	b.recordFailure() // no-op tanpa panic

	stubReset(false)
	var nilRouter *ReadRouter
	if nilRouter.useReplica() {
		t.Fatal("nil router must not claim replica usage")
	}
	row := nilRouter.QueryRow(`SELECT 1`)
	if err := row.Scan(new(int)); err == nil {
		t.Fatal("nil router query must return error, not panic")
	}
}

func TestReplicaConfigFromEnvPostgres(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "postgres")
	t.Setenv("POSTGRES_HOST", "10.0.0.8")
	t.Setenv("POSTGRES_PORT", "5532")
	t.Setenv("POSTGRES_USER", "adatrack_gps_user")
	t.Setenv("POSTGRES_REPLICA_PORT", "5533")
	t.Setenv("DB_REPLICA_ENABLED", "true")

	cfg := NewConfigFromEnv()
	if !cfg.ReplicaEnabled {
		t.Fatal("DB_REPLICA_ENABLED=true harus terbaca")
	}
	host, port := cfg.ReplicaEndpoint()
	if host != "10.0.0.8" || port != "5533" {
		t.Fatalf("endpoint = %s:%s, want 10.0.0.8:5533", host, port)
	}

	// DATABASE_URL (PRIMARY) tidak boleh bocor ke DSN replika.
	t.Setenv("DATABASE_URL", "postgres://leak@primary:1/db?sslmode=disable")
	dsn := cfg.ReplicaDSN("adatrack_gps_dev001")
	if strings.Contains(dsn, "leak@primary") {
		t.Fatalf("replica DSN must bypass DATABASE_URL: %s", dsn)
	}
	if !strings.Contains(dsn, "@10.0.0.8:5533/") ||
		!strings.Contains(dsn, "search_path=adatrack_gps_dev001") {
		t.Fatalf("unexpected replica DSN: %s", dsn)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestReplicaConfigFromEnvMySQL(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "mysql")
	t.Setenv("MASTER_DB_HOST", "10.1.1.5")
	t.Setenv("MASTER_DB_PORT", "3307")
	t.Setenv("MASTER_DB_NAME", "adatrack_gps_master")
	t.Setenv("MYSQL_REPLICA_PORT", "3407")
	t.Setenv("DB_REPLICA_ENABLED", "1")

	cfg := NewConfigFromEnv()
	host, port := cfg.ReplicaEndpoint()
	if host != "10.1.1.5" || port != "3407" {
		t.Fatalf("endpoint = %s:%s, want 10.1.1.5:3407", host, port)
	}
	dsn := cfg.ReplicaDSN("adatrack_gps_dev001")
	want := "tcp(10.1.1.5:3407)/adatrack_gps_dev001"
	if !strings.Contains(dsn, want) {
		t.Fatalf("replica DSN missing %q: %s", want, dsn)
	}
}

func TestReplicaConfigDisabledByDefault(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "mysql")
	t.Setenv("MASTER_DB_HOST", "127.0.0.1")
	t.Setenv("MASTER_DB_PORT", "3306")
	t.Setenv("MASTER_DB_NAME", "adatrack_gps_master")
	for _, k := range []string{"DB_REPLICA_ENABLED", "MYSQL_REPLICA_PORT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg := NewConfigFromEnv()
	if cfg.ReplicaEnabled {
		t.Fatal("replica harus DISABLED secara default")
	}
	if _, ok := os.LookupEnv("MYSQL_REPLICA_PORT"); !ok {
		// getEnv fallback → 3407; cukup pastikan tidak kosong utk validasi.
		if cfg.ReplicaPort == "" {
			t.Fatal("default replica port harus 3407 utk mysql")
		}
	}
}