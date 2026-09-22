// Package models holds the request/response shapes and the object-catalog entity
// of service-media (PRD Module 8 / Scope A, FR-8.1..FR-8.9).
package models

import "time"

// Media event types accepted at ingest (PRD Module 8 scope:
// sos/alarm/geofence/overspeed/manual/scheduled/power). The list mirrors the
// `th_media_events.event_type` CHECK constraint — a whitelist, never a blacklist
// (PRD §8.5 rule 2).
const (
	EventTypeSOS       = "sos"
	EventTypeAlarm     = "alarm"
	EventTypeGeofence  = "geofence"
	EventTypeOverspeed = "overspeed"
	EventTypeManual    = "manual"
	EventTypeScheduled = "scheduled"
	EventTypePower     = "power"
)

// EventTypes is the canonical ordered allowlist.
var EventTypes = []string{
	EventTypeSOS, EventTypeAlarm, EventTypeGeofence, EventTypeOverspeed,
	EventTypeManual, EventTypeScheduled, EventTypePower,
}

// ValidEventType reports whether an event type is in the allowlist.
func ValidEventType(raw string) bool {
	for _, t := range EventTypes {
		if t == raw {
			return true
		}
	}
	return false
}

// Content-type allowlist (FR-8.2) with the extension used in the object key.
var allowedMime = map[string]string{
	"image/jpeg": "jpg",
	"video/mp4":  "mp4",
}

// MimeAllowed reports whether a content type may be stored; the second return is
// the file extension used to build the object key.
func MimeAllowed(raw string) (string, bool) {
	ext, ok := allowedMime[normaliseMime(raw)]
	return ext, ok
}

// normaliseMime lowercases and strips parameters (`image/jpeg; charset=...`).
func normaliseMime(raw string) string {
	out := ""
	for _, r := range raw {
		if r == ';' || r == ' ' {
			break
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out += string(r)
	}
	return out
}

// Upload source values (th_media_events.upload_source, migration 016).
const (
	SourceMultipart = "multipart"
	SourceJSON      = "json"
)

// Media lifecycle statuses (FR-8.3 + §6.0.1 soft delete).
const (
	StatusPending  = "pending"
	StatusComplete = "complete"
	StatusExpired  = "expired"
	StatusDeleted  = "deleted"
)

// MediaEvent is one `th_media_events` catalog row.
type MediaEvent struct {
	ID              int64      `json:"id"`
	VehicleID       int64      `json:"vehicle_id"`
	IMEI            string     `json:"imei"`
	EventType       string     `json:"event_type"`
	ObjectKey       string     `json:"object_key"`
	FileSize        int64      `json:"file_size"`
	MimeType        string     `json:"mime_type"`
	ContentSHA256   string     `json:"content_sha256"`
	Status          string     `json:"status"`
	HMACVerified    bool       `json:"hmac_verified"`
	UploadSource    string     `json:"upload_source"`
	ObjectETag      string     `json:"object_etag,omitempty"`
	RetentionDays   int        `json:"retention_days,omitempty"`
	CreatedByUserID *int64     `json:"created_by_user_id,omitempty"`
	CapturedAt      time.Time  `json:"captured_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	NotifiedAt      *time.Time `json:"notified_at,omitempty"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	DeleteReason    string     `json:"delete_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// MediaEventData is the `MEDIA_EVENT` WebSocket payload (PRD §8.3) published on
// `media.event.<company_code>` and fanned out by service-websocket. The presigned
// URL is short-lived so the dashboard can render immediately.
type MediaEventData struct {
	ID          int64  `json:"id"`
	CompanyCode string `json:"company_code"`
	VehicleID   int64  `json:"vehicle_id"`
	IMEI        string `json:"imei"`
	EventType   string `json:"event_type"`
	ObjectKey   string `json:"object_key"`
	MimeType    string `json:"mime_type"`
	FileSize    int64  `json:"file_size"`
	Status      string `json:"status"`
	URL         string `json:"url,omitempty"`
	CapturedAt  string `json:"captured_at"`
}

// Envelope is the generic PRD §8.1 success response.
type Envelope struct {
	Status     string      `json:"status"`
	Data       any         `json:"data,omitempty"`
	Pagination *Pagination `json:"pagination,omitempty"`
}

// ErrorEnvelope is the generic PRD §8.1 error response.
type ErrorEnvelope struct {
	Status    string            `json:"status"`
	ErrorCode string            `json:"error_code"`
	Message   string            `json:"message"`
	Timestamp string            `json:"timestamp"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// Pagination is the optional PRD §8.1 pagination block.
type Pagination struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}
