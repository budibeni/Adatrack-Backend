package db

import (
	"context"
	"fmt"

	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/models"
	"github.com/jackc/pgx/v5"
)

func BatchInsert(ctx context.Context, payloads []models.TelemetryPayload) error {
	if len(payloads) == 0 {
		return nil
	}

	// Group by company to insert into specific schemas
	grouped := make(map[string][]models.TelemetryPayload)
	for _, p := range payloads {
		grouped[p.CompanyCode] = append(grouped[p.CompanyCode], p)
	}

	for company, items := range grouped {
		batch := &pgx.Batch{}
		schema := fmt.Sprintf("adatrack_gps_%s", company)
		query := fmt.Sprintf(`
			INSERT INTO %s.th_telemetry_logs 
			(vehicle_id, imei, company_code, lat, lon, speed, heading, altitude, acc_status, battery_level, timestamp) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT DO NOTHING
		`, schema)

		for _, item := range items {
			batch.Queue(query, 
				item.VehicleID, item.IMEI, item.CompanyCode, 
				item.Latitude, item.Longitude, item.Speed, 
				item.Heading, item.Altitude, item.ACCStatus, 
				item.Battery, item.Timestamp,
			)
		}

		br := dbclient.Pool.SendBatch(ctx, batch)
		for i := 0; i < len(items); i++ {
			_, err := br.Exec()
			if err != nil {
				br.Close()
				logger.Log.Error("Batch insert failed", "company", company, "err", err)
				return err // Will cause NATS to redeliver
			}
		}
		br.Close()
	}
	return nil
}
