package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/natsclient"
	"github.com/google/uuid"
)

func StartMaintenanceChecker(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute) // Check every 10 mins
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				checkMaintenance(ctx)
			}
		}
	}()
}

func checkMaintenance(ctx context.Context) {
	// Fetch all active companies
	companiesQuery := `SELECT code FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL`
	companyRows, err := dbclient.Pool.Query(ctx, companiesQuery)
	if err != nil {
		logger.Log.Error("Failed to query companies for maintenance", "err", err)
		return
	}
	
	var companies []string
	for companyRows.Next() {
		var code string
		if err := companyRows.Scan(&code); err == nil {
			companies = append(companies, code)
		}
	}
	companyRows.Close()

	for _, companyCode := range companies {
		schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
		query := fmt.Sprintf(`
			SELECT t.id, t.company_code, t.vehicle_id, t.task_name, v.odometer_km
			FROM %s.tm_maintenance_tasks t
			JOIN %s.tm_vehicles v ON t.vehicle_id = v.id
			WHERE t.is_active = true AND t.deleted_at IS NULL
			  AND (
				  (t.interval_km > 0 AND v.odometer_km >= (COALESCE(t.last_service_km, 0) + t.interval_km))
				  OR 
				  (t.interval_hours > 0 AND v.engine_hours >= (COALESCE(t.last_service_hours, 0) + t.interval_hours))
			  )
		`, schema, schema)
		
		rows, err := dbclient.Pool.Query(ctx, query)
		if err != nil {
			continue // schema might not exist yet
		}

		for rows.Next() {
			var (
				taskID      uuid.UUID
				cCode       string
				vehicleID   int
				taskName    string
				odometer    float64
			)
			if err := rows.Scan(&taskID, &cCode, &vehicleID, &taskName, &odometer); err != nil {
				continue
			}

			// Fire an alert via NATS
			alertPayload := map[string]interface{}{
				"alert_id":     uuid.New().String(),
				"type":         "MAINTENANCE_DUE",
				"company_code": cCode,
				"vehicle_id":   vehicleID,
				"message":      fmt.Sprintf("Maintenance due for task: %s", taskName),
				"timestamp":    time.Now().UTC(),
			}
			
			data, _ := json.Marshal(alertPayload)
			natsclient.NC.Publish("alert.MAINTENANCE_DUE", data)
			natsclient.NC.Publish("alert.all", data)
			
			logger.Log.Info("Triggered maintenance alert", "task_id", taskID, "vehicle_id", vehicleID, "company", cCode)
		}
		rows.Close()
	}
}
