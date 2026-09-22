package controllers

// store_pg_it_test.go — integration coverage for PostgresStore against a LIVE
// PostgreSQL (PRD §6.2 master schema + per-tenant company schemas).
//
// Sebelum suite ini, seluruh lapisan store service-websocket berada di 0 %
// coverage: unit test-nya memakai fake store, jadi SQL auth/user/tenant yang
// sebenarnya tidak pernah dieksekusi. Suite ini menutup celah itu:
//   - opt-in via ADATRACK_IT=1 (go test ./... tetap hermetik),
//   - fixture user memakai email unik per proses + dibersihkan di t.Cleanup,
//   - host/port (127.0.0.1:5533/6380) sama dengan scripts/start-services.sh.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// itCompany is the provider tenant seeded by database/seed + provision-tenant.sh.
const itCompany = "DEV001"

// skipNoDB gates the DB integration suite behind ADATRACK_IT=1.
func skipNoDB(t *testing.T) {
	t.Helper()
	if v, ok := os.LookupEnv("ADATRACK_IT"); ok && v == "1" {
		return
	}
	t.Skip("integration test — set ADATRACK_IT=1 with live PostgreSQL (127.0.0.1:5533)")
}

// envOr reads an env var or returns def.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// newITStore wires a real PostgresStore on the local stack. The tenant manager is
// closed in t.Cleanup.
func newITStore(t *testing.T) (*PostgresStore, *tenant.Manager) {
	t.Helper()
	skipNoDB(t)

	internal.LoadProjectEnv()
	t.Setenv("POSTGRES_HOST", envOr("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", envOr("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", envOr("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", envOr("ADATRACK_IT_REDIS_PORT", "6380"))

	cfg := internal.LoadConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tm, err := tenant.New(ctx, cfg, tenant.ConfigFromEnv(cfg), nil, nil)
	if err != nil {
		t.Fatalf("tenant manager: %v", err)
	}
	t.Cleanup(tm.Close)
	return NewPostgresStore(tm), tm
}

// itEmail builds a unique per-process fixture email. The tag keeps fixtures of
// different tests in this package from colliding on the unique email constraint.
func itEmail(tag string) string {
	return "it-" + tag + "-" + strconv.Itoa(os.Getpid()) + "@example.test"
}

// itUser inserts a master auth row for a fixture (role Admin, as required by the
// tm_users CHECK constraint) and deletes it in t.Cleanup.
func itUser(t *testing.T, store *PostgresStore, tag string) (int64, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	email := itEmail(tag)
	id, err := store.CreateUser(ctx, UserRecord{
		CompanyCode:  itCompany,
		Email:        email,
		PasswordHash: "$2a$12$itfixturehashnotusedforauth",
		FullName:     "IT Store Fixture",
		GlobalRole:   "Admin",
	})
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", tag, err)
	}
	if id == 0 {
		t.Fatalf("CreateUser(%s) returned id 0", tag)
	}
	t.Cleanup(func() {
		_, cerr := store.tenants.Master().DB.Exec(`DELETE FROM tm_users WHERE id = $1`, id)
		if cerr != nil {
			t.Errorf("cleanup tm_users id=%d: %v", id, cerr)
		}
	})
	return id, email
}

// TestITStoreReadiness covers the health surface used by /healthz.
func TestITStoreReadiness(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if store.Master() == nil {
		t.Fatal("Master() = nil")
	}
	if err := store.Readiness(ctx); err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	if err := store.TenantHealth(ctx); err != nil {
		t.Fatalf("TenantHealth: %v", err)
	}
	if err := store.PingTenant(ctx, itCompany); err != nil {
		t.Fatalf("PingTenant(%s): %v", itCompany, err)
	}
	// Unknown tenant: the readiness probe must report an error, never silently pass.
	if err := store.PingTenant(ctx, "NOSUCHTENANT"); err == nil {
		t.Fatal("PingTenant(unknown) = nil, want error")
	}
}

// TestITStoreUserLifecycle covers the master auth row used by Login: lookup by
// email (case-insensitive), lookup by id, duplicate detection, lockout state and
// the success/failure recorders (PRD §9.1).
func TestITStoreUserLifecycle(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	email := itEmail("user")
	id, err := store.CreateUser(ctx, UserRecord{
		CompanyCode:        itCompany,
		Email:              email,
		PasswordHash:       "$2a$12$itfixturehashnotusedforauth",
		FullName:           "IT Store Fixture",
		GlobalRole:         "Admin",
		MustChangePassword: false,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() {
		_, err := store.tenants.Master().DB.Exec(
			`DELETE FROM tm_users WHERE id = $1`, id)
		if err != nil {
			t.Errorf("cleanup tm_users id=%d: %v", id, err)
		}
	})
	if id == 0 {
		t.Fatal("CreateUser returned id 0")
	}

	// Lookup is case-insensitive and trims surrounding whitespace.
	got, err := store.UserByEmail(ctx, strings.ToUpper("  "+email+" "))
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if got == nil {
		t.Fatal("UserByEmail = nil, want record")
	}
	if got.ID != id || got.Email != email || got.GlobalRole != "Admin" {
		t.Fatalf("UserByEmail mismatch: id=%d email=%q role=%q", got.ID, got.Email, got.GlobalRole)
	}
	if got.CompanyCode != itCompany {
		t.Fatalf("CompanyCode = %q, want %q", got.CompanyCode, itCompany)
	}
	if !got.IsActive {
		t.Fatal("IsActive = false, want true")
	}

	byID, err := store.UserByID(ctx, id)
	if err != nil || byID == nil {
		t.Fatalf("UserByID(%d) = %v, %v", id, byID, err)
	}
	if byID.Email != email {
		t.Fatalf("UserByID email = %q, want %q", byID.Email, email)
	}

	exists, err := store.UserEmailExists(ctx, strings.ToUpper(email))
	if err != nil {
		t.Fatalf("UserEmailExists: %v", err)
	}
	if !exists {
		t.Fatal("UserEmailExists = false, want true")
	}
	missing, err := store.UserEmailExists(ctx, "nobody-"+strconv.Itoa(os.Getpid())+"@example.test")
	if err != nil || missing {
		t.Fatalf("UserEmailExists(unknown) = %v, %v; want false, nil", missing, err)
	}

	// Unknown rows yield (nil, nil): callers map that to 401, not 500.
	if u, err := store.UserByID(ctx, 9223372036854775807); err != nil || u != nil {
		t.Fatalf("UserByID(unknown) = %v, %v; want nil, nil", u, err)
	}
	if u, err := store.UserByEmail(ctx, "nobody-xx@example.test"); err != nil || u != nil {
		t.Fatalf("UserByEmail(unknown) = %v, %v; want nil, nil", u, err)
	}

	// Failure recorder persists the lockout window...
	lockUntil := time.Now().Add(15 * time.Minute).UTC()
	if err := store.RecordLoginFailure(ctx, id, 5, &lockUntil); err != nil {
		t.Fatalf("RecordLoginFailure: %v", err)
	}
	locked, err := store.UserByID(ctx, id)
	if err != nil || locked == nil {
		t.Fatalf("reload after failure: %v, %v", locked, err)
	}
	if locked.FailedAttempts != 5 {
		t.Fatalf("FailedAttempts = %d, want 5", locked.FailedAttempts)
	}
	if locked.LockedUntil == nil {
		t.Fatal("LockedUntil = nil, want timestamp")
	}

	// ...and the success recorder clears it again.
	if err := store.RecordLoginSuccess(ctx, id); err != nil {
		t.Fatalf("RecordLoginSuccess: %v", err)
	}
	cleared, err := store.UserByID(ctx, id)
	if err != nil || cleared == nil {
		t.Fatalf("reload after success: %v, %v", cleared, err)
	}
	if cleared.FailedAttempts != 0 || cleared.LockedUntil != nil {
		t.Fatalf("after success: attempts=%d locked=%v, want 0/nil", cleared.FailedAttempts, cleared.LockedUntil)
	}
}

// TestITStoreVehiclesAndHistory covers the tenant-scoped reads the WS handlers
// depend on: row-level RBAC filtering, search/status/del-soft filters, paging and
// the history window (PRD §8.5, §13 read path).
func TestITStoreVehiclesAndHistory(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Row-level RBAC: an empty AssignedIDs set must never fall back to tenant-wide.
	if rows, total, err := store.ListVehicles(ctx, VehicleQuery{CompanyCode: itCompany, Limit: 5}); err != nil {
		t.Fatalf("ListVehicles(assigned none): %v", err)
	} else if len(rows) != 0 || total != 0 {
		t.Fatalf("ListVehicles(assigned none) = %d rows/%d total, want 0/0", len(rows), total)
	}

	// Tenant-wide read: page 1 must be a subset of the reported total and the
	// limit must be honoured.
	page1, total, err := store.ListVehicles(ctx, VehicleQuery{CompanyCode: itCompany, AllVehicles: true, Page: 1, Limit: 2})
	if err != nil {
		t.Fatalf("ListVehicles(page1): %v", err)
	}
	if len(page1) > 2 {
		t.Fatalf("ListVehicles(page1) returned %d rows, limit was 2", len(page1))
	}
	if total < int64(len(page1)) {
		t.Fatalf("total = %d < rows = %d", total, len(page1))
	}
	for _, v := range page1 {
		if v.ID == 0 || v.IMEI == "" {
			t.Fatalf("vehicle row incompletely scanned: id=%d imei=%q", v.ID, v.IMEI)
		}
	}

	// Status filter is applied in SQL, not post-filtered: every returned row matches.
	if len(page1) > 0 {
		first, _, err := store.ListVehicles(ctx, VehicleQuery{
			CompanyCode: itCompany, AllVehicles: true, Status: page1[0].Status,
			// Precondition: Page >= 1 — parsePagination rejects page < 1 with 400, so
			// the store never computes a negative OFFSET in production.
			Page: 1, Limit: 20})
		if err != nil {
			t.Fatalf("ListVehicles(status=%q): %v", page1[0].Status, err)
		}
		for _, v := range first {
			if v.Status != page1[0].Status {
				t.Fatalf("status filter leaked row %d: %q != %q", v.ID, v.Status, page1[0].Status)
			}
		}
	}

	// Paging must not repeat rows across pages.
	if total > 2 {
		page2, _, err := store.ListVehicles(ctx, VehicleQuery{CompanyCode: itCompany, AllVehicles: true, Page: 2, Limit: 2})
		if err != nil {
			t.Fatalf("ListVehicles(page2): %v", err)
		}
		seen := map[int64]bool{}
		for _, v := range page1 {
			seen[v.ID] = true
		}
		for _, v := range page2 {
			if seen[v.ID] {
				t.Fatalf("vehicle %d returned on both page 1 and page 2", v.ID)
			}
		}
	}

	// History: resolve a real vehicle through the tenant pool (read-split aware),
	// then query its window. Even an empty result must succeed with the right shape.
	if len(page1) == 0 {
		t.Skip("tenant has no vehicles to exercise history")
	}
	v := page1[0]
	rows, histTotal, err := store.VehicleHistory(ctx, HistoryQuery{
		CompanyCode: itCompany, VehicleID: v.ID, IMEI: v.IMEI,
		From: time.Now().Add(-24 * time.Hour), To: time.Now().Add(time.Hour),
		Page: 1, Limit: 5,
	})
	if err != nil {
		t.Fatalf("VehicleHistory(%d): %v", v.ID, err)
	}
	if len(rows) > 5 {
		t.Fatalf("VehicleHistory returned %d rows, limit was 5", len(rows))
	}
	if histTotal < int64(len(rows)) {
		t.Fatalf("history total = %d < page rows = %d", histTotal, len(rows))
	}
	// The handler contract is RFC3339 UTC strings ordered newest-first.
	var prev time.Time
	for i, r := range rows {
		ts, perr := time.Parse(time.RFC3339, r.Timestamp)
		if perr != nil {
			t.Fatalf("history row %d timestamp %q is not RFC3339: %v", i, r.Timestamp, perr)
		}
		if ts.Before(time.Now().Add(-25*time.Hour)) || ts.After(time.Now().Add(2*time.Hour)) {
			t.Fatalf("history row %d outside requested window: %s", i, r.Timestamp)
		}
		if i > 0 && ts.After(prev) {
			t.Fatalf("history rows not newest-first at %d: %s after %s", i, r.Timestamp, prev.Format(time.RFC3339))
		}
		prev = ts
	}

	// Unknown vehicle: empty window, no error (404 is decided by the handler).
	if rows, unkTotal, err := store.VehicleHistory(ctx, HistoryQuery{
		CompanyCode: itCompany, VehicleID: 9223372036854775807,
		From: time.Now().Add(-time.Hour), To: time.Now(), Page: 1, Limit: 5,
	}); err != nil || len(rows) != 0 || unkTotal != 0 {
		t.Fatalf("VehicleHistory(unknown) = %d rows/%d total, %v; want 0/0, nil", len(rows), unkTotal, err)
	}
}

// TestITStoreWriteAudit covers the audit sink (PRD §9.4): batching, NULL mapping
// for empty fields, JSONB payload round-trip and the append-only guarantee.
//
// Fixture rows are intentionally NOT deleted: tm_audit_logs is append-only by
// contract, so cleanup is impossible without disabling the trigger. They carry a
// unique per-process action prefix so repeated runs stay distinguishable.
func TestITStoreWriteAudit(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Empty batch is a no-op (the flush loop may hand over nothing).
	if err := store.WriteAudit(ctx, nil); err != nil {
		t.Fatalf("WriteAudit(nil): %v", err)
	}

	action := "it.store.fixture." + strconv.Itoa(os.Getpid())
	requestID := "it-req-" + strconv.Itoa(os.Getpid())
	rows := []AuditRow{
		{
			Action: action, Outcome: "success", ActorUserID: 4242,
			ActorEmail: "it-store@example.test", ActorRole: "admin", ActorIP: "127.0.0.1",
			ActorUserAgent: "it-harness", CompanyCode: itCompany, EntityType: "vehicle",
			EntityID: "it-1", BeforeState: map[string]any{"status": "idle"},
			AfterState: map[string]any{"status": "moving"}, Reason: "fixture", RequestID: requestID,
		},
		{
			// All optional fields empty -> SQL NULL, not empty string.
			Action: action, Outcome: "denied",
		},
	}
	if err := store.WriteAudit(ctx, rows); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	type auditRow struct {
		outcome   string
		actorID   *int64
		email     *string
		company   *string
		before    *string
		after     *string
		reason    *string
		requestID *string
	}
	q := `SELECT outcome, actor_user_id, actor_email, company_code,
             before_state::text, after_state::text, reason, request_id
      FROM tm_audit_logs WHERE action = $1 ORDER BY audit_id`
	dbrows, err := store.tenants.Master().DB.QueryContext(ctx, q, action)
	if err != nil {
		t.Fatalf("read back audit: %v", err)
	}
	defer dbrows.Close()

	var got []auditRow
	for dbrows.Next() {
		var r auditRow
		if err := dbrows.Scan(&r.outcome, &r.actorID, &r.email, &r.company,
			&r.before, &r.after, &r.reason, &r.requestID); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		got = append(got, r)
	}
	if err := dbrows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("persisted %d audit rows, want 2", len(got))
	}

	first := got[0]
	if first.outcome != "success" {
		t.Fatalf("outcome = %q, want success", first.outcome)
	}
	if first.actorID == nil || *first.actorID != 4242 {
		t.Fatalf("actor_user_id = %v, want 4242", first.actorID)
	}
	if first.email == nil || *first.email != "it-store@example.test" {
		t.Fatalf("actor_email = %v, want it-store@example.test", first.email)
	}
	if first.company == nil || *first.company != itCompany {
		t.Fatalf("company_code = %v, want %s", first.company, itCompany)
	}
	if first.requestID == nil || *first.requestID != requestID {
		t.Fatalf("request_id = %v, want %s", first.requestID, requestID)
	}
	if first.before == nil || !strings.Contains(*first.before, `"status": "idle"`) {
		t.Fatalf("before_state = %v, want JSON containing status idle", first.before)
	}
	if first.after == nil || !strings.Contains(*first.after, `"status": "moving"`) {
		t.Fatalf("after_state = %v, want JSON containing status moving", first.after)
	}

	// Empty optional fields must land as NULL, not "".
	second := got[1]
	if second.actorID != nil || second.email != nil || second.company != nil || second.reason != nil || second.before != nil {
		t.Fatalf("empty fields were not stored as NULL: %+v", second)
	}

	// Append-only: the DB itself must reject mutation of an audit row.
	if _, err := store.tenants.Master().DB.ExecContext(ctx,
		`UPDATE tm_audit_logs SET outcome = 'failure' WHERE action = $1`, action); err == nil {
		t.Fatal("UPDATE on tm_audit_logs succeeded, want rejection by trigger")
	}
	if _, err := store.tenants.Master().DB.ExecContext(ctx,
		`DELETE FROM tm_audit_logs WHERE action = $1`, action); err == nil {
		t.Fatal("DELETE on tm_audit_logs succeeded, want rejection by trigger")
	}
}

// itTenantExec runs a fixture-maintenance statement on the DEV001 schema (used
// to simulate soft deletes so the RBAC queries can be exercised). The statement
// is a constant in this file, never user input.
func itTenantExec(t *testing.T, store *PostgresStore, query string, args ...any) error {
	t.Helper()
	pool, err := store.tenantPool(itCompany)
	if err != nil {
		t.Fatalf("tenant pool: %v", err)
	}
	if _, err := pool.DB.Exec(query, args...); err != nil {
		t.Fatalf("fixture exec %q: %v", query, err)
	}
	return nil
}

// TestITStoreTenantAccessAndAssignments covers the row-level RBAC layer (PRD
// §3.1/§9.2): per-tenant role resolution, access upsert idempotency, vehicle
// grants and the IDOR guard. Before this suite the whole file sat at 0 %.
func TestITStoreTenantAccessAndAssignments(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	uid, _ := itUser(t, store, "rbac")

	// No access row yet: the caller must be able to tell "not a member" apart
	// from "database failure" (found=false, err=nil).
	role, active, found, err := store.TenantAccess(ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("TenantAccess(no row): %v", err)
	}
	if found || active || role != "" {
		t.Fatalf("TenantAccess(no row) = %q/%v/%v, want empty/false/false", role, active, found)
	}

	t.Cleanup(func() {
		_ = itTenantExec(t, store, "DELETE FROM tm_user_vehicles WHERE user_id = $1", uid)
		_ = itTenantExec(t, store, "DELETE FROM tm_user_company_access WHERE user_id = $1", uid)
	})

	// Upsert twice: the second write must UPDATE, never duplicate (uq on user_id).
	if err := store.UpsertTenantAccess(ctx, itCompany, uid, "Manager"); err != nil {
		t.Fatalf("UpsertTenantAccess(Manager): %v", err)
	}
	if err := store.UpsertTenantAccess(ctx, itCompany, uid, "Operator"); err != nil {
		t.Fatalf("UpsertTenantAccess(Operator): %v", err)
	}
	role, active, found, err = store.TenantAccess(ctx, itCompany, uid)
	if err != nil || !found {
		t.Fatalf("TenantAccess(after upsert) = %v, %v, found=%v", role, err, found)
	}
	if role != "Operator" {
		t.Fatalf("role_override = %q, want Operator (last upsert wins)", role)
	}
	if !active {
		t.Fatal("is_active = false, want true")
	}
	var accessRows int
	if err := store.tenants.Master().DB.QueryRowContext(ctx,
		`SELECT count(*) FROM adatrack_gps_dev001.tm_user_company_access WHERE user_id = $1`, uid).Scan(&accessRows); err != nil {
		t.Fatalf("count access rows: %v", err)
	}
	if accessRows != 1 {
		t.Fatalf("access rows = %d, want 1 (upsert must not duplicate)", accessRows)
	}

	// A soft-deleted access row means "not a member" again.
	if err := itTenantExec(t, store,
		"UPDATE tm_user_company_access SET deleted_at = CURRENT_TIMESTAMP WHERE user_id = $1", uid); err != nil {
		t.Fatalf("soft delete access: %v", err)
	}
	if _, _, found, err = store.TenantAccess(ctx, itCompany, uid); err != nil || found {
		t.Fatalf("TenantAccess(soft-deleted) found=%v err=%v, want false/nil", found, err)
	}

	// Tenant onboarding guard: a real tenant exists, an unknown one never does.
	if !store.TenantExists(ctx, itCompany) {
		t.Fatalf("TenantExists(%s) = false, want true", itCompany)
	}
	if store.TenantExists(ctx, "NOSUCHTENANT") {
		t.Fatal("TenantExists(NOSUCHTENANT) = true, want false")
	}

	// Master-side helper used by FR-5.5 idempotency.
	if exists, err := store.CompanyExists(ctx, strings.ToLower(itCompany)); err != nil || !exists {
		t.Fatalf("CompanyExists(%s) = %v, %v; want true, nil", itCompany, exists, err)
	}
	if exists, err := store.CompanyExists(ctx, "NOSUCHTENANT"); err != nil || exists {
		t.Fatalf("CompanyExists(unknown) = %v, %v; want false, nil", exists, err)
	}

	// Vehicle grants: pick real vehicles from the tenant (read-only use).
	rows, _, err := store.ListVehicles(ctx, VehicleQuery{CompanyCode: itCompany, AllVehicles: true, Page: 1, Limit: 3})
	if err != nil {
		t.Fatalf("ListVehicles for fixture: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("tenant has no vehicles to exercise the assignment layer")
	}
	ids := []int64{rows[0].ID}
	if len(rows) > 1 {
		ids = append(ids, rows[1].ID)
	}

	// Idempotent grants: assigning twice must not duplicate rows.
	if err := store.AssignVehicles(ctx, itCompany, uid, ids); err != nil {
		t.Fatalf("AssignVehicles: %v", err)
	}
	if err := store.AssignVehicles(ctx, itCompany, uid, ids); err != nil {
		t.Fatalf("AssignVehicles (2nd): %v", err)
	}
	assigned, err := store.AssignedVehicleIDs(ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("AssignedVehicleIDs: %v", err)
	}
	if len(assigned) != len(ids) {
		t.Fatalf("assigned = %v, want %v (no duplicates)", assigned, ids)
	}
	for i, id := range ids {
		if assigned[i] != id {
			t.Fatalf("assigned[%d] = %d, want %d (ordered by vehicle_id)", i, assigned[i], id)
		}
	}

	// A soft-deleted grant disappears from the assigned set... then re-assignment
	// revives it (the ON CONFLICT DO UPDATE branch).
	if err := itTenantExec(t, store,
		"UPDATE tm_user_vehicles SET deleted_at = CURRENT_TIMESTAMP WHERE user_id = $1", uid); err != nil {
		t.Fatalf("soft delete grant: %v", err)
	}
	if gone, err := store.AssignedVehicleIDs(ctx, itCompany, uid); err != nil || len(gone) != 0 {
		t.Fatalf("AssignedVehicleIDs(soft-deleted) = %v, %v; want empty", gone, err)
	}
	if err := store.AssignVehicles(ctx, itCompany, uid, ids); err != nil {
		t.Fatalf("AssignVehicles (revive): %v", err)
	}
	if revived, err := store.AssignedVehicleIDs(ctx, itCompany, uid); err != nil || len(revived) != len(ids) {
		t.Fatalf("AssignedVehicleIDs(after revive) = %v, %v; want %v", revived, err, ids)
	}

	// IDOR guard: unknown ids are filtered out, existing ones survive.
	if got, err := store.ExistingVehicleIDs(ctx, itCompany, append(append([]int64{}, ids...), 9223372036854775807)); err != nil {
		t.Fatalf("ExistingVehicleIDs: %v", err)
	} else if len(got) != len(ids) {
		t.Fatalf("ExistingVehicleIDs = %v, want %v (unknown id must be dropped)", got, ids)
	}
	if got, err := store.ExistingVehicleIDs(ctx, itCompany, nil); err != nil || got != nil {
		t.Fatalf("ExistingVehicleIDs(nil) = %v, %v; want nil, nil", got, err)
	}

	// VehicleByID / VehicleMeta back the WS fan-out and the 404-vs-403 decision.
	v, err := store.VehicleByID(ctx, itCompany, ids[0], false)
	if err != nil || v == nil {
		t.Fatalf("VehicleByID(%d) = %v, %v", ids[0], v, err)
	}
	if v.IMEI != rows[0].IMEI || v.PlateNumber != rows[0].PlateNumber {
		t.Fatalf("VehicleByID mismatch: imei=%q plate=%q", v.IMEI, v.PlateNumber)
	}
	if missing, err := store.VehicleByID(ctx, itCompany, 9223372036854775807, false); err != nil || missing != nil {
		t.Fatalf("VehicleByID(unknown) = %v, %v; want nil, nil", missing, err)
	}
	meta, err := store.VehicleMeta(ctx, itCompany, ids[0])
	if err != nil {
		t.Fatalf("VehicleMeta(%d): %v", ids[0], err)
	}
	if meta.ID != ids[0] || meta.IMEI != rows[0].IMEI {
		t.Fatalf("VehicleMeta = id %d imei %q, want %d/%q", meta.ID, meta.IMEI, ids[0], rows[0].IMEI)
	}
	if empty, err := store.VehicleMeta(ctx, itCompany, 9223372036854775807); err != nil || empty.ID != 0 {
		t.Fatalf("VehicleMeta(unknown) = %+v, %v; want zero value", empty, err)
	}
}
