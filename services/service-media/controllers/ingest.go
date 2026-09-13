package controllers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Allowed media content types (FR-8.2 allowlist).
var allowedContentTypes = map[string]bool{
	"image/jpeg": true,
	"video/mp4":  true,
}

var allowedTriggerTypes = map[string]bool{
	"sos": true, "alarm": true, "geofence": true, "overspeed": true,
	"manual": true, "scheduled": true, "power": true,
}

var allowedMediaTypes = map[string]bool{"photo": true, "video_clip": true}

// mediaConfig is the resolved effective ingest config for a company.
type mediaConfig struct {
	companyCode string
	bucket      string
	maxFileMB   int
	hmacSecret  string
}

// resolveMediaConfig returns the effective per-company config: master
// company_media_config row wins; else env fallbacks (dev convenience).
func resolveMediaConfig(companyCode string) (*mediaConfig, error) {
	cfg, err := CompanyMediaConfig(companyCode)
	if err != nil {
		return nil, err
	}
	mc := &mediaConfig{companyCode: strings.ToUpper(strings.TrimSpace(companyCode))}
	if cfg != nil {
		mc.bucket = cfg.Bucket
		mc.maxFileMB = cfg.MaxFileMB
		mc.hmacSecret = cfg.HMACSecret
	} else {
		mc.bucket = envOr("MEDIA_S3_BUCKET", appCfg.Media.S3Bucket)
		mc.maxFileMB = envInt("MEDIA_MAX_FILE_MB", appCfg.Media.MaxFileMB)
		mc.hmacSecret = envOr("MEDIA_DEFAULT_HMAC_SECRET", appCfg.Media.DefaultHMACSecret)
	}
	if mc.bucket == "" {
		mc.bucket = "adatrack-media"
	}
	if mc.maxFileMB <= 0 {
		mc.maxFileMB = 100
	}
	return mc, nil
}

// ---------------------------------------------------------------------------
// POST /api/v1/media/events — HMAC-SHA256 per-company (FR-8.1).
// Two flows accepted:
//   A) multipart/form-data: file field 'file' + metadata fields (direct upload).
//   B) application/json:    metadata only → returns a presigned PUT URL so the
//                           device/gateway uploads the object itself.
// ---------------------------------------------------------------------------

func ingestMediaHandler(c *gin.Context) {
	cfg, err := resolveMediaConfig(c.GetHeader("X-Company-Code"))
	if err != nil {
		slog.Error("media ingest: resolve config failed", "error", err)
		mediaIngestErrorsTotal.WithLabelValues("?", "config").Inc()
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "media config unavailable")
		return
	}

	// Read raw body to verify HMAC over the EXACT uploaded bytes.
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "read").Inc()
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "unable to read request body")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))

	if !verifySignatureHex(c.GetHeader("X-Signature"), cfg.hmacSecret, raw) {
		slog.Warn("media ingest: HMAC mismatch", "company", cfg.companyCode)
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "hmac").Inc()
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid X-Signature (HMAC mismatch)")
		return
	}

	ct := c.ContentType()
	if strings.HasPrefix(ct, "multipart/form-data") {
		ingestMultipart(c, cfg)
		return
	}
	ingestJSON(c, cfg, raw)
}

// verifySignatureHex constant-time-compares the presented hex HMAC signature.
func verifySignatureHex(presented, secret string, body []byte) bool {
	p := strings.TrimSpace(presented)
	if p == "" || secret == "" {
		return false
	}
	want, err := hex.DecodeString(hmacSHA256Hex(secret, body))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(p)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

func hmacSHA256Hex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// buildObjectKey follows FR-8.2 layout {company}/{vehicle}/{yyyyMM}/{uuid}.
func buildObjectKey(company, vehicleID string, t time.Time, uuid string) string {
	return strings.ToUpper(company) + "/" + vehicleID + "/" +
		t.Format("200601") + "/" + uuid
}
