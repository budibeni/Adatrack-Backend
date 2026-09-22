package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"adatrack_gps/service-media/models"
)

// deviceIMEI is the registered device used across the ingest tests.
const deviceIMEI = "864201040512345"

// tenantSecret is the per-company HMAC secret seeded in the fake store.
const tenantSecret = "tenant-hmac-secret-123456"

// jpegBytes is a minimal (but byte-exact) JPEG payload for the round trips.
var jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01, 0x0D, 0x0A}

func ctxBG() context.Context { return context.Background() }

// doRequest executes one request against the service engine.
func doRequest(t *testing.T, svc *Service, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	return rec
}

// multipartBody builds a multipart/form-data body + its content type.
func multipartBody(t *testing.T, fields map[string]string, filename, contentType string, data []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			t.Fatalf("write field %s: %v", name, err)
		}
	}
	if filename != "" {
		hdr := make(map[string][]string)
		hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
		hdr["Content-Type"] = []string{contentType}
		part, err := w.CreatePart(hdr)
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// ingestHeaders builds the FR-8.1 handshake headers.
func ingestHeaders(company, secret string, payload []byte) map[string]string {
	return map[string]string{
		"X-Company-Code": company,
		"X-Signature":    SignHMAC(secret, payload),
		"X-Timestamp":    time.Now().UTC().Format(time.RFC3339),
	}
}

// authToken mints an access token accepted by the shared verifier.
func authToken(t *testing.T, svc *Service, userID int64) string {
	t.Helper()
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    svc.settings.JWTIssuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			ID:        "test-" + strconv.FormatInt(userID, 10),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(svc.settings.JWTSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// bearer returns the Authorization header for a user.
func bearer(t *testing.T, svc *Service, userID int64) map[string]string {
	return map[string]string{"Authorization": "Bearer " + authToken(t, svc, userID)}
}

// errorCode decodes the PRD §8.1 error envelope code.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env models.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope %s: %v", trimForLog(body), err)
	}
	return env.ErrorCode
}

// trimForLog shortens a payload for assertion output.
func trimForLog(body []byte) string {
	if len(body) > 200 {
		return string(body[:200]) + "..."
	}
	return string(body)
}

// TestMultipartIngestStoresObjectAndCatalog is the FR-8.1/FR-8.2/FR-8.3 happy
// path: HMAC → object stored → catalog row `complete` → audit row.
func TestMultipartIngestStoresObjectAndCatalog(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, mem := newTestService(t, store)

	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
	}, "clip.jpg", "image/jpeg", jpegBytes)
	signed := deviceIMEI + "\n" + models.EventTypeSOS + "\n" + string(jpegBytes)
	headers := ingestHeaders("DEV001", tenantSecret, []byte(signed))
	headers["Content-Type"] = contentType

	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data models.MediaEvent `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	row := env.Data
	if row.Status != models.StatusComplete || row.UploadSource != models.SourceMultipart {
		t.Errorf("row = %+v, want a complete multipart row", row)
	}
	if row.VehicleID != 7 || row.IMEI != deviceIMEI || row.EventType != models.EventTypeSOS {
		t.Errorf("row identity = %+v, want vehicle 7 / %s / sos", row, deviceIMEI)
	}
	if !strings.HasPrefix(row.ObjectKey, "dev001/7/") {
		t.Errorf("object key = %q, want the {company}/{vehicle}/{yyyyMM}/{uuid} layout", row.ObjectKey)
	}
	if row.FileSize != int64(len(jpegBytes)) {
		t.Errorf("file_size = %d, want %d", row.FileSize, len(jpegBytes))
	}
	stored, err := mem.Get(ctxBG(), row.ObjectKey)
	if err != nil {
		t.Fatalf("object not stored: %v", err)
	}
	if !bytes.Equal(stored, jpegBytes) {
		t.Errorf("stored bytes differ from the upload")
	}
	audit := store.auditRows()
	if len(audit) == 0 || audit[0].Action != ActionMediaUploaded || audit[0].CompanyCode != "DEV001" {
		t.Errorf("audit rows = %s, want an ENTITY_CREATED row for DEV001", describe(audit))
	}
}

// TestIngestRejectsBadSignature covers the FR-8.1 negative path (401).
func TestIngestRejectsBadSignature(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, _ := newTestService(t, store)

	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
	}, "clip.jpg", "image/jpeg", jpegBytes)
	headers := map[string]string{
		"Content-Type":   contentType,
		"X-Company-Code": "DEV001",
		"X-Signature":    SignHMAC("wrong-secret", []byte("nope")),
		"X-Timestamp":    time.Now().UTC().Format(time.RFC3339),
	}
	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != CodeMediaSignatureInvalid {
		t.Errorf("error_code = %q, want %s", code, CodeMediaSignatureInvalid)
	}
	if len(store.rows("DEV001")) != 0 {
		t.Errorf("a rejected ingest must not create catalog rows")
	}
}

// TestIngestRejectsStaleTimestamp covers the anti-replay window (PRD 9.6).
func TestIngestRejectsStaleTimestamp(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, _ := newTestService(t, store)

	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
	}, "clip.jpg", "image/jpeg", jpegBytes)
	signed := deviceIMEI + "\n" + models.EventTypeSOS + "\n" + string(jpegBytes)
	headers := ingestHeaders("DEV001", tenantSecret, []byte(signed))
	headers["Content-Type"] = contentType
	headers["X-Timestamp"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)

	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a stale timestamp", rec.Code)
	}
}

// TestIngestRejectsDisallowedContentType covers the FR-8.2 mime allowlist.
func TestIngestRejectsDisallowedContentType(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, _ := newTestService(t, store)

	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeManual,
	}, "notes.txt", "text/plain", []byte("hello"))
	signed := deviceIMEI + "\n" + models.EventTypeManual + "\nhello"
	headers := ingestHeaders("DEV001", tenantSecret, []byte(signed))
	headers["Content-Type"] = contentType

	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != CodeMediaTypeNotAllowed {
		t.Errorf("error_code = %q, want %s", code, CodeMediaTypeNotAllowed)
	}
}

// TestIngestRejectsOversize covers the per-company max_file_mb ceiling.
func TestIngestRejectsOversize(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	svc, _ := newTestService(t, store, func(s *Settings) {
		s.MaxFileMB = 1
		s.MaxBodyBytes = 8 << 20
	})

	big := bytes.Repeat([]byte{0xAB}, (1<<20)+1024)
	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeOverspeed,
	}, "big.mp4", "video/mp4", big)
	signed := deviceIMEI + "\n" + models.EventTypeOverspeed + "\n" + string(big)
	headers := ingestHeaders("DEV001", tenantSecret, []byte(signed))
	headers["Content-Type"] = contentType

	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an oversize upload (body=%s)", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != CodeMediaTooLarge {
		t.Errorf("error_code = %q, want %s", code, CodeMediaTooLarge)
	}
	if len(store.rows("DEV001")) != 0 {
		t.Errorf("an oversize upload must not create catalog rows")
	}
}
