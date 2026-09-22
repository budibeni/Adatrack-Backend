package models

// JSONUploadRequest is the `POST /api/v1/media/events` JSON body (FR-8.1
// "JSON + presigned PUT flow"). The signature covers the RAW body bytes.
type JSONUploadRequest struct {
	IMEI          string `json:"imei" binding:"required,len=15,numeric"`
	VehicleID     int64  `json:"vehicle_id,omitempty" binding:"omitempty,gt=0"`
	EventType     string `json:"event_type" binding:"required,oneof=sos alarm geofence overspeed manual scheduled power"`
	MimeType      string `json:"mime_type" binding:"required,max=100"`
	FileSize      int64  `json:"file_size" binding:"required,gt=0"`
	ContentSHA256 string `json:"content_sha256,omitempty" binding:"omitempty,len=64"`
	CapturedAt    string `json:"captured_at,omitempty" binding:"omitempty,max=40"`
}

// CompleteRequest finalises the JSON flow (`POST /media/events/:id/complete`). The
// declared size/hash are cross-checked against the stored object when present.
type CompleteRequest struct {
	FileSize      int64  `json:"file_size,omitempty" binding:"omitempty,gt=0"`
	ContentSHA256 string `json:"content_sha256,omitempty" binding:"omitempty,len=64"`
}

// UploadTicket is the 201 answer of the JSON ingest: where to PUT the object.
type UploadTicket struct {
	ID              int64  `json:"id"`
	ObjectKey       string `json:"object_key"`
	UploadURL       string `json:"upload_url"`
	UploadExpiresIn int    `json:"upload_expires_in"`
	Status          string `json:"status"`
	CompleteURL     string `json:"complete_url"`
}

// URLResponse is the presigned GET answer (FR-8.4).
type URLResponse struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
}

// DeleteRequest is the soft-delete body (reason is optional but audited).
type DeleteRequest struct {
	Reason string `json:"reason,omitempty" binding:"omitempty,max=255"`
}

// RestoreRequest is the restore body — §6.0.1 requires an explicit reason.
type RestoreRequest struct {
	Reason string `json:"reason" binding:"required,min=3,max=255"`
}
