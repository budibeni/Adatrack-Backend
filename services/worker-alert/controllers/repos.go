package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ajb_gps/internal/dialect"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"
)

// companyStore implements store against ONE pre-warmed company pool
// (adatrack_gps_{company_code}). All queries are schema-conform with the
// multi-tenant migrations 001–012.
//
// B4 HA read/write split: `db` adalah pool PRIMARY (semua WRITE + guard-read
// dedup yang konsistensi-kritis); `ro` adalah ReadRouter untuk query baca —
// diarahkan ke READ REPLICA ketika tersedia & sehat dengan fallback otomatis
// ke primary (lihat internal/tenant replica.go).
type companyStore struct {
	code string
	db   *sql.DB
	ro   *tenant.ReadRouter
}

// ErrUnknownVehicle is returned when the IMEI has no live row in the company DB.
var ErrUnknownVehicle = errors.New("vehicle not found in company db")

// newStore resolves the tenant pool and wraps it in a store.
func (wa *WorkerAlert) newStore(companyCode string) (store, error) {
	db, err := wa.tm.DB(companyCode)
	if err != nil {
		return nil, err
	}
	// Router baca (replica-preferred). Ketika manager tidak bisa menyediakan
	// (mis. replika belum warm / unit test), pakai single-router primary agar
	// store tetap berfungsi penuh tanpa replika.
	ro, rerr := wa.tm.ReadRouter(companyCode)
	if rerr != nil || ro == nil {
		ro = tenant.NewSingleRouter(db)
	}
	return &companyStore{code: companyCode, db: db, ro: ro}, nil
}

// CompanyCode exposes the tenant this store is bound to.
func (s *companyStore) CompanyCode() string { return s.code }

// VehicleByIMEI returns (id, status) for a live vehicle row.
// READ path (replica): data vehicle jarang berubah sehingga aman terhadap
// replication lag singkat; ini juga query TERPANAS per-telemetry (5 dtk/device).
func (s *companyStore) VehicleByIMEI(imei string) (uint64, string, error) {
	var id uint64
	var status string
	err := s.ro.QueryRow(
		`SELECT id, COALESCE(status,'active') FROM vehicles WHERE imei = ? AND deleted_at IS NULL LIMIT 1`,
		imei,
	).Scan(&id, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", fmt.Errorf("%w: %s", ErrUnknownVehicle, imei)
		}
		return 0, "", err
	}
	return id, status, nil
}

// normalizeSeverity maps internal severities onto the alerts.severity enum
// (low|medium|high|critical, migration 008). 'warning' → 'medium'.
func normalizeSeverity(s string) string {
	switch s {
	case "warning", "info", "":
		return "medium"
	case "low", "medium", "high", "critical":
		return s
	default:
		return "medium"
	}
}

// InsertAlert creates an alerts row (migration 008) and returns the new id.
// Dialect-aware (PG-parity fix): pgx tidak mendukung res.LastInsertId() —
// dulu INSERT berhasil tapi fungsi gagal sebelum publish/notifikasi
// ("LastInsertId is not supported by this driver"). PG kini RETURNING id.
func (s *companyStore) InsertAlert(a models.AlertRecord) (uint64, error) {
	a.Severity = normalizeSeverity(a.Severity)
	id, err := dialect.InsertReturningID(dialect.Current(), context.Background(), s.db,
		`INSERT INTO alerts (vehicle_id, alert_type, severity, description, status, vehicle_lat, vehicle_lon)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.VehicleID, a.AlertType, a.Severity, a.Description, a.Status, a.VehicleLat, a.VehicleLon,
	)
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// FuelConfigFor returns the effective fuel config (B5a, migration 014): an
// enabled vehicle-specific row wins over the global default (vehicle_id IS
// NULL). READ path (replica).
func (s *companyStore) FuelConfigFor(vehicleID uint64) (models.FuelConfig, bool, error) {
	rows, err := s.ro.Query(
		`SELECT vehicle_id, drop_threshold, refuel_threshold, window_seconds, enabled
		 FROM fuel_configs WHERE enabled = TRUE AND (vehicle_id = ? OR vehicle_id IS NULL)
		 ORDER BY (vehicle_id IS NULL) ASC`, // vehicle-specific first
		vehicleID,
	)
	if err != nil {
		return models.FuelConfig{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			vid sql.NullInt64
			cfg models.FuelConfig
		)
		if err := rows.Scan(&vid, &cfg.DropThreshold, &cfg.RefuelThreshold, &cfg.WindowSeconds, &cfg.Enabled); err != nil {
			return models.FuelConfig{}, false, err
		}
		if vid.Valid {
			v := vid.Int64
			cfg.VehicleID = &v
		}
		return cfg, true, rows.Err()
	}
	return models.FuelConfig{}, false, rows.Err()
}

// HasOpenAlert reports whether an open alert of alertType already exists for
// the vehicle (dedup guard for OFFLINE / BATTERY_LOW background monitors).
func (s *companyStore) HasOpenAlert(vehicleID uint64, alertType string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM alerts WHERE vehicle_id = ? AND alert_type = ? AND status = 'open'`,
		vehicleID, alertType,
	).Scan(&n)
	return n > 0, err
}

// SpeedConfigFor returns the effective speed config: an active vehicle-specific
// row wins over the global default (vehicle_id IS NULL), migration 007.
// READ path (replica).
func (s *companyStore) SpeedConfigFor(vehicleID uint64) (models.SpeedConfig, bool, error) {
	rows, err := s.ro.Query(
		`SELECT vehicle_id, speed_limit_kmh, COALESCE(grace_margin_kmh,0)
		 FROM speed_configs WHERE is_active = TRUE AND (vehicle_id = ? OR vehicle_id IS NULL)
		 ORDER BY (vehicle_id IS NULL) ASC`, // vehicle-specific first
		vehicleID,
	)
	if err != nil {
		return models.SpeedConfig{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			vid sql.NullInt64
			cfg models.SpeedConfig
		)
		if err := rows.Scan(&vid, &cfg.SpeedLimitKMH, &cfg.GraceMargin); err != nil {
			return models.SpeedConfig{}, false, err
		}
		if vid.Valid {
			v := vid.Int64
			cfg.VehicleID = &v
		}
		return cfg, true, rows.Err()
	}
	return models.SpeedConfig{}, false, rows.Err()
}
