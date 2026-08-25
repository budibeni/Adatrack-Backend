package controllers

import (
	"encoding/json"
	"fmt"
	"time"

	"ajb_gps/worker-alert/models"
)

// alertEvent is the NATS payload published on alert.<type>.<company>.
// Konvensi subject mengikuti roadmap (alert.geofence.*, alert.sos.*, dst.)
// dengan token terakhir = company_code agar konsumen dapat filter tenant,
// dan tetap cocok dengan definisi JetStream stream alert-<type> (single *).
type alertEvent struct {
	AlertID     uint64  `json:"alert_id"`
	VehicleID   uint64  `json:"vehicle_id"`
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code"`
	Type        string  `json:"type"` // geofence|speed|sos|battery|offline|route_deviation
	Severity    string  `json:"severity"`
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	Lat         float64 `json:"lat,omitempty"`
	Lon         float64 `json:"lon,omitempty"`
	Speed       float64 `json:"speed,omitempty"`
	Timestamp   int64   `json:"timestamp"` // unix epoch dari device
	PublishedAt int64   `json:"published_at"`

	// Route deviation specifics.
	RouteAssignmentID uint64  `json:"route_assignment_id,omitempty"`
	DistanceM         float64 `json:"distance_m,omitempty"`
}

// alertTypeSubject maps the DB enum to the NATS subject token.
func alertTypeSubject(t string) string {
	switch t {
	case models.AlertTypeGeofence:
		return "geofence"
	case models.AlertTypeSpeed:
		return "speed"
	case models.AlertTypeSOS:
		return "sos"
	case models.AlertTypeBattery:
		return "battery"
	case models.AlertTypeOffline:
		return "offline"
	case models.AlertTypeRouteDev:
		return "route_deviation"
	default:
		return lowerCode(t)
	}
}

// publishAlert emits an alert event on alert.<subject-type>.<company_code>
// (core NATS; JetStream streams alert-<type> capture it).
func (wa *WorkerAlert) publishAlert(company, imei string, rec models.AlertRecord, tm models.TelemetryMessage) {
	if wa.nac == nil || rec.ID == 0 {
		return
	}
	rec.Severity = normalizeSeverity(rec.Severity)
	ev := alertEvent{
		AlertID:     rec.ID,
		VehicleID:   rec.VehicleID,
		IMEI:        imei,
		CompanyCode: company,
		Type:        alertTypeSubject(rec.AlertType),
		Severity:    rec.Severity,
		Status:      rec.Status,
		Message:     rec.Description,
		Speed:       tm.Speed,
		Timestamp:   tm.Timestamp,
		PublishedAt: time.Now().Unix(),
	}
	if rec.VehicleLat != nil {
		ev.Lat = *rec.VehicleLat
	}
	if rec.VehicleLon != nil {
		ev.Lon = *rec.VehicleLon
	}

	subject := wa.cfg.SubjectPlain("alert", ev.Type, lowerCode(company))
	data, err := json.Marshal(ev)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"alert_id":%d,"error":"marshal"}`, rec.ID))
	}
	if err := wa.nac.Publish(subject, data); err != nil {
		wa.metrics.incError(company, "publish")
		return
	}
	wa.metrics.incPublished(subject)
}
