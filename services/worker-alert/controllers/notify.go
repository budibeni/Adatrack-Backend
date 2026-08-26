package controllers

import (
	"log/slog"

	"ajb_gps/worker-alert/models"
)

// severityRank maps severities onto the notification_preferences.min_severity
// scale (info < warning < critical). Alert severities low/medium/high/critical.
func severityRank(s string) int {
	switch s {
	case "low", "info":
		return 0
	case "medium", "warning":
		return 1
	case "high", "critical":
		return 2
	default:
		return 1
	}
}

// alertPrefTypes returns the preference alert_type tokens matching a DB enum
// type (notification_preferences memakai token lowercase seperti 'sos').
func alertPrefTypes(alertType string) []string {
	switch alertType {
	case models.AlertTypeGeofence:
		return []string{"geofence"}
	case models.AlertTypeSpeed:
		return []string{"speed"}
	case models.AlertTypeSOS:
		return []string{"sos"}
	case models.AlertTypeBattery:
		return []string{"battery"}
	case models.AlertTypeOffline:
		return []string{"offline"}
	case models.AlertTypeRouteDev:
		return []string{"route_deviation", "route"}
	case models.AlertTypeFuelDrop:
		return []string{"fuel_drop", "fuel"}
	case models.AlertTypeRefuel:
		return []string{"refuel", "fuel"}
	default:
		return []string{lowerCode(alertType)}
	}
}

// notifyAlert dispatches an alert to every eligible recipient following their
// notification_preferences (B3 notifikasi wajib, bisa diset per user/channel).
// Recipients = user dengan akses vehicle (user_vehicles) ∪ Admin company
// (master role), lalu difilter min_severity per channel.
func (wa *WorkerAlert) notifyAlert(s store, company string, tm models.TelemetryMessage, rec models.AlertRecord) {
	if rec.ID == 0 || rec.VehicleID == 0 {
		return
	}
	prefs, err := s.EnabledPreferences(alertPrefTypes(rec.AlertType))
	if err != nil {
		slog.Error("notification preferences lookup failed", "company", company,
			"alert_id", rec.ID, "error", err)
		wa.metrics.incError(company, "notify_prefs")
		return
	}
	if len(prefs) == 0 {
		return // tidak ada preferensi aktif untuk tipe alert ini
	}

	// Filter preferensi di bawah ambang severity user.
	eligible := make(map[uint64]models.NotifPreference, len(prefs))
	for _, p := range prefs {
		if severityRank(rec.Severity) >= severityRank(p.MinSeverity) {
			eligible[p.UserID] = p
		}
	}
	if len(eligible) == 0 {
		return
	}

	vehUsers, err := s.VehicleUserIDs(rec.VehicleID)
	if err != nil {
		slog.Error("vehicle users lookup failed", "company", company, "error", err)
		wa.metrics.incError(company, "notify_users")
	}
	allowed := make(map[uint64]bool, len(vehUsers)+2)
	for _, id := range vehUsers {
		allowed[id] = true
	}
	admins, err := wa.adminUserIDs(company)
	if err != nil {
		slog.Warn("admin users lookup failed", "company", company, "error", err)
	} else {
		for _, id := range admins {
			allowed[id] = true
		}
	}

	for userID, pref := range eligible {
		if !allowed[userID] {
			continue // user tidak berhak atas vehicle ini (row-level)
		}
		wa.dispatch(s, company, userID, pref, rec, tm)
	}
}

// adminUserIDs resolves master users with role Admin for the company via the
// master pool (auth authority — Admin melihat semua vehicle company-nya).
func (wa *WorkerAlert) adminUserIDs(companyCode string) ([]uint64, error) {
	master := wa.tm.Master()
	if master == nil {
		return nil, errMasterPool
	}
	rows, err := master.Query(
		`SELECT id FROM users
		 WHERE company_code = ? AND role = 'Admin' AND status = 'active'
		   AND deleted_at IS NULL`,
		companyCode,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uint64, 0, 4)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

var errMasterPool = slogNotWired{}

type slogNotWired struct{}

func (slogNotWired) Error() string { return "tenant master pool unavailable" }
