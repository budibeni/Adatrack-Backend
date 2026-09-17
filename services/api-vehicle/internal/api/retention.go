package api

import (
	"context"
	"fmt"
	"time"

	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/storage"
	"github.com/minio/minio-go/v7"
)

func StartRetentionJob(ctx context.Context, cfg *config.Config, store *storage.S3Store) {
	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				runRetention(ctx, cfg, store)
			}
		}
	}()
}

func runRetention(ctx context.Context, cfg *config.Config, store *storage.S3Store) {
	// We need to iterate over all tenants
	rows, err := dbclient.Pool.Query(ctx, "SELECT company_code FROM master.tm_companies WHERE deleted_at IS NULL")
	if err != nil {
		logger.Log.Error("Retention job failed to fetch companies", "err", err)
		return
	}
	
	var companies []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err == nil {
			companies = append(companies, code)
		}
	}
	rows.Close()

	for _, companyCode := range companies {
		schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
		
		// Find expired media
		query := fmt.Sprintf("SELECT id, storage_key FROM %s.th_media_events WHERE expires_at < NOW() AND status = 'complete'", schema)
		mRows, err := dbclient.Pool.Query(ctx, query)
		if err != nil {
			continue
		}
		
		var toDelete []struct{id int; key string}
		for mRows.Next() {
			var id int
			var key string
			if err := mRows.Scan(&id, &key); err == nil {
				toDelete = append(toDelete, struct{id int; key string}{id, key})
			}
		}
		mRows.Close()

		for _, item := range toDelete {
			// Delete from S3 (we need the parts or just use the client directly since we have the full key)
			err := store.Client().RemoveObject(ctx, cfg.S3BucketName, item.key, minio.RemoveObjectOptions{})
			if err != nil {
				logger.Log.Error("Failed to remove object from S3", "key", item.key, "err", err)
				continue
			}

			// Update status in DB
			dbclient.Pool.Exec(ctx, fmt.Sprintf("UPDATE %s.th_media_events SET status = 'expired' WHERE id = $1", schema), item.id)
			logger.Log.Info("Expired media deleted", "company", companyCode, "id", item.id)
		}
	}
}
