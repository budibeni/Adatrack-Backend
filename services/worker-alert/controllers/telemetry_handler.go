package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"
)

// handleTelemetry routes one telemetry message through all B3 checks:
// tenant resolution → vehicle lookup → geofence/speed/battery/SOS/route.
func (wa *WorkerAlert) handleTelemetry(msg *natsMsg) error {
	var tm models.TelemetryMessage
	if err := json.Unmarshal(msg.Data, &tm); err != nil {
		wa.publishError(msg.Subject, msg.Data)
		slog.Error("invalid telemetry JSON", "subject", msg.Subject, "error", err)
		return nil
	}
	if tm.IMEI == "" {
		return nil
	}

	company := tm.CompanyCode
	if company == "" {
		// Defensive: payload tanpa company_code → resolve via master map
		// (anti-spoofing tetap terjaga; unknown IMEI di-drop dengan log).
		code, err := wa.tm.ResolveCompanyByIMEI(context.Background(), tm.IMEI)
		if err != nil {
			if errors.Is(err, tenant.ErrIMEINotRegistered) {
				slog.Warn("telemetry for unregistered IMEI dropped", "imei", tm.IMEI)
				return nil
			}
			slog.Error("tenant resolution failed", "imei", tm.IMEI, "error", err)
			wa.metrics.incError(company, "tenant")
			return nil
		}
		company = code
		tm.CompanyCode = code
	}

	st, err := wa.newStore(company)
	if err != nil {
		if errors.Is(err, tenant.ErrCompanyNotFound) {
			slog.Warn("company pool unavailable for telemetry", "company", company, "imei", tm.IMEI)
		} else {
			slog.Error("store init failed", "company", company, "error", err)
		}
		wa.metrics.incError(company, "store")
		return nil
	}

	vehicleID, vstatus, err := st.VehicleByIMEI(tm.IMEI)
	if err != nil {
		if errors.Is(err, ErrUnknownVehicle) {
			slog.Warn("telemetry IMEI has no vehicle row in company db",
				"company", company, "imei", tm.IMEI)
			return nil
		}
		slog.Error("vehicle lookup failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "vehicle_lookup")
		return nil
	}
	if vstatus != "active" {
		return nil // kendaraan inactive/maintenance tidak memicu alert
	}
	if tm.VehicleID > 0 && int64(vehicleID) != tm.VehicleID {
		// Payload & DB tidak sinkron — percayai DB perusahaan (source of truth).
		tm.VehicleID = int64(vehicleID)
	}

	// SOS diproses eksklusif (event darurat; bukan kandidat check lain).
	if isSOS(tm) {
		wa.handleSOS(st, company, tm, vehicleID)
		return nil
	}

	wa.checkGeofence(st, company, tm, vehicleID)
	wa.checkSpeeding(st, company, tm, vehicleID)
	wa.checkBattery(st, company, tm, vehicleID)
	wa.checkRoute(st, company, tm, vehicleID)
	return nil
}

// publishError forwards malformed payloads to telemetry.error.<IMEI> (no
// silent drop), bila subject cukup informatif untuk menurunkan IMEI.
func (wa *WorkerAlert) publishError(subject string, payload []byte) {
	if wa.nac == nil || subject == "" {
		return
	}
	parts := splitSubject(subject)
	imei := parts[len(parts)-1]
	errSubject := wa.cfg.Subject("error", imei)
	if err := wa.nac.Publish(errSubject, payload); err != nil {
		slog.Error("failed to publish parse error", "subject", errSubject, "error", err)
	}
}

func splitSubject(s string) []string {
	out := make([]string, 0, 4)
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(s[i])
	}
	out = append(out, cur)
	return out
}
