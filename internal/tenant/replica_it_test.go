package tenant

// replica_it_test.go — integration coverage for the read/write split (PRD §13)
// against a REAL standby. Opt-in like the rest of the IT suite (ADATRACK_IT=1)
// plus ADATRACK_IT_PG_REPLICA=<host:port>; the HA overlay provides one:
//
//make ha-up                       # postgres-replica on 127.0.0.1:5433
//ADATRACK_IT=1 ADATRACK_IT_PG_REPLICA=127.0.0.1:5433 \
//  go test -run TestITReadWriteSplit ./internal/tenant/
//
// It proves the split end-to-end: reads are served by the standby (route=replica),
// writes go to the primary, and the standby sees them through streaming WAL.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestITReadWriteSplit routes reads to the standby and writes to the primary.
func TestITReadWriteSplit(t *testing.T) {
	skipNoDB(t)

	replicaAddr := strings.TrimSpace(os.Getenv("ADATRACK_IT_PG_REPLICA"))
	if replicaAddr == "" {
		t.Skip("set ADATRACK_IT_PG_REPLICA=<host:port> (e.g. 127.0.0.1:5433 from `make ha-up`) to exercise the read/write split")
	}
	host, port, ok := strings.Cut(replicaAddr, ":")
	if !ok {
		t.Fatalf("ADATRACK_IT_PG_REPLICA=%q must be host:port", replicaAddr)
	}
	t.Setenv("POSTGRES_REPLICA_HOST", host)
	t.Setenv("POSTGRES_REPLICA_PORT", port)

	m, _, _ := itManager(t)
	// Close LAST: t.Cleanup runs LIFO, so registering the close first makes the
	// marker-table DROP below run while the pools are still open. (A plain
	// `defer m.Close()` would close them BEFORE the cleanup and the DROP would
	// silently fail, leaving the fixture table behind.)
	t.Cleanup(m.Close)

	if !m.ReplicaEnabled() {
		t.Fatal("ReplicaEnabled() = false although POSTGRES_REPLICA_HOST is set")
	}
	if m.ReadRoute("DEFAULT") != RouteReplica {
		t.Fatal("ReadRoute(DEFAULT) != replica — the split did not engage")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	primary, err := m.DB("DEFAULT") // write pool (search_path = platform schema)
	if err != nil {
		t.Fatalf("primary pool: %v", err)
	}
	if _, err := primary.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS it_split_marker (
id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
note TEXT NOT NULL)`); err != nil {
		t.Fatalf("create marker table on primary: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = primary.DB.ExecContext(cctx, `DROP TABLE IF EXISTS it_split_marker`)
	})

	marker := "it-split-" + strconv.Itoa(os.Getpid())
	if _, err := primary.DB.ExecContext(ctx, `INSERT INTO it_split_marker (note) VALUES ($1)`, marker); err != nil {
		t.Fatalf("insert on primary: %v", err)
	}

	// The standby is a streaming replica: the row must show up there within the
	// replication lag, and the read must have been served by the replica route.
	var (
		count int
		route string
		last  error
	)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rows, rt, rerr := m.readQuery(ctx, "DEFAULT", `SELECT count(*) FROM it_split_marker WHERE note = $1`, marker)
		if rerr != nil {
			last = rerr
			time.Sleep(250 * time.Millisecond)
			continue
		}
		route = rt
		if rows.Next() {
			if serr := rows.Scan(&count); serr != nil {
				last = serr
			}
		}
		_ = rows.Close()
		if last == nil && count == 1 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if last != nil {
		t.Fatalf("read through the split failed: %v", last)
	}
	if route != RouteReplica {
		t.Fatalf("read route = %q want %q (reads must come from the standby)", route, RouteReplica)
	}
	if count != 1 {
		t.Fatalf("standby saw %d row(s) for the marker, want 1 (replication lag?)", count)
	}
	if got := readActor(t, m, ctx); got == "" {
		t.Fatal("could not read current_user through the split")
	}

	// ReadQueryRow keeps *sql.Row semantics (single row + ErrNoRows when empty).
	var note string
	if err := m.ReadQueryRow(ctx, "DEFAULT", `SELECT note FROM it_split_marker WHERE note = $1`, marker).Scan(&note); err != nil {
		t.Fatalf("ReadQueryRow scan: %v (replication lag?)", err)
	}
	if note != marker {
		t.Fatalf("ReadQueryRow note = %q want %q", note, marker)
	}
	if err := m.ReadQueryRow(ctx, "DEFAULT", `SELECT note FROM it_split_marker WHERE note = $1`, "does-not-exist").Scan(&note); err == nil {
		t.Fatal("ReadQueryRow on a missing row returned no error (want sql.ErrNoRows)")
	}

	t.Logf("read/write split verified: route=%s rows=%d read_route=%s", route, count, m.ReadRoute("DEFAULT"))
}

// readActor reads current_user through the split (a second real statement, so the
// replica pool is exercised with args-free SQL too).
func readActor(t *testing.T, m *Manager, ctx context.Context) string {
	t.Helper()
	var who string
	if err := m.ReadQueryRow(ctx, "DEFAULT", `SELECT current_user`).Scan(&who); err != nil {
		t.Errorf("current_user via replica: %v", err)
		return ""
	}
	return who
}

// TestITReadFallbackToPrimary pins the guarantee that a BROKEN replica never fails
// a read: the replica endpoint points at a closed port, so opening its pool fails
// and ReadQuery must serve the query from the primary (route=primary).
func TestITReadFallbackToPrimary(t *testing.T) {
	skipNoDB(t)

	// 127.0.0.1:1 has nothing listening → connect refused, deterministically.
	t.Setenv("POSTGRES_REPLICA_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_REPLICA_PORT", "1")

	m, _, _ := itManager(t)
	t.Cleanup(m.Close)

	if !m.ReplicaEnabled() {
		t.Fatal("ReplicaEnabled() = false although a replica endpoint is configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rows, route, err := m.readQuery(ctx, "DEFAULT", `SELECT 1`)
	if err != nil {
		t.Fatalf("read with an unreachable replica failed (must fall back to primary): %v", err)
	}
	var one int
	if rows.Next() {
		_ = rows.Scan(&one)
	}
	_ = rows.Close()
	if one != 1 {
		t.Fatalf("SELECT 1 returned %d", one)
	}
	if route != RoutePrimary {
		t.Fatalf("route = %q want %q (the replica is unreachable)", route, RoutePrimary)
	}

	// One failure is deliberately NOT enough to open the breaker (transient blips
	// must not disable the split), so the route is still "replica"…
	if got := m.ReadRoute("DEFAULT"); got != RouteReplica {
		t.Fatalf("ReadRoute after 1 failure = %q want %q (threshold is %d)",
			got, RouteReplica, replicaFailThreshold)
	}

	// …but after the threshold the breaker opens and reads skip the replica.
	for i := 1; i < replicaFailThreshold; i++ {
		r, _, rerr := m.readQuery(ctx, "DEFAULT", `SELECT 1`)
		if rerr != nil {
			t.Fatalf("attempt %d with an unreachable replica failed: %v", i+1, rerr)
		}
		_ = r.Close()
	}
	if got := m.ReadRoute("DEFAULT"); got != RoutePrimary {
		t.Fatalf("ReadRoute after %d failures = %q want %q", replicaFailThreshold, got, RoutePrimary)
	}

	var missed int
	if err := m.ReadQueryRow(ctx, "DEFAULT", `SELECT 1`).Scan(&missed); err != nil {
		t.Fatalf("ReadQueryRow with an unreachable replica: %v", err)
	}
	t.Logf("fallback verified: route=%s breaker=%s", route, m.ReadRoute("DEFAULT"))
}
