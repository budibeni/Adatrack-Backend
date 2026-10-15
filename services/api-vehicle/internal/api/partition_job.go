package api

import (
	"context"
	"fmt"
	"time"

	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
)

// StartPartitionJob runs partition maintenance on boot and periodically every 24 hours.
// It ensures that partitions for the next 12 months are always pre-created across all tenant schemas.
func StartPartitionJob(ctx context.Context, cfg *config.Config) {
	go func() {
		// Small delay on boot to allow DB connection pool to settle
		time.Sleep(3 * time.Second)
		runPartitionMaintenance(ctx)

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runPartitionMaintenance(ctx)
			}
		}
	}()
}

func runPartitionMaintenance(ctx context.Context) {
	if dbclient.Pool == nil {
		return
	}

	rows, err := dbclient.Pool.Query(ctx, `
		SELECT schema_name 
		FROM information_schema.schemata 
		WHERE schema_name LIKE 'adatrack_gps_%' 
		  AND schema_name != 'adatrack_gps_master'
		ORDER BY schema_name
	`)
	if err != nil {
		logger.Log.Error("PartitionJob: failed to fetch tenant schemas", "err", err)
		return
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			schemas = append(schemas, s)
		}
	}

	for _, schema := range schemas {
		query := fmt.Sprintf("SET search_path TO %s, public; SELECT %s.tm_ensure_monthly_partitions(12);", schema, schema)
		_, err := dbclient.Pool.Exec(ctx, query)
		if err != nil {
			logger.Log.Warn("PartitionJob: error maintaining partitions for schema", "schema", schema, "err", err)
		} else {
			logger.Log.Info("PartitionJob: verified and ensured partitions", "schema", schema)
		}
	}
}
