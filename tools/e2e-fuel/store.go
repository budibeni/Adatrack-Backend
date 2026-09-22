package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

// openPG opens a pool bound to one schema (search_path).
func openPG(cfg pgConfig, schema string) (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&search_path=%s",
		cfg.user, cfg.password, cfg.host, cfg.port, cfg.db, schema)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// vehicleIDForIMEI resolves the tenant + vehicle of a registered device.
func vehicleIDForIMEI(ctx context.Context, master *sql.DB, imei string) (int64, string, error) {
	var (
		id      int64
		company string
	)
	err := master.QueryRowContext(ctx, `
SELECT COALESCE(vehicle_id, 0), company_code
FROM tm_vehicle_imei_map
WHERE imei = $1 AND deleted_at IS NULL`, imei).Scan(&id, &company)
	if err != nil {
		return 0, "", fmt.Errorf("resolve IMEI %s: %w", imei, err)
	}
	return id, strings.ToUpper(company), nil
}

// fuelHistoryRow is one td_fuel_logs reading used by the assertions.
func fuelRowsSince(ctx context.Context, tenant *sql.DB, imei string, since time.Time) (int, *float64, *float64, error) {
	var count int
	if err := tenant.QueryRowContext(ctx, `
SELECT count(*) FROM td_fuel_logs WHERE imei = $1 AND "timestamp" >= $2`,
		imei, since.UTC()).Scan(&count); err != nil {
		return 0, nil, nil, fmt.Errorf("count td_fuel_logs: %w", err)
	}
	var level, volume *float64
	err := tenant.QueryRowContext(ctx, `
SELECT fuel_level, fuel_volume FROM td_fuel_logs
WHERE imei = $1 AND "timestamp" >= $2
ORDER BY "timestamp" DESC LIMIT 1`, imei, since.UTC()).Scan(&level, &volume)
	if err == sql.ErrNoRows {
		return count, nil, nil, nil
	}
	if err != nil {
		return count, nil, nil, fmt.Errorf("latest td_fuel_logs: %w", err)
	}
	return count, level, volume, nil
}

// fuelConfigExists reports whether an enabled fuel config covers the vehicle
// (per-vehicle row or the tenant-wide default).
func fuelConfigExists(ctx context.Context, tenant *sql.DB, vehicleID int64) (bool, error) {
	var n int
	err := tenant.QueryRowContext(ctx, `
SELECT count(*) FROM tm_fuel_configs
WHERE deleted_at IS NULL AND enabled = TRUE
  AND (vehicle_id = $1 OR vehicle_id IS NULL)`, vehicleID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("fuel config lookup: %w", err)
	}
	return n > 0, nil
}

// resolveOpenFuelAlerts frees the dedup slot of previous runs so a repeated E2E
// run can raise the alert again (the engine keeps one OPEN alert per dedup key).
func resolveOpenFuelAlerts(ctx context.Context, tenant *sql.DB) (int64, error) {
	res, err := tenant.ExecContext(ctx, `
UPDATE th_alerts
   SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
 WHERE type IN ('fuel_drop', 'refuel')
   AND status IN ('open', 'acknowledged')`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// liveState is the subset of the Redis fuel state the harness asserts on.
type liveState struct {
	IMEI       string   `json:"imei"`
	Lat        float64  `json:"lat"`
	Lon        float64  `json:"lon"`
	FuelLevel  *float64 `json:"fuel_level"`
	FuelVolume *float64 `json:"fuel_volume"`
	LastSeen   int64    `json:"last_seen"`
}

// waitForLiveFuel polls the tenant live-state key until it carries a fuel level.
func waitForLiveFuel(ctx context.Context, rdb *redis.Client, key string, timeout time.Duration) (liveState, error) {
	deadline := time.Now().Add(timeout)
	var last liveState
	for time.Now().Before(deadline) {
		raw, err := rdb.Get(ctx, key).Result()
		if err == nil && raw != "" {
			if uerr := json.Unmarshal([]byte(raw), &last); uerr == nil && last.FuelLevel != nil {
				return last, nil
			}
		}
		select {
		case <-ctx.Done():
			return liveState{}, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return last, fmt.Errorf("live state %s carried no fuel_level within %s", key, timeout)
}

// liveStateKey mirrors the documented Redis key layout (FR-2.1).
func liveStateKey(prefix, company, imei string) string {
	if prefix == "" {
		prefix = "adatrack_gps:"
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return prefix + strings.ToLower(company) + ":vehicle:state:" + imei
}
