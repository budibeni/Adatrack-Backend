// Package models holds service-media data types (DTOs + org pipeline).
package models

import (
	"encoding/json"
	"time"
)

// OkResponse is the standard success envelope { status, data, pagination? }.
type OkResponse struct {
	Status     string          `json:"status"`
	Data       interface{}     `json:"data"`
	Pagination *PaginationInfo `json:"pagination,omitempty"`
}

// PaginationInfo describes page/limit/total for list endpoints.
type PaginationInfo struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// ApiErrorResponse is the standard error envelope (GAP #3).
type ApiErrorResponse struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// AuthUser is the authenticated caller stored in the gin context (identical to
// api-vehicle/service-websocket for JWT interop).
type AuthUser struct {
	ID            uint64
	CompanyCode   string
	CompanyID     int64
	Email         string
	Role          string
	CompanyUserID uint64
}

// MediaConfig is one row of master company_media_config (migration 013).
type MediaConfig struct {
	CompanyCode   string
	Bucket        string
	RetentionDays int
	MaxFileMB     int
	HMACSecret    string
}

// MediaEvent is one row of company media_events (migration 015).
type MediaEvent struct {
	ID          uint64
	VehicleID   uint64
	IMEI        string
	CompanyCode string
	MediaType   string // photo | video_clip
	TriggerType string // sos|alarm|geofence|overspeed|manual|scheduled|power
	ObjectKey   string
	Bucket      string
	SizeBytes   int64
	DurationSec *int
	MimeType    string
	SHA256      string
	Status      string // uploaded|available|expired|failed
	TakenAt     time.Time
	Meta        []byte // raw JSON (nullable)
	CreatedAt   time.Time
	DeletedAt   *time.Time
}

// MediaEventDTO is the API-facing media_events representation.
type MediaEventDTO struct {
	ID          uint64          `json:"id"`
	VehicleID   uint64          `json:"vehicle_id"`
	IMEI        string          `json:"imei"`
	CompanyCode string          `json:"company_code"`
	MediaType   string          `json:"media_type"`
	TriggerType string          `json:"trigger_type"`
	ObjectKey   string          `json:"object_key"`
	Bucket      string          `json:"bucket"`
	SizeBytes   int64           `json:"size_bytes"`
	DurationSec *int            `json:"duration_seconds,omitempty"`
	MimeType    string          `json:"mime_type"`
	SHA256      string          `json:"sha256,omitempty"`
	Status      string          `json:"status"`
	TakenAt     string          `json:"taken_at"`
	Meta        json.RawMessage `json:"meta,omitempty"`
}

// IngestionEvent is the JSON body of POST /api/v1/media/events (JSON flow),
// where the caller first obtains a presigned PUT URL then uploads the object.
type IngestionEvent struct {
	VehicleID   uint64          `json:"vehicle_id"`
	IMEI        string          `json:"imei,omitempty"`
	MediaType   string          `json:"media_type"`   // photo | video_clip
	TriggerType string          `json:"trigger_type"` // sos|alarm|geofence|overspeed|manual|scheduled|power
	DurationSec *int            `json:"duration_seconds,omitempty"`
	MimeType    string          `json:"mime_type"` // image/jpeg | video/mp4
	SHA256      string          `json:"sha256,omitempty"`
	TakenAt     *time.Time      `json:"taken_at"` // RFC3339
	Meta        json.RawMessage `json:"meta,omitempty"`
}

// MediaEventsEvent is the NATS media.event.<company> payload (FR-8.5), mirrored
// by service-websocket into a MEDIA_EVENT WS event for authorised clients.
type MediaEventsEvent struct {
	Event       string `json:"event"`        // "MEDIA_EVENT"
	MediaID     uint64 `json:"media_id"`
	VehicleID   uint64 `json:"vehicle_id"`
	IMEI        string `json:"imei"`
	CompanyCode string `json:"company_code"`
	MediaType   string `json:"media_type"`
	TriggerType string `json:"trigger_type"`
	Status      string `json:"status"`
	SizeBytes   int64  `json:"size_bytes"`
	TakenAt     int64  `json:"taken_at"`
	PublishedAt int64  `json:"published_at"`
}
