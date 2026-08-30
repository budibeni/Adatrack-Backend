package controllers

import (
	"testing"
	"time"

	"ajb_gps/service-media/models"
)

func TestHMACVerifyBackAndForth(t *testing.T) {
	secret := "dev001-hmac-secret-b5b"
	body := []byte(`{"vehicle_id":1,"media_type":"photo","trigger_type":"alarm","mime_type":"image/jpeg"}`)

	sig := hmacSHA256Hex(secret, body)
	if !verifySignatureHex(sig, secret, body) {
		t.Fatal("valid signature should verify")
	}
	if verifySignatureHex("deadbeef", secret, body) {
		t.Fatal("bogus signature must not verify")
	}
	if verifySignatureHex(sig, "wrong-secret", body) {
		t.Fatal("wrong secret must not verify")
	}
	if verifySignatureHex("", secret, body) {
		t.Fatal("empty signature must fail")
	}
	if verifySignatureHex(sig, "", body) {
		t.Fatal("empty secret must fail")
	}
}

func TestBuildObjectKey_KeyLayout(t *testing.T) {
	ts := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	key := buildObjectKey("dev001", "7", ts, "uuid-1")
	want := "DEV001/7/202608/uuid-1" // FR-8.2: {company}/{vehicle}/{yyyyMM}/{uuid}
	if key != want {
		t.Fatalf("buildObjectKey = %q, want %q", key, want)
	}
}

func TestParseDailyCron(t *testing.T) {
	h, m := parseDailyCron("0 3 * * *")
	if h != 3 || m != 0 {
		t.Fatalf("default cron parsed as h=%d m=%d", h, m)
	}
	h, m = parseDailyCron("30 1 * * *")
	if h != 1 || m != 30 {
		t.Fatalf("valid cron parsed as h=%d m=%d", h, m)
	}
	h, m = parseDailyCron("not-a-cron")
	if h != 3 || m != 0 {
		t.Fatalf("invalid cron should fall back to 3:00, got h=%d m=%d", h, m)
	}
}

func TestNextOClock(t *testing.T) {
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	// 03:00 today (future) → today 03:00.
	nxt := nextOClock(now, 3, 0)
	if nxt.Day() != 27 || nxt.Hour() != 3 {
		t.Fatalf("nextOClock future = %v", nxt)
	}
	// 01:00 (already passed) → tomorrow 01:00.
	nxt2 := nextOClock(now, 1, 0)
	if nxt2.Day() != 28 || nxt2.Hour() != 1 {
		t.Fatalf("nextOClock past = %v", nxt2)
	}
}

func TestParseTakenAt(t *testing.T) {
	before := time.Now().Unix()
	ts := parseTakenAt("")
	if ts.Before(time.Unix(before, 0)) {
		t.Fatal("empty taken_at should default to now")
	}
	parsed := parseTakenAt("2026-08-27T10:00:00Z")
	if parsed.Unix() != time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("parse taken_at = %v", parsed)
	}
}

func TestMediaEventToDTO_NilMeta(t *testing.T) {
	ev := models.MediaEvent{ID: 1, VehicleID: 2, TakenAt: time.Now()}
	dto := mediaEventToDTO(ev)
	if dto.ID != 1 || dto.VehicleID != 2 {
		t.Fatalf("dto mismatch: %+v", dto)
	}
	if dto.Meta != nil {
		t.Fatal("nil meta should be omitted")
	}
	if dto.TakenAt == "" {
		t.Fatal("taken_at should be formatted")
	}
}
