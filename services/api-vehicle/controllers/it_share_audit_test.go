package controllers

// it_share_audit_test.go — integration coverage for the two MASTER-schema paths
// that the B12 E2E (`scripts/e2e-enterprise.sh`) surfaced as HTTP 503 on
// 2026-09-29: the audit trail read and the public share links.
//
// Why a dedicated file: the hermetic suite runs on fakeStore (no SQL) and the
// original B11/B12 verification used direct SQL — so the API/store path was
// never exercised against a live database. These tests close that blind spot.

import (
	"testing"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// TestITStoreListAuditLogs is the regression for GET /api/v1/audit-logs → 503.
func TestITStoreListAuditLogs(t *testing.T) {
	h := newITHarness(t)

	logs, total, err := h.store.ListAuditLogs(h.ctx, AuditLogQuery{
		CompanyCode: itCompany,
		Page:        1,
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs failed (the master-schema read behind /audit-logs): %v", err)
	}
	t.Logf("audit logs: total=%d returned=%d", total, len(logs))
}

// TestITStoreSharedVehicles is the regression for the public share read behind
// GET /api/v1/share/{token}, which used to 503 with
// `column "company_code" does not exist (SQLSTATE 42703)`.
func TestITStoreSharedVehicles(t *testing.T) {
	h := newITHarness(t)
	vehicleID, _ := h.itVehicle()

	linkID, err := h.store.CreateShareLink(h.ctx, itCompany, &models.ShareLink{
		Token: "itpub" + itTag() + "0123456789", Scope: "vehicles", VehicleIDs: []int64{vehicleID},
	}, time.Now().UTC().Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("CreateShareLink failed: %v", err)
	}
	t.Cleanup(func() { h.execMaster("DELETE FROM tm_share_links WHERE id = $1", linkID) })

	shared, err := h.store.SharedVehicles(h.ctx, itCompany, []int64{vehicleID})
	if err != nil {
		t.Fatalf("SharedVehicles failed (the read behind the public share payload): %v", err)
	}
	if len(shared) != 1 || shared[0].VehicleID != vehicleID {
		t.Fatalf("SharedVehicles returned %+v, want exactly vehicle %d", shared, vehicleID)
	}
}

// TestITStoreIntegrationEventsArray is the regression for the text[] `events`
// column of tm_integrations (same class as vehicle_ids: pgx returns an array in
// its literal form, which cannot be scanned into []string).
func TestITStoreIntegrationEventsArray(t *testing.T) {
	h := newITHarness(t)

	endpoint := "https://example.invalid/it"
	in := &models.Integration{
		Name:        "it-hook-" + itTag(),
		Kind:        "webhook",
		EndpointURL: &endpoint,
		Status:      "active",
		Events:      []string{"alert.sos", "alert.geofence"},
	}
	id, err := h.store.CreateIntegration(h.ctx, itCompany, in, "hash", 1)
	if err != nil {
		t.Fatalf("CreateIntegration failed: %v", err)
	}
	t.Cleanup(func() { h.execCompany("DELETE FROM tm_integrations WHERE id = $1", id) })

	items, err := h.store.ListIntegrations(h.ctx, itCompany)
	if err != nil {
		t.Fatalf("ListIntegrations failed (the text[] scan): %v", err)
	}
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			if len(item.Events) != 2 {
				t.Fatalf("integration %d events = %v, want 2 entries", id, item.Events)
			}
		}
	}
	if !found {
		t.Fatalf("created integration %d not present in the list", id)
	}
}

// TestITStoreShareLinkRoundTrip is the regression for POST /api/v1/share-links → 503
// (`vehicle_ids` bigint[] could not be scanned into []int64).
func TestITStoreShareLinkRoundTrip(t *testing.T) {
	h := newITHarness(t)

	link := &models.ShareLink{
		Token:      "itprobe" + itTag() + "0123456789",
		Scope:      "vehicles",
		VehicleIDs: []int64{1},
	}
	id, err := h.store.CreateShareLink(h.ctx, itCompany, link, time.Now().UTC().Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("CreateShareLink failed: %v", err)
	}
	t.Cleanup(func() { h.execMaster("DELETE FROM tm_share_links WHERE id = $1", id) })

	links, err := h.store.ListShareLinks(h.ctx, itCompany)
	if err != nil {
		t.Fatalf("ListShareLinks failed: %v", err)
	}
	t.Logf("share links: %d", len(links))

	resolved, err := h.store.ResolveShareLink(h.ctx, link.Token)
	if err != nil {
		t.Fatalf("ResolveShareLink failed: %v", err)
	}
	if resolved == nil {
		t.Fatal("ResolveShareLink returned nil for a fresh, unexpired token")
	}

	affected, err := h.store.RevokeShareLink(h.ctx, itCompany, id, 1, "it cleanup")
	if err != nil {
		t.Fatalf("RevokeShareLink failed: %v", err)
	}
	if affected != 1 {
		t.Fatalf("RevokeShareLink affected %d rows, want 1", affected)
	}
}
