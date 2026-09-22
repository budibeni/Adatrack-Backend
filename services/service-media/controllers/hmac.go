package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// VerifyHMAC validates the FR-8.1 `X-Signature` header: hex-encoded
// HMAC-SHA256 of the signed payload (the file bytes for multipart, the raw JSON
// body for the presigned flow) using the company secret. Comparison is
// constant-time.
func VerifyHMAC(secret string, payload []byte, signature string) bool {
	raw := strings.TrimSpace(signature)
	if secret == "" || raw == "" {
		return false
	}
	// Tolerate a `sha256=<hex>` prefix (documented GitHub-style form).
	if trimmed, ok := strings.CutPrefix(raw, "sha256="); ok {
		raw = strings.TrimSpace(trimmed)
	}
	provided, err := hex.DecodeString(raw)
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hmac.Equal(mac.Sum(nil), provided)
}

// SignHMAC builds the same signature (used by the E2E harness and clients).
func SignHMAC(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// TimestampFresh validates the mandatory `X-Timestamp` header (RFC3339): a
// request older than the skew window is rejected, so a captured signature cannot
// be replayed (§9.6).
func TimestampFresh(raw string, skew time.Duration, now time.Time) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return false
	}
	if skew <= 0 {
		skew = 5 * time.Minute
	}
	delta := now.UTC().Sub(ts.UTC())
	if delta < 0 {
		delta = -delta
	}
	return delta <= skew
}
