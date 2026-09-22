package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"adatrack_gps/service-media/models"
)

// TestIngestRateLimitRejectsFlood covers the hardening added after the B5b audit:
// the HMAC ingest tier is rate limited per IP BEFORE the signature is verified
// (verifying it requires buffering the body).
func TestIngestRateLimitRejectsFlood(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	kv := newFakeKV()
	svc, _ := newTestServiceWithKV(t, store, kv, func(s *Settings) {
		s.IngestRateLimit = 2
	})

	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
	}, "clip.jpg", "image/jpeg", jpegBytes)
	signed := deviceIMEI + "\n" + models.EventTypeSOS + "\n" + string(jpegBytes)
	send := func() *httptest.ResponseRecorder {
		headers := ingestHeaders("DEV001", tenantSecret, []byte(signed))
		headers["Content-Type"] = contentType
		return doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	}

	for i := 1; i <= 2; i++ {
		if rec := send(); rec.Code != http.StatusCreated {
			t.Fatalf("request %d status = %d, want 201 (body=%s)", i, rec.Code, rec.Body.String())
		}
	}
	rec := send()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request status = %d, want 429 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != CodeRateLimited {
		t.Errorf("error_code = %q, want %s", code, CodeRateLimited)
	}
}

// TestIngestRateLimitFailsClosedWhenRedisDown documents the fail-closed contract:
// an unreachable limiter must NOT let ingest through.
func TestIngestRateLimitFailsClosedWhenRedisDown(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	kv := newFakeKV()
	kv.err = errInjected
	svc, _ := newTestServiceWithKV(t, store, kv)

	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
	}, "clip.jpg", "image/jpeg", jpegBytes)
	signed := deviceIMEI + "\n" + models.EventTypeSOS + "\n" + string(jpegBytes)
	headers := ingestHeaders("DEV001", tenantSecret, []byte(signed))
	headers["Content-Type"] = contentType

	rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the limiter is unavailable (body=%s)", rec.Code, rec.Body.String())
	}
	if len(store.rows("DEV001")) != 0 {
		t.Errorf("a fail-closed rejection must not create catalog rows")
	}
}

// TestIngestRateLimitCanBeDisabled covers the MEDIA_INGEST_RATE_LIMIT=0 knob
// (dev/test): the limiter must not interfere when it is switched off.
func TestIngestRateLimitCanBeDisabled(t *testing.T) {
	store := newFakeStore()
	seedTenant(store, "DEV001", deviceIMEI, 7)
	kv := newFakeKV()
	svc, _ := newTestServiceWithKV(t, store, kv, func(s *Settings) {
		s.IngestRateLimit = 0
	})

	body, contentType := multipartBody(t, map[string]string{
		"imei": deviceIMEI, "event_type": models.EventTypeSOS,
	}, "clip.jpg", "image/jpeg", jpegBytes)
	signed := deviceIMEI + "\n" + models.EventTypeSOS + "\n" + string(jpegBytes)
	for i := 0; i < 4; i++ {
		headers := ingestHeaders("DEV001", tenantSecret, []byte(signed))
		headers["Content-Type"] = contentType
		rec := doRequest(t, svc, http.MethodPost, "/api/v1/media/events", body, headers)
		if rec.Code != http.StatusCreated {
			t.Fatalf("request %d status = %d (want 201) when the limiter is off", i+1, rec.Code)
		}
	}
}
