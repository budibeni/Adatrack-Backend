package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"adatrack_gps/internal/storage"
	"adatrack_gps/service-media/models"
)

// seedCompleteMedia inserts a `complete` catalog row + its object.
func seedCompleteMedia(t *testing.T, store *fakeStore, mem *storage.Mem, company string, vehicleID int64, expires time.Time) models.MediaEvent {
	t.Helper()
	ctx := context.Background()
	sum := "a" + strconv.FormatInt(time.Now().UnixNano(), 16)
	key := "dev001/" + strconv.FormatInt(vehicleID, 10) + "/202609/" + sum + ".jpg"
	for len(sum) < 64 {
		sum += "0"
	}
	if _, err := mem.Put(ctx, key, jpegBytes, "image/jpeg"); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	now := time.Now().UTC()
	retention := 30
	row := &models.MediaEvent{
		VehicleID: vehicleID, IMEI: deviceIMEI, EventType: models.EventTypeSOS,
		ObjectKey: key, FileSize: int64(len(jpegBytes)), MimeType: "image/jpeg",
		ContentSHA256: sum[:64], Status: models.StatusComplete, HMACVerified: true,
		UploadSource: models.SourceMultipart, RetentionDays: retention,
		CapturedAt: now, CompletedAt: &now, ExpiresAt: &expires,
	}
	id, err := store.CreateMediaEvent(ctx, company, row)
	if err != nil {
		t.Fatalf("seed row: %v", err)
	}
	row.ID = id
	return *row
}

// TestJSONIngestTicketAndComplete is the FR-8.1 presigned-PUT flow.
func TestJSONIngestTicketAndComplete(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, mem := newTestService(t, store)

	body, _ := json.Marshal(map[string]any{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
		"mime_type": "image/jpeg", "file_size": len(jpegBytes),
	})
	headers := ingestHeaders("DEV001", tenantSecret, body)
	headers["Content-Type"] = "application/json"

	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ticket status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var ticket struct {
		Data models.UploadTicket `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	if ticket.Data.Status != models.StatusPending || ticket.Data.UploadURL == "" || ticket.Data.ID == 0 {
		t.Fatalf("ticket = %+v, want a pending row with a presigned upload URL", ticket.Data)
	}

	// The agent uploads to the bucket (the URL is resolved by the client; the
	// in-memory store backs the object in tests).
	if _, err := mem.Put(ctxBG(), ticket.Data.ObjectKey, jpegBytes, "image/jpeg"); err != nil {
		t.Fatalf("upload: %v", err)
	}

	completePath := "/api/v1/media/events/" + strconv.FormatInt(ticket.Data.ID, 10) + "/complete"
	hello := ingestHeaders("DEV001", tenantSecret, nil)
	hello["Content-Type"] = "application/json"
	rec = doRequest(t, svc, http.MethodPost, completePath, nil, hello)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var done struct {
		Data models.MediaEvent `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &done); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if done.Data.Status != models.StatusComplete || done.Data.ExpiresAt == nil || done.Data.ObjectETag == "" {
		t.Errorf("completed row = %+v, want status complete + expires_at + etag", done.Data)
	}

	// A second completion must be rejected (lifecycle guard).
	rec = doRequest(t, svc, http.MethodPost, completePath, nil, hello)
	if rec.Code != http.StatusConflict {
		t.Errorf("second complete status = %d, want 409", rec.Code)
	}
}

// TestCompleteRejectsMissingObject covers the 404 contract.
func TestCompleteRejectsMissingObject(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, _ := newTestService(t, store)

	body, _ := json.Marshal(map[string]any{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
		"mime_type": "image/jpeg", "file_size": len(jpegBytes),
	})
	headers := ingestHeaders("DEV001", tenantSecret, body)
	headers["Content-Type"] = "application/json"
	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	var ticket struct {
		Data models.UploadTicket `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}

	hello := ingestHeaders("DEV001", tenantSecret, nil)
	hello["Content-Type"] = "application/json"
	path := "/api/v1/media/events/" + strconv.FormatInt(ticket.Data.ID, 10) + "/complete"
	rec = doRequest(t, svc, http.MethodPost, path, nil, hello)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when the object was never uploaded", rec.Code)
	}
}
