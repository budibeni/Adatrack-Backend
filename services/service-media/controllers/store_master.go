package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"ajb_gps/service-media/models"
)

// CompanyMediaConfig loads per-company object-storage/ingest config from master
// company_media_config (migration 013). Returns (nil, nil) when the company has
// no row yet — the caller then falls back to environment defaults.
func CompanyMediaConfig(companyCode string) (*models.MediaConfig, error) {
	db := masterDB()
	if db == nil {
		return nil, fmt.Errorf("master db unavailable")
	}
	code := strings.ToUpper(strings.TrimSpace(companyCode))
	var cfg models.MediaConfig
	err := db.QueryRow(
		`SELECT company_code, bucket, COALESCE(retention_days,30), COALESCE(max_file_mb,100), hmac_secret
		 FROM company_media_config WHERE company_code = ?`, code,
	).Scan(&cfg.CompanyCode, &cfg.Bucket, &cfg.RetentionDays, &cfg.MaxFileMB, &cfg.HMACSecret)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &cfg, nil
}
