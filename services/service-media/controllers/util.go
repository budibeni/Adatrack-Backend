package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"ajb_gps/service-media/models"
)

// envOr returns env value or default (blank → default).
func envOr(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

// envInt returns env int or default.
func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

// parseTakenAt parses an optional RFC3339 timestamp, defaulting to now.
func parseTakenAt(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}

// newUUID returns a random 128-bit hex identifier for object keys (FR-8.2).
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))
	}
	return hex.EncodeToString(b)
}

// publishMediaEvent emits a media.event.<company_code> NATS message (FR-8.5)
// that service-websocket fans out as a MEDIA_EVENT WS event.
func publishMediaEvent(company string, mediaID, vehicleID uint64, imei, mediaType, triggerType, status string, size int64, takenAt time.Time) {
	if appNATS == nil {
		return
	}
	ev := models.MediaEventsEvent{
		Event:       "MEDIA_EVENT",
		MediaID:     mediaID,
		VehicleID:   vehicleID,
		IMEI:        imei,
		CompanyCode: strings.ToUpper(company),
		MediaType:   mediaType,
		TriggerType: triggerType,
		Status:      status,
		SizeBytes:   size,
		TakenAt:     takenAt.Unix(),
		PublishedAt: time.Now().Unix(),
	}
	data, err := json.Marshal(ev)
	if err != nil {
		slog.Error("media: marshal event failed", "error", err, "media_id", mediaID)
		return
	}
	subject := appNATS.SubjectPlain("media", "event", strings.ToLower(company))
	if err := appNATS.Publish(subject, data); err != nil {
		slog.Error("media: publish event failed", "error", err, "subject", subject, "media_id", mediaID)
	}
}
