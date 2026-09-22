package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openPG opens a pool bound to the given schema (search_path).
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

// mediaConfig is the per-company media configuration from the master schema.
type mediaConfig struct {
	bucket        string
	hmacSecret    string
	retentionDays int
	maxFileMB     int
}

// loadMediaConfig reads master.tm_company_media_config for one company.
func loadMediaConfig(ctx context.Context, db *sql.DB, company string) (mediaConfig, error) {
	var cfg mediaConfig
	err := db.QueryRowContext(ctx, `
SELECT COALESCE(bucket, ''), COALESCE(hmac_secret, ''),
       COALESCE(retention_days, 0), COALESCE(max_file_mb, 0)
FROM tm_company_media_config
WHERE company_code = $1 AND deleted_at IS NULL`, strings.ToUpper(company)).
		Scan(&cfg.bucket, &cfg.hmacSecret, &cfg.retentionDays, &cfg.maxFileMB)
	if err != nil {
		return cfg, fmt.Errorf("media config for %s: %w", company, err)
	}
	return cfg, nil
}

// setMaxFileMB temporarily lowers the per-company ceiling (oversize negative test).
func setMaxFileMB(ctx context.Context, db *sql.DB, company string, mb int) error {
	_, err := db.ExecContext(ctx, `
UPDATE tm_company_media_config SET max_file_mb = $2, updated_at = CURRENT_TIMESTAMP
WHERE company_code = $1`, strings.ToUpper(company), mb)
	return err
}

// vehicleIDForIMEI resolves the vehicle id of a registered device.
func vehicleIDForIMEI(ctx context.Context, db *sql.DB, imei string) (int64, string, error) {
	var (
		id      int64
		company string
	)
	err := db.QueryRowContext(ctx, `
SELECT COALESCE(vehicle_id, 0), company_code
FROM tm_vehicle_imei_map
WHERE imei = $1 AND deleted_at IS NULL`, imei).Scan(&id, &company)
	if err != nil {
		return 0, "", fmt.Errorf("resolve IMEI %s: %w", imei, err)
	}
	return id, strings.ToUpper(company), nil
}

// auditCount counts audit rows for one action since a timestamp.
func auditCount(ctx context.Context, db *sql.DB, action string, since time.Time) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `
SELECT count(*) FROM tm_audit_logs
WHERE action = $1 AND created_at >= $2`, action, since.UTC()).Scan(&n)
	return n, err
}

// backdateExpiry forces a catalog row past its retention so the sweep must pick
// it up (FR-8.7). It returns the object key of that row.
func backdateExpiry(ctx context.Context, db *sql.DB, mediaID int64) (string, error) {
	var key string
	err := db.QueryRowContext(ctx, `
UPDATE th_media_events
   SET expires_at = CURRENT_TIMESTAMP - INTERVAL '1 hour', updated_at = CURRENT_TIMESTAMP
 WHERE id = $1
RETURNING object_key`, mediaID).Scan(&key)
	if err != nil {
		return "", fmt.Errorf("backdate media %d: %w", mediaID, err)
	}
	return key, nil
}

// catalogStatus reads the lifecycle status of one row.
func catalogStatus(ctx context.Context, db *sql.DB, mediaID int64) (string, error) {
	var status string
	err := db.QueryRowContext(ctx, `SELECT status FROM th_media_events WHERE id = $1`, mediaID).Scan(&status)
	return status, err
}

// hmacHex signs a payload exactly like FR-8.1 requires (hex HMAC-SHA256).
func hmacHex(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
