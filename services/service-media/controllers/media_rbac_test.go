package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"adatrack_gps/service-media/models"
)

// TestListAndDetailRowLevel covers the PRD 3.1 row-level filter: an Operator only
// sees the vehicles assigned via tm_user_vehicles (an empty grant = zero rows).
func TestListAndDetailRowLevel(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	seedUser(store, 1, "DEV001", "admin@dev001.io", RoleAdmin, nil)
	seedUser(store, 2, "DEV001", "driver@dev001.io", RoleDriver, []int64{7})
	svc, mem := newTestService(t, store)

	seedCompleteMedia(t, store, mem, "DEV001", 7, time.Now().UTC().Add(24*time.Hour))
	other := seedCompleteMedia(t, store, mem, "DEV001", 9, time.Now().UTC().Add(24*time.Hour))

	rec := doRequest(t, svc, http.MethodGet, "/api/v1/media", nil, bearer(t, svc, 2))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Data []models.MediaEvent `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].VehicleID != 7 {
		t.Errorf("driver list = %+v, want only vehicle 7", listed.Data)
	}

	detailOther := "/api/v1/media/" + strconv.FormatInt(other.ID, 10)
	rec = doRequest(t, svc, http.MethodGet, detailOther, nil, bearer(t, svc, 2))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("detail status = %d, want 403 for an unassigned vehicle", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != CodeUnauthorizedVehicle {
		t.Errorf("error_code = %q, want %s", code, CodeUnauthorizedVehicle)
	}

	rec = doRequest(t, svc, http.MethodGet, detailOther, nil, bearer(t, svc, 1))
	if rec.Code != http.StatusOK {
		t.Errorf("admin detail status = %d, want 200", rec.Code)
	}

	// No token at all → 401.
	if rec := doRequest(t, svc, http.MethodGet, "/api/v1/media", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list status = %d, want 401", rec.Code)
	}
}

// TestMediaURLIsAuditedAndFailClosed covers FR-8.4: the presigned URL is only
// handed out together with a MEDIA_URL_ACCESS audit row (fail-closed on error).
func TestMediaURLIsAuditedAndFailClosed(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	seedUser(store, 1, "DEV001", "admin@dev001.io", RoleAdmin, nil)
	svc, mem := newTestService(t, store)

	row := seedCompleteMedia(t, store, mem, "DEV001", 7, time.Now().UTC().Add(24*time.Hour))
	path := "/api/v1/media/" + strconv.FormatInt(row.ID, 10) + "/url"

	rec := doRequest(t, svc, http.MethodGet, path, nil, bearer(t, svc, 1))
	if rec.Code != http.StatusOK {
		t.Fatalf("url status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var res struct {
		Data models.URLResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode url: %v", err)
	}
	if res.Data.URL == "" || res.Data.ExpiresIn <= 0 {
		t.Errorf("url response = %+v, want a presigned URL + TTL", res.Data)
	}
	found := false
	for _, a := range store.auditRows() {
		if a.Action == ActionMediaURL {
			found = true
		}
	}
	if !found {
		t.Errorf("no MEDIA_URL_ACCESS audit row was written")
	}

	// Now make the audit sink fail: the URL must NOT be served (fail-closed).
	store.mu.Lock()
	store.auditErr = errors.New("audit sink down")
	store.mu.Unlock()
	rec = doRequest(t, svc, http.MethodGet, path, nil, bearer(t, svc, 1))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the audit trail is unavailable", rec.Code)
	}
}

// TestDeleteAndRestoreAreAdminOnly covers FR-8.9 + 6.0.1.
func TestDeleteAndRestoreAreAdminOnly(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	seedUser(store, 1, "DEV001", "admin@dev001.io", RoleAdmin, nil)
	seedUser(store, 2, "DEV001", "manager@dev001.io", RoleManager, nil)
	svc, mem := newTestService(t, store)

	row := seedCompleteMedia(t, store, mem, "DEV001", 7, time.Now().UTC().Add(24*time.Hour))
	path := "/api/v1/media/" + strconv.FormatInt(row.ID, 10)

	if rec := doRequest(t, svc, http.MethodDelete, path, nil, bearer(t, svc, 2)); rec.Code != http.StatusForbidden {
		t.Fatalf("manager delete status = %d, want 403 (Admin only)", rec.Code)
	}

	rec := doRequest(t, svc, http.MethodDelete, path, []byte(`{"reason":"requested by customer"}`),
		mergeHeaders(bearer(t, svc, 1), "Content-Type", "application/json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin delete status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var deleted struct {
		Data models.MediaEvent `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if deleted.Data.Status != models.StatusDeleted {
		t.Errorf("status = %q, want deleted (soft delete)", deleted.Data.Status)
	}
	// The physical object must survive a soft delete (only retention deletes it).
	if _, err := mem.Head(context.Background(), row.ObjectKey); err != nil {
		t.Errorf("object must still exist after a soft delete: %v", err)
	}
	// The row disappears from the default list.
	recList := doRequest(t, svc, http.MethodGet, "/api/v1/media", nil, bearer(t, svc, 1))
	var listed struct {
		Data []models.MediaEvent `json:"data"`
	}
	if err := json.Unmarshal(recList.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, m := range listed.Data {
		if m.ID == row.ID {
			t.Errorf("a soft-deleted row must not appear in the default list")
		}
	}

	restorePath := path + "/restore"
	rec = doRequest(t, svc, http.MethodPost, restorePath, []byte(`{}`),
		mergeHeaders(bearer(t, svc, 1), "Content-Type", "application/json"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("restore without a reason status = %d, want 400", rec.Code)
	}
	rec = doRequest(t, svc, http.MethodPost, restorePath, []byte(`{"reason":"false alarm"}`),
		mergeHeaders(bearer(t, svc, 1), "Content-Type", "application/json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	restored := false
	for _, a := range store.auditRows() {
		if a.Action == ActionMediaRestored {
			restored = true
		}
	}
	if !restored {
		t.Errorf("no ENTITY_RESTORED audit row was written")
	}
}

// TestRetentionSweepDeletesObjectsAndExpiresRows covers FR-8.7.
func TestRetentionSweepDeletesObjectsAndExpiresRows(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, mem := newTestService(t, store)

	row := seedCompleteMedia(t, store, mem, "DEV001", 7, time.Now().UTC().Add(-time.Hour))
	fresh := seedCompleteMedia(t, store, mem, "DEV001", 9, time.Now().UTC().Add(24*time.Hour))

	svc.sweepRetention(context.Background())

	if _, err := mem.Head(context.Background(), row.ObjectKey); err == nil {
		t.Errorf("expired object %s must be deleted from storage", row.ObjectKey)
	}
	if _, err := mem.Head(context.Background(), fresh.ObjectKey); err != nil {
		t.Errorf("a non-expired object must survive the sweep: %v", err)
	}
	for _, m := range store.rows("DEV001") {
		if m.ID == row.ID && m.Status != models.StatusExpired {
			t.Errorf("row status = %q, want expired", m.Status)
		}
	}
	hardDeleted := false
	for _, a := range store.auditRows() {
		if a.Action == ActionHardDelete {
			hardDeleted = true
		}
	}
	if !hardDeleted {
		t.Errorf("no HARD_DELETE audit row was written for the retention deletion")
	}
}

// mergeHeaders adds one header to a header map.
func mergeHeaders(base map[string]string, key, value string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	base[key] = value
	return base
}
