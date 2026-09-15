// Package main — verification helpers for the E2E harness.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openPG opens a pool bound to a specific tenant schema (search_path).
func openPG(cfg pgConfig, schema string) (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&search_path=%s",
		cfg.user, cfg.password, cfg.host, cfg.port, cfg.db, schema)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// countTelemetrySince counts persisted telemetry rows for one IMEI at or after
// `since` (used both for the single-device flow and the load test).
func countTelemetrySince(ctx context.Context, db *sql.DB, imei string, since time.Time) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM th_telemetry_logs
		WHERE imei = $1 AND "timestamp" >= $2`, imei, since.UTC()).Scan(&n)
	return n, err
}

// waitForTelemetryRow polls until at least one row exists (≤ timeout), returning
// the newest row for verification.
func waitForTelemetryRow(ctx context.Context, db *sql.DB, imei string, since time.Time, timeout time.Duration) (float64, float64, float64, error) {
	deadline := time.Now().Add(timeout)
	var (
		lat, lon, speed float64
	)
	for time.Now().Before(deadline) {
		err := db.QueryRowContext(ctx, `
			SELECT latitude, longitude, speed FROM th_telemetry_logs
			WHERE imei = $1 AND "timestamp" >= $2
			ORDER BY "timestamp" DESC LIMIT 1`, imei, since.UTC()).Scan(&lat, &lon, &speed)
		if err == nil {
			return lat, lon, speed, nil
		}
		if err != sql.ErrNoRows {
			return 0, 0, 0, err
		}
		select {
		case <-ctx.Done():
			return 0, 0, 0, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return 0, 0, 0, fmt.Errorf("no telemetry row for imei %s within %s", imei, timeout)
}

// liveState is the subset of the Redis live state the harness asserts on.
type liveState struct {
	IMEI     string  `json:"imei"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Speed    float64 `json:"speed"`
	Status   string  `json:"status"`
	ACC      *bool   `json:"acc"`
	LastSeen int64   `json:"last_seen"`
}

// waitForLiveState polls the tenant live-state key until it exists and is fresh.
func waitForLiveState(ctx context.Context, rdb *redis.Client, key string, timeout time.Duration) (liveState, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := rdb.Get(ctx, key).Result()
		if err == nil && raw != "" {
			var st liveState
			if uerr := json.Unmarshal([]byte(raw), &st); uerr == nil {
				return st, nil
			}
		}
		select {
		case <-ctx.Done():
			return liveState{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return liveState{}, fmt.Errorf("live state %s not present within %s", key, timeout)
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
