package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
	"github.com/jackc/pgx/v5"
)

func BatchInsert(ctx context.Context, payloads []models.TelemetryPayload) error {
	if len(payloads) == 0 {
		return nil
	}

	grouped := make(map[string][]models.TelemetryPayload)
	for _, p := range payloads {
		grouped[p.CompanyCode] = append(grouped[p.CompanyCode], p)
	}

	for company, items := range grouped {
		batch := &pgx.Batch{}
		schema := fmt.Sprintf("adatrack_gps_%s", company)
		query := fmt.Sprintf(`
			INSERT INTO %s.th_telemetry_logs 
			(vehicle_id, imei, company_code, lat, lon, speed, heading, altitude, acc_status, battery_level, satellites, gsm_signal, timestamp) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT DO NOTHING
		`, schema)

		fuelQuery := fmt.Sprintf(`
			INSERT INTO %s.th_fuel_logs 
			(vehicle_id, fuel_level, volume_liters, temperature_c, lat, lon, timestamp)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING
		`, schema)

		var expectedExecs int
		for _, item := range items {
			batch.Queue(query, 
				item.VehicleID, item.IMEI, item.CompanyCode, 
				item.Latitude, item.Longitude, item.Speed, 
				item.Heading, item.Altitude, item.ACCStatus, 
				item.Battery, item.Satellites, item.GSMSignal, item.Timestamp,
			)
			expectedExecs++
			
			if item.FuelLevel != nil || item.FuelVolume != nil || item.FuelTempC != nil {
				batch.Queue(fuelQuery,
					item.VehicleID, item.FuelLevel, item.FuelVolume, item.FuelTempC,
					item.Latitude, item.Longitude, item.Timestamp,
				)
				expectedExecs++
			}
		}

		var batchError error
		
		// Retry with backoff up to 3 times
		for attempt := 0; attempt < 3; attempt++ {
			batchError = nil
			br := dbclient.Pool.SendBatch(ctx, batch)
			for i := 0; i < expectedExecs; i++ {
				if _, err := br.Exec(); err != nil {
					batchError = err
					break
				}
			}
			br.Close()
			
			if batchError == nil {
				break
			}
			
			// Backoff
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
		
		if batchError != nil {
			logger.Log.Error("Batch insert failed after retries, routing to DLQ", "company", company, "err", batchError)
			// Enterprise Hardening: Prevent Poison Pill by routing bad payloads to DLQ instead of blocking NATS worker
			for _, item := range items {
				data, _ := json.Marshal(item)
				natsclient.PublishToDLQ("telemetry", data)
			}
		}
	}
	// We always return nil so the NATS message gets Ack()ed. 
	// The bad rows are now safely stored in the DLQ Stream.
	return nil
}
