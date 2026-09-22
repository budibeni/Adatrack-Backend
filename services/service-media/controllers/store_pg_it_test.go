package controllers

// store_pg_it_test.go — integration coverage for service-media's PostgresStore
// against a LIVE PostgreSQL (PRD §6.2 master + per-tenant schemas).
//
// Sebelum suite ini seluruh lapisan store (store_pg.go / store_pg_catalog.go /
// store_pg_media.go / store_pg_audit.go, 714 baris, 20 metode) berada di 0 %:
// unit test modul ini memakai fake store, sehingga SQL katalog media, RBAC
// row-level, dan audit yang sebenarnya tidak pernah dieksekusi.
//
// Opt-in via ADATRACK_IT=1 (go test ./... tetap hermetik). Fixture memakai marka
// unik per proses dan dihapus di t.Cleanup; host/port sama dengan
// scripts/start-services.sh (127.0.0.1:5533).

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/service-media/models"
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

// itEnvOr reads an env var or returns def.
func itEnvOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// newITStore wires a real PostgresStore on the DEV001 tenant. The tenant manager
// is closed in t.Cleanup.
func newITStore(t *testing.T) (*PostgresStore, *tenant.Manager) {
	t.Helper()
	skipNoDB(t)

	internal.LoadProjectEnv()
	t.Setenv("POSTGRES_HOST", itEnvOr("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", itEnvOr("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", itEnvOr("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", itEnvOr("ADATRACK_IT_REDIS_PORT", "6380"))

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

// itUser inserts a master auth row (Admin, the only role allowed by the tm_users
// CHECK constraint) and deletes it in t.Cleanup.
func itUser(t *testing.T, store *PostgresStore, tag string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	email := "it-media-" + tag + "-" + strconv.Itoa(os.Getpid()) + "@example.test"
	var id int64
	err := store.Master().DB.QueryRowContext(ctx, `
INSERT INTO tm_users (company_id, company_code, email, password_hash, full_name,
                      global_role, is_active, must_change_password, email_verified)
VALUES ((SELECT id FROM tm_companies WHERE code = $1), $1, $2, $3, $4, $5, TRUE, FALSE, FALSE)
RETURNING id`,
		itCompany, email, "$2a$12$itfixturehashnotusedforauth", "IT Media Fixture", "Admin").Scan(&id)
	if err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	t.Cleanup(func() {
		_, cerr := store.Master().DB.Exec(`DELETE FROM tm_users WHERE id = $1`, id)
		if cerr != nil {
			t.Errorf("cleanup tm_users id=%d: %v", id, cerr)
		}
	})
	return id
}

// itTenantExec runs a fixture statement on the DEV001 schema.
func itTenantExec(t *testing.T, store *PostgresStore, query string, args ...any) {
	t.Helper()
	pool, err := store.tenantPool(itCompany)
	if err != nil {
		t.Fatalf("tenant pool: %v", err)
	}
	if _, err := pool.DB.Exec(query, args...); err != nil {
		t.Fatalf("fixture exec %q: %v", query, err)
	}
}

// itFirstVehicle returns one existing vehicle of the tenant (read-only use).
func itFirstVehicle(t *testing.T, store *PostgresStore) (int64, string) {
	t.Helper()
	pool, err := store.tenantPool(itCompany)
	if err != nil {
		t.Fatalf("tenant pool: %v", err)
	}
	var id int64
	var imei string
	err = pool.DB.QueryRow(`SELECT id, COALESCE(imei, '') FROM tm_vehicles
WHERE deleted_at IS NULL ORDER BY id LIMIT 1`).Scan(&id, &imei)
	if err != nil {
		t.Skipf("tenant has no vehicle to exercise media fixtures: %v", err)
	}
	return id, imei
}

// TestITStoreReadinessAndLookups covers the read surfaces the handlers rely on:
// master/tenant health, tenant vehicle lookup, the anti-spoofing IMEI allowlist,
// and the per-company media configuration (FR-8.1/8.7).
func TestITStoreReadinessAndLookups(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if store.Master() == nil {
		t.Fatal("Master() = nil")
	}
	if err := store.TenantHealth(ctx); err != nil {
		t.Fatalf("TenantHealth: %v", err)
	}

	// Tenant pool resolution is normalised (trim + upper) and unknown codes fail.
	if _, err := store.tenantPool(" dev001 "); err != nil {
		t.Fatalf("tenantPool(normalised): %v", err)
	}
	if _, err := store.tenantPool("NOSUCHTENANT"); err == nil {
		t.Fatal("tenantPool(unknown) = nil error, want error")
	}

	vehicleID, imei := itFirstVehicle(t, store)
	v, err := store.VehicleByID(ctx, itCompany, vehicleID, false)
	if err != nil || v == nil {
		t.Fatalf("VehicleByID(%d) = %v, %v", vehicleID, v, err)
	}
	if v.ID != vehicleID || v.Deleted {
		t.Fatalf("VehicleByID = %+v, want id %d and not deleted", v, vehicleID)
	}
	if missing, err := store.VehicleByID(ctx, itCompany, 9223372036854775807, false); err != nil || missing != nil {
		t.Fatalf("VehicleByID(unknown) = %v, %v; want nil, nil", missing, err)
	}

	// The allowlist is master-side (anti-spoofing): a registered IMEI resolves to
	// its tenant, anything else must be nil (never an error).
	if imei != "" {
		ref, err := store.ResolveVehicleByIMEI(ctx, imei)
		if err != nil {
			t.Fatalf("ResolveVehicleByIMEI(%s): %v", imei, err)
		}
		if ref == nil {
			t.Fatalf("ResolveVehicleByIMEI(%s) = nil for a tenant vehicle", imei)
		}
		if ref.CompanyCode != itCompany || ref.VehicleID != vehicleID {
			t.Fatalf("ResolveVehicleByIMEI = %+v, want %s/%d", ref, itCompany, vehicleID)
		}
	}
	if ref, err := store.ResolveVehicleByIMEI(ctx, "999999999999999"); err != nil || ref != nil {
		t.Fatalf("ResolveVehicleByIMEI(unknown) = %v, %v; want nil, nil", ref, err)
	}

	// Media configuration: every active tenant is listed (LEFT JOIN, so a tenant
	// without a config row still appears with zero values).
	companies, err := store.MediaCompanies(ctx)
	if err != nil {
		t.Fatalf("MediaCompanies: %v", err)
	}
	var found bool
	for _, c := range companies {
		if c.CompanyCode != strings.ToUpper(c.CompanyCode) {
			t.Fatalf("MediaCompanies code not normalised: %q", c.CompanyCode)
		}
		if c.CompanyCode == itCompany {
			found = true
		}
	}
	if !found {
		t.Fatalf("MediaCompanies tidak memuat %s: %+v", itCompany, companies)
	}
}

// TestITStoreRBACAndRevocation pins the row-level authorization reads — including
// the two defects found while writing this suite:
//  1. `tm_user_vehicles` has NO `is_active` column, so the previous
//     `COALESCE(is_active, TRUE)` filter made every non-Admin media request fail
//     with "column does not exist" → 503 authorization backend unavailable.
//  2. Neither read filtered `deleted_at`, so a REVOKED membership/grant still
//     authorized (every other service filters it).
func TestITStoreRBACAndRevocation(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	uid := itUser(t, store, "rbac")
	vehicleID, _ := itFirstVehicle(t, store)
	t.Cleanup(func() {
		itTenantExec(t, store, "DELETE FROM tm_user_vehicles WHERE user_id = $1", uid)
		itTenantExec(t, store, "DELETE FROM tm_user_company_access WHERE user_id = $1", uid)
	})

	// No access row yet: "not a member" must be distinguishable from a DB failure.
	role, active, exists, err := store.TenantAccess(ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("TenantAccess(no row): %v", err)
	}
	if exists || active || role != "" {
		t.Fatalf("TenantAccess(no row) = %q/%v/%v, want empty/false/false", role, active, exists)
	}

	itTenantExec(t, store, `INSERT INTO tm_user_company_access (user_id, role_override, is_active)
VALUES ($1, 'Operator', TRUE)`, uid)
	role, active, exists, err = store.TenantAccess(ctx, itCompany, uid)
	if err != nil || !exists {
		t.Fatalf("TenantAccess = %v, %v, found=%v", role, err, exists)
	}
	if role != "Operator" || !active {
		t.Fatalf("TenantAccess = %q/%v, want Operator/true", role, active)
	}

	// REVOKED membership (soft delete) must not authorize.
	itTenantExec(t, store, "UPDATE tm_user_company_access SET deleted_at = CURRENT_TIMESTAMP WHERE user_id = $1", uid)
	if _, _, exists, err = store.TenantAccess(ctx, itCompany, uid); err != nil || exists {
		t.Fatalf("TenantAccess(revoked) found=%v err=%v, want false/nil", exists, err)
	}

	// Vehicle grants: this call used to fail outright (missing column).
	itTenantExec(t, store, "INSERT INTO tm_user_vehicles (user_id, vehicle_id) VALUES ($1, $2)", uid, vehicleID)
	ids, err := store.AssignedVehicleIDs(ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("AssignedVehicleIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != vehicleID {
		t.Fatalf("AssignedVehicleIDs = %v, want [%d]", ids, vehicleID)
	}

	// REVOKED grant (soft delete) must disappear from the row-level filter.
	itTenantExec(t, store, "UPDATE tm_user_vehicles SET deleted_at = CURRENT_TIMESTAMP WHERE user_id = $1", uid)
	ids, err = store.AssignedVehicleIDs(ctx, itCompany, uid)
	if err != nil {
		t.Fatalf("AssignedVehicleIDs(revoked): %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("AssignedVehicleIDs(revoked) = %v, want empty (revocation harus berlaku)", ids)
	}

	// An empty grant list means ZERO vehicles — never the whole tenant.
	if ids, err := store.AssignedVehicleIDs(ctx, itCompany, 9223372036854775807); err != nil || len(ids) != 0 {
		t.Fatalf("AssignedVehicleIDs(unknown user) = %v, %v; want empty", ids, err)
	}
}

// itHasEvent reports whether the catalog page contains one id.
func itHasEvent(rows []models.MediaEvent, id int64) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// itCreatePending inserts one pending catalog row with a unique object key and
// schedules its removal (th_media_events has no immutability trigger).
func itCreatePending(t *testing.T, store *PostgresStore, vehicleID int64, imei, suffix string) (int64, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	key := "it-media-" + strconv.Itoa(os.Getpid()) + "-" + suffix
	id, err := store.CreateMediaEvent(ctx, itCompany, &models.MediaEvent{
		VehicleID:     vehicleID,
		IMEI:          imei,
		EventType:     models.EventTypeManual,
		ObjectKey:     key,
		ContentSHA256: strings.Repeat("a", 64),
		Status:        models.StatusPending,
		HMACVerified:  true,
		UploadSource:  "multipart",
		CapturedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateMediaEvent(%s): %v", suffix, err)
	}
	t.Cleanup(func() {
		pool, perr := store.tenantPool(itCompany)
		if perr != nil {
			t.Errorf("cleanup pool: %v", perr)
			return
		}
		if _, derr := pool.DB.Exec(`DELETE FROM th_media_events WHERE object_key = $1`, key); derr != nil {
			t.Errorf("cleanup media event %s: %v", key, derr)
		}
	})
	return id, key
}

// TestITStoreMediaCatalog covers the media catalog lifecycle on a live schema:
// create -> read -> filter/paginate -> complete -> soft delete -> restore ->
// retention selection (FR-8.3/8.4/8.7/8.9).
func TestITStoreMediaCatalog(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	vehicleID, imei := itFirstVehicle(t, store)
	base := MediaQuery{CompanyCode: itCompany, AllVehicles: true, IMEI: imei, EventType: models.EventTypeManual, Limit: 100}

	_, totalBefore, err := store.ListMediaEvents(ctx, base)
	if err != nil {
		t.Fatalf("ListMediaEvents(baseline): %v", err)
	}
	storedBefore, err := store.CountStoredObjects(ctx, itCompany)
	if err != nil {
		t.Fatalf("CountStoredObjects(baseline): %v", err)
	}

	id1, key1 := itCreatePending(t, store, vehicleID, imei, "lifecycle")

	// CreateMediaEvent persisted every field faithfully.
	event, err := store.MediaEventByID(ctx, itCompany, id1, false)
	if err != nil || event == nil {
		t.Fatalf("MediaEventByID(%d) = %v, %v", id1, event, err)
	}
	if event.Status != models.StatusPending || event.ObjectKey != key1 {
		t.Fatalf("created event = status %q key %q, want pending/%s", event.Status, event.ObjectKey, key1)
	}
	if !event.HMACVerified || event.UploadSource != "multipart" || event.ContentSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("created event lost fields: %+v", event)
	}
	if event.VehicleID != vehicleID || event.IMEI != imei {
		t.Fatalf("created event vehicle/imei = %d/%s, want %d/%s", event.VehicleID, event.IMEI, vehicleID, imei)
	}

	// Lookup by object key (JSON-flow completion) returns the same row.
	byKey, err := store.MediaEventByObjectKey(ctx, itCompany, key1)
	if err != nil || byKey == nil || byKey.ID != id1 {
		t.Fatalf("MediaEventByObjectKey = %v, %v; want id %d", byKey, err, id1)
	}
	if missing, err := store.MediaEventByObjectKey(ctx, itCompany, "it-media-absent-key"); err != nil || missing != nil {
		t.Fatalf("MediaEventByObjectKey(absent) = %v, %v; want nil, nil", missing, err)
	}

	// Filters: the fixture is the only new row for its IMEI/event type.
	rows, total, err := store.ListMediaEvents(ctx, base)
	if err != nil {
		t.Fatalf("ListMediaEvents: %v", err)
	}
	if total != totalBefore+1 || !itHasEvent(rows, id1) {
		t.Fatalf("ListMediaEvents total %d (want %d) contains=%v", total, totalBefore+1, itHasEvent(rows, id1))
	}
	for _, r := range rows {
		if r.IMEI != imei || r.EventType != models.EventTypeManual {
			t.Fatalf("filter bocor: %+v", r)
		}
	}

	// Status filter.
	pending := base
	pending.Status = models.StatusPending
	if rows, _, err := store.ListMediaEvents(ctx, pending); err != nil || !itHasEvent(rows, id1) {
		t.Fatalf("ListMediaEvents(status=pending) contains=%v err=%v", itHasEvent(rows, id1), err)
	}
	completeFilter := base
	completeFilter.Status = models.StatusComplete
	if rows, _, err := store.ListMediaEvents(ctx, completeFilter); err != nil || itHasEvent(rows, id1) {
		t.Fatalf("pending event muncul di filter complete (err=%v)", err)
	}

	// Vehicle filter + paging: page 2 of a 1-per-page window excludes the only row.
	byVehicle := base
	byVehicle.VehicleID = vehicleID
	byVehicle.Limit = 1
	if rows, _, err := store.ListMediaEvents(ctx, byVehicle); err != nil || !itHasEvent(rows, id1) {
		t.Fatalf("ListMediaEvents(vehicle=%d) contains=%v err=%v", vehicleID, itHasEvent(rows, id1), err)
	}
	byVehicle.Page = 2
	if rows, _, err := store.ListMediaEvents(ctx, byVehicle); err != nil || itHasEvent(rows, id1) {
		t.Fatalf("paging page=2 masih memuat baris yang sama (err=%v)", err)
	}

	// Row-level RBAC: an empty grant list yields ZERO rows, never the tenant.
	if rows, total, err := store.ListMediaEvents(ctx, MediaQuery{CompanyCode: itCompany, Limit: 10}); err != nil {
		t.Fatalf("ListMediaEvents(no grants): %v", err)
	} else if len(rows) != 0 || total != 0 {
		t.Fatalf("ListMediaEvents(no grants) = %d rows/%d total, want 0/0", len(rows), total)
	}

	// Time window filter (captured_at).
	windowed := base
	windowed.From = time.Now().Add(-time.Hour)
	windowed.To = time.Now().Add(time.Hour)
	if rows, _, err := store.ListMediaEvents(ctx, windowed); err != nil || !itHasEvent(rows, id1) {
		t.Fatalf("ListMediaEvents(window) contains=%v err=%v", itHasEvent(rows, id1), err)
	}
	outside := base
	outside.To = time.Now().Add(-24 * time.Hour)
	if rows, _, err := store.ListMediaEvents(ctx, outside); err != nil || itHasEvent(rows, id1) {
		t.Fatalf("ListMediaEvents(window lampau) memuat baris baru (err=%v)", err)
	}

	// A `pending` upload is not yet a stored object (FR-8.8 metric semantics).
	if storedAfter, err := store.CountStoredObjects(ctx, itCompany); err != nil {
		t.Fatalf("CountStoredObjects: %v", err)
	} else if storedAfter != storedBefore {
		t.Fatalf("CountStoredObjects naik untuk baris pending: %d -> %d", storedBefore, storedAfter)
	}
}

// TestITStoreMediaLifecycleAndRetention covers completion, soft delete/restore
// and the retention candidate selection (FR-8.7/8.9) on a live schema.
func TestITStoreMediaLifecycleAndRetention(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	vehicleID, imei := itFirstVehicle(t, store)
	storedBefore, err := store.CountStoredObjects(ctx, itCompany)
	if err != nil {
		t.Fatalf("CountStoredObjects: %v", err)
	}

	id1, _ := itCreatePending(t, store, vehicleID, imei, "complete")
	now := time.Now().UTC()
	expires := now.Add(7 * 24 * time.Hour)
	completed := now
	if err := store.CompleteMediaEvent(ctx, itCompany, &models.MediaEvent{
		ID: id1, FileSize: 4096, MimeType: "video/mp4", ObjectETag: "etag-it",
		RetentionDays: 7, CompletedAt: &completed, ExpiresAt: &expires,
	}); err != nil {
		t.Fatalf("CompleteMediaEvent: %v", err)
	}

	event, err := store.MediaEventByID(ctx, itCompany, id1, false)
	if err != nil || event == nil {
		t.Fatalf("MediaEventByID setelah complete: %v, %v", event, err)
	}
	if event.Status != models.StatusComplete {
		t.Fatalf("status = %q, want complete", event.Status)
	}
	if event.FileSize != 4096 || event.MimeType != "video/mp4" || event.ObjectETag != "etag-it" || event.RetentionDays != 7 {
		t.Fatalf("metadata complete tidak tersimpan: %+v", event)
	}
	if event.CompletedAt == nil || event.ExpiresAt == nil {
		t.Fatalf("completed_at/expires_at nil setelah complete: %+v", event)
	}

	// Idempotent-safe: a second completion is a state-transition conflict.
	if err := store.CompleteMediaEvent(ctx, itCompany, &models.MediaEvent{ID: id1}); err == nil {
		t.Fatal("CompleteMediaEvent (2x) = nil, want conflict")
	}
	if err := store.CompleteMediaEvent(ctx, itCompany, &models.MediaEvent{ID: 9223372036854775807}); err == nil {
		t.Fatal("CompleteMediaEvent(unknown id) = nil, want conflict")
	}

	// `complete` counts as a stored object.
	if after, err := store.CountStoredObjects(ctx, itCompany); err != nil {
		t.Fatalf("CountStoredObjects: %v", err)
	} else if after != storedBefore+1 {
		t.Fatalf("CountStoredObjects = %d, want %d", after, storedBefore+1)
	}

	// Soft delete: hidden from normal reads, visible with includeDeleted, still
	// counted as stored (the object remains until retention).
	if err := store.SoftDeleteMediaEvent(ctx, itCompany, id1, 4242, "it fixture delete"); err != nil {
		t.Fatalf("SoftDeleteMediaEvent: %v", err)
	}
	if hidden, err := store.MediaEventByID(ctx, itCompany, id1, false); err != nil || hidden != nil {
		t.Fatalf("MediaEventByID(hidden) = %v, %v; want nil", hidden, err)
	}
	deleted, err := store.MediaEventByID(ctx, itCompany, id1, true)
	if err != nil || deleted == nil {
		t.Fatalf("MediaEventByID(includeDeleted) = %v, %v", deleted, err)
	}
	if deleted.Status != models.StatusDeleted || deleted.DeleteReason != "it fixture delete" || deleted.DeletedAt == nil {
		t.Fatalf("soft delete tidak terekam: %+v", deleted)
	}
	if err := store.SoftDeleteMediaEvent(ctx, itCompany, id1, 4242, "again"); err == nil {
		t.Fatal("SoftDeleteMediaEvent (2x) = nil, want conflict")
	}
	if err := store.SoftDeleteMediaEvent(ctx, itCompany, 9223372036854775807, 4242, "unknown"); err == nil {
		t.Fatal("SoftDeleteMediaEvent(unknown) = nil, want conflict")
	}
	if after, err := store.CountStoredObjects(ctx, itCompany); err != nil {
		t.Fatalf("CountStoredObjects: %v", err)
	} else if after != storedBefore+1 {
		t.Fatalf("baris terhapus-soft tidak lagi dihitung sebagai objek: %d, want %d", after, storedBefore+1)
	}

	// Restore reverses the soft delete (expiry still in the future -> complete).
	if err := store.RestoreMediaEvent(ctx, itCompany, id1); err != nil {
		t.Fatalf("RestoreMediaEvent: %v", err)
	}
	restored, err := store.MediaEventByID(ctx, itCompany, id1, false)
	if err != nil || restored == nil {
		t.Fatalf("MediaEventByID setelah restore: %v, %v", restored, err)
	}
	if restored.Status != models.StatusComplete || restored.DeletedAt != nil {
		t.Fatalf("restore tidak mengembalikan baris: %+v", restored)
	}
	if err := store.RestoreMediaEvent(ctx, itCompany, id1); err == nil {
		t.Fatal("RestoreMediaEvent pada baris aktif = nil, want conflict")
	}

	// Retention candidates: a pending upload past its TTL is selected, and an
	// already-expired row is not selected again.
	id2, _ := itCreatePending(t, store, vehicleID, imei, "pending-ttl")
	candidates, err := store.ExpiredMediaEvents(ctx, itCompany, now, now.Add(time.Minute), 200)
	if err != nil {
		t.Fatalf("ExpiredMediaEvents: %v", err)
	}
	if !itHasEvent(candidates, id2) {
		t.Fatalf("ExpiredMediaEvents tidak memuat pending upload %d", id2)
	}
	if err := store.MarkMediaExpired(ctx, itCompany, []int64{id2}); err != nil {
		t.Fatalf("MarkMediaExpired: %v", err)
	}
	expiredRow, err := store.MediaEventByID(ctx, itCompany, id2, false)
	if err != nil || expiredRow == nil || expiredRow.Status != models.StatusExpired {
		t.Fatalf("MarkMediaExpired tidak mengubah status: %+v (%v)", expiredRow, err)
	}
	if candidates, err := store.ExpiredMediaEvents(ctx, itCompany, now, now.Add(time.Minute), 200); err != nil {
		t.Fatalf("ExpiredMediaEvents(2): %v", err)
	} else if itHasEvent(candidates, id2) {
		t.Fatal("baris yang sudah `expired` masih diusulkan untuk retensi")
	}
	if err := store.MarkMediaExpired(ctx, itCompany, nil); err != nil {
		t.Fatalf("MarkMediaExpired(nil): %v", err)
	}

	// A complete row whose expiry has already passed is a retention candidate.
	id3, _ := itCreatePending(t, store, vehicleID, imei, "expired-horizon")
	past := now.Add(-2 * time.Hour)
	if err := store.CompleteMediaEvent(ctx, itCompany, &models.MediaEvent{
		ID: id3, FileSize: 10, MimeType: "video/mp4", CompletedAt: &past, ExpiresAt: &past,
	}); err != nil {
		t.Fatalf("CompleteMediaEvent(id3): %v", err)
	}
	if candidates, err := store.ExpiredMediaEvents(ctx, itCompany, now, now.Add(-time.Hour), 200); err != nil {
		t.Fatalf("ExpiredMediaEvents(id3): %v", err)
	} else if !itHasEvent(candidates, id3) {
		t.Fatalf("baris complete yang sudah kedaluwarsa tidak diusulkan untuk retensi")
	}
}

// TestITStoreWriteAudit covers the audit sink (PRD §9.4): batching, NULL mapping
// and the append-only guarantee of the shared master table.
//
// Fixture rows are intentionally NOT deleted: tm_audit_logs is append-only by
// contract, so they stay (unique per-process action prefix keeps runs apart).
func TestITStoreWriteAudit(t *testing.T) {
	store, _ := newITStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := store.WriteAudit(ctx, nil); err != nil {
		t.Fatalf("WriteAudit(nil): %v", err)
	}

	action := "it.media.fixture." + strconv.Itoa(os.Getpid())
	requestID := "it-media-req-" + strconv.Itoa(os.Getpid())
	rows := []AuditRow{
		{
			Action: action, Outcome: OutcomeSuccess, ActorUserID: 4242,
			ActorEmail: "it-media@example.test", ActorRole: "Admin", ActorIP: "127.0.0.1",
			ActorUserAgent: "it-harness", CompanyCode: itCompany, EntityType: "media_event",
			EntityID: "it-1", BeforeState: map[string]any{"status": "pending"},
			AfterState: map[string]any{"status": "complete"}, Reason: "fixture", RequestID: requestID,
		},
		{
			// Empty optional fields must land as SQL NULL, and the empty outcome
			// defaults to success (the flush loop may leave it unset).
			Action: action,
		},
	}
	if err := store.WriteAudit(ctx, rows); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	type auditRow struct {
		outcome string
		actorID *int64
		email   *string
		company *string
		before  *string
		after   *string
	}
	dbrows, err := store.Master().DB.QueryContext(ctx, `
SELECT outcome, actor_user_id, actor_email, company_code, before_state::text, after_state::text
FROM tm_audit_logs WHERE action = $1 ORDER BY audit_id`, action)
	if err != nil {
		t.Fatalf("read back audit: %v", err)
	}
	defer dbrows.Close()

	var got []auditRow
	for dbrows.Next() {
		var r auditRow
		if err := dbrows.Scan(&r.outcome, &r.actorID, &r.email, &r.company, &r.before, &r.after); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		got = append(got, r)
	}
	if err := dbrows.Err(); err != nil {
		t.Fatalf("iterate audit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("persisted %d audit rows, want 2", len(got))
	}

	if got[0].outcome != OutcomeSuccess {
		t.Fatalf("outcome = %q, want %q", got[0].outcome, OutcomeSuccess)
	}
	if got[0].actorID == nil || *got[0].actorID != 4242 {
		t.Fatalf("actor_user_id = %v, want 4242", got[0].actorID)
	}
	if got[0].before == nil || !strings.Contains(*got[0].before, "pending") {
		t.Fatalf("before_state = %v, want JSON with pending", got[0].before)
	}
	if got[0].after == nil || !strings.Contains(*got[0].after, "complete") {
		t.Fatalf("after_state = %v, want JSON with complete", got[0].after)
	}
	// The row with an empty outcome also defaults to success.
	if got[1].outcome != OutcomeSuccess {
		t.Fatalf("default outcome = %q, want %q", got[1].outcome, OutcomeSuccess)
	}
	if got[1].actorID != nil || got[1].email != nil || got[1].company != nil {
		t.Fatalf("empty fields tidak menjadi NULL: %+v", got[1])
	}

	// Append-only: the DB rejects mutation of an audit row.
	if _, err := store.Master().DB.ExecContext(ctx,
		`UPDATE tm_audit_logs SET outcome = 'failure' WHERE action = $1`, action); err == nil {
		t.Fatal("UPDATE tm_audit_logs berhasil, want ditolak trigger")
	}
	if _, err := store.Master().DB.ExecContext(ctx,
		`DELETE FROM tm_audit_logs WHERE action = $1`, action); err == nil {
		t.Fatal("DELETE tm_audit_logs berhasil, want ditolak trigger")
	}
}
