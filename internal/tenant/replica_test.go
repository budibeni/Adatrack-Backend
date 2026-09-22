package tenant

import (
	"strings"
	"testing"
	"time"

	"adatrack_gps/internal"
)

// replicaManager builds a Manager with the replica configured but no live pools:
// the breaker state machine is pure logic and must be testable without a DB.
func replicaManager(t *testing.T, replicaHost string) *Manager {
	t.Helper()
	base := &internal.Config{}
	base.Postgres.Host = "primary.internal"
	base.Postgres.Port = "5432"
	base.Postgres.DB = "adatrack_gps_db"
	base.Postgres.User = "adatrack_gps_user"
	base.Postgres.Password = "secret"
	base.Postgres.SSLMode = "disable"
	base.Postgres.Replica.Host = replicaHost
	if replicaHost != "" {
		base.Postgres.Replica.Port = "5433"
	}
	return &Manager{
		baseCfg:   base,
		cfg:       Config{MasterSchema: "adatrack_gps_master", CompanyPrefix: "adatrack_gps_"},
		reads:     make(map[string]*replicaReads),
		pools:     make(map[string]*internal.DBPool),
		companies: make(map[string]Company),
	}
}

// TestReplicaDisabledByDefault pins the backwards-compatibility guarantee: with no
// POSTGRES_REPLICA_HOST every read stays on the primary.
func TestReplicaDisabledByDefault(t *testing.T) {
	m := replicaManager(t, "")
	if m.ReplicaEnabled() {
		t.Fatal("ReplicaEnabled() = true without POSTGRES_REPLICA_HOST")
	}
	if got := m.ReadRoute("DEV001"); got != RoutePrimary {
		t.Fatalf("ReadRoute = %q want %q", got, RoutePrimary)
	}
}

// TestReplicaBreakerStateMachine drives 3 failures -> open 30 s -> half-open and a
// success that closes it again (PRD §13).
func TestReplicaBreakerStateMachine(t *testing.T) {
	m := replicaManager(t, "replica.internal")
	if !m.ReplicaEnabled() {
		t.Fatal("ReplicaEnabled() = false with POSTGRES_REPLICA_HOST set")
	}

	const code = "DEV001"
	// A sentinel pool marks the replica as opened (its DB handle is never used by
	// the breaker logic under test).
	st := &replicaReads{pool: &internal.DBPool{Name: "replica:dev001"}}
	m.reads[code] = st

	if got := m.ReadRoute(code); got != RouteReplica {
		t.Fatalf("fresh replica: ReadRoute = %q want %q", got, RouteReplica)
	}

	// Two failures keep the breaker closed (below the threshold).
	for i := 1; i <= replicaFailThreshold-1; i++ {
		m.recordReplicaResult(code, errFake)
		if got := m.ReadRoute(code); got != RouteReplica {
			t.Fatalf("after %d failure(s): ReadRoute = %q want %q", i, got, RouteReplica)
		}
	}

	// The threshold failure opens it: reads fall back to the primary.
	m.recordReplicaResult(code, errFake)
	if got := m.ReadRoute(code); got != RoutePrimary {
		t.Fatalf("after %d failures: ReadRoute = %q want %q", replicaFailThreshold, got, RoutePrimary)
	}

	// Once the open window elapses the breaker is half-open (one read may probe).
	st.mu.Lock()
	st.openTill = time.Now().Add(-time.Second)
	st.mu.Unlock()
	if got := m.ReadRoute(code); got != RouteReplica {
		t.Fatalf("half-open: ReadRoute = %q want %q", got, RouteReplica)
	}

	// A success resets the failure count and closes the breaker for good.
	m.recordReplicaResult(code, nil)
	st.mu.Lock()
	failures, openTill := st.failures, st.openTill
	st.mu.Unlock()
	if failures != 0 || !openTill.IsZero() {
		t.Fatalf("after success: failures=%d openTill=%v want 0/nil", failures, openTill)
	}
	if got := m.ReadRoute(code); got != RouteReplica {
		t.Fatalf("closed after success: ReadRoute = %q want %q", got, RouteReplica)
	}
}

// errFake stands in for a replica error in the breaker tests.
var errFake = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "replica unavailable (test)" }

// TestPostgresReplicaDSN covers the DSN rules: explicit replica endpoint, primary
// inheritance, forced search_path and the deliberate DATABASE_URL bypass.
func TestPostgresReplicaDSN(t *testing.T) {
	base := &internal.Config{}
	base.Postgres.Host = "primary.internal"
	base.Postgres.Port = "5533"
	base.Postgres.DB = "adatrack_gps_db"
	base.Postgres.User = "adatrack_gps_user"
	base.Postgres.Password = "secret"
	base.Postgres.SSLMode = "disable"

	// No replica configured: the DSN builder is still well defined.
	base.Postgres.Replica.Host = "replica.internal"
	base.Postgres.Replica.Port = "5433"

	t.Setenv("DATABASE_URL", "postgres://someone@elsewhere:9999/otherdb")
	dsn := base.PostgresReplicaDSN("adatrack_gps_dev001")

	for _, want := range []string{
		"replica.internal:5433",
		"adatrack_gps_user:secret@",
		"/adatrack_gps_db?",
		"search_path=adatrack_gps_dev001",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("replica DSN %q missing %q", dsn, want)
		}
	}
	if strings.Contains(dsn, "elsewhere") {
		t.Errorf("replica DSN must ignore DATABASE_URL (points at the primary): %q", dsn)
	}

	// Credentials/db inherited from the primary when the replica does not override
	// them (a standby that only differs by host/port).
	base.Postgres.Replica.User = ""
	base.Postgres.Replica.Password = ""
	base.Postgres.Replica.DB = ""
	base.Postgres.Replica.Port = ""
	dsn = base.PostgresReplicaDSN("adatrack_gps_master")
	for _, want := range []string{"replica.internal:5533", "adatrack_gps_user:secret@", "/adatrack_gps_db?"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("inherited replica DSN %q missing %q", dsn, want)
		}
	}
}
