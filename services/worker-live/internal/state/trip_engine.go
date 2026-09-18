package state

import (
	"context"
	"time"
	"fmt"
	"encoding/json"

	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/redclient"
)

const (
	TripStopGraceSeconds = 30
	TripMinStopSeconds   = 60
)

type TripState struct {
	IsMoving     bool      `json:"is_moving"`
	LastMoveTime time.Time `json:"last_move_time"`
	LastStopTime time.Time `json:"last_stop_time"`
	CurrentTripID int64    `json:"current_trip_id"`
	
	TripStartLat float64   `json:"trip_start_lat"`
	TripStartLon float64   `json:"trip_start_lon"`
	TripDistance float64   `json:"trip_distance"`
	TripMaxSpeed float64   `json:"trip_max_speed"`
}

func ProcessTripAndStop(ctx context.Context, p *models.TelemetryPayload, prev *models.TelemetryPayload) {
	if p.VehicleID == 0 || p.CompanyCode == "" {
		return
	}
	
	stateKey := fmt.Sprintf("adatrack_gps:%s:vehicle:trip_state:%s", p.CompanyCode, p.IMEI)
	stateStr, err := redclient.Client.Get(ctx, stateKey).Result()
	
	var ts TripState
	if err == nil && stateStr != "" {
		json.Unmarshal([]byte(stateStr), &ts)
	}

	if ts.LastMoveTime.IsZero() {
		ts.LastMoveTime = time.Now()
		ts.LastStopTime = time.Now()
	}

	if p.Speed > 0 {
		ts.LastMoveTime = p.Timestamp
		
		if !ts.IsMoving {
			// Was stopped, check if grace period passed
			if p.Timestamp.Sub(ts.LastStopTime).Seconds() > TripStopGraceSeconds {
				// Transition to MOVING
				ts.IsMoving = true
				
				// Start new trip in DB
				var tripID int64
				schema := fmt.Sprintf("adatrack_gps_%s", p.CompanyCode)
				query := fmt.Sprintf("INSERT INTO %s.th_vehicle_trips (vehicle_id, start_time, start_lat, start_lon) VALUES ($1, $2, $3, $4) RETURNING id", schema)
				err := dbclient.Pool.QueryRow(ctx, query,
					p.VehicleID, p.Timestamp, p.Latitude, p.Longitude,
				).Scan(&tripID)
				
				if err != nil {
					logger.Log.Error("Failed to create trip", "err", err, "vehicle_id", p.VehicleID)
				} else {
					ts.CurrentTripID = tripID
					ts.TripStartLat = p.Latitude
					ts.TripStartLon = p.Longitude
					ts.TripDistance = 0
					ts.TripMaxSpeed = p.Speed
				}
			}
		} else {
			// Already moving, update stats
			if p.Speed > ts.TripMaxSpeed {
				ts.TripMaxSpeed = p.Speed
			}
			if prev != nil {
				// dist can be calculated from payload
				ts.TripDistance += p.OdometerKM - prev.OdometerKM
			}
		}
	} else {
		ts.LastStopTime = p.Timestamp
		
		if ts.IsMoving {
			// Was moving, check if min stop passed
			if p.Timestamp.Sub(ts.LastMoveTime).Seconds() > TripMinStopSeconds {
				// Transition to STOPPED
				ts.IsMoving = false
				
				// End trip in DB
				if ts.CurrentTripID > 0 {
					schema := fmt.Sprintf("adatrack_gps_%s", p.CompanyCode)
					updateQuery := fmt.Sprintf("UPDATE %s.th_vehicle_trips SET end_time = $1, end_lat = $2, end_lon = $3, distance_km = $4, max_speed = $5, duration_seconds = EXTRACT(EPOCH FROM ($1 - start_time)) WHERE id = $6", schema)
					_, err := dbclient.Pool.Exec(ctx, updateQuery,
						p.Timestamp, p.Latitude, p.Longitude, ts.TripDistance, ts.TripMaxSpeed, ts.CurrentTripID,
					)
					if err != nil {
						logger.Log.Error("Failed to update trip", "err", err, "trip_id", ts.CurrentTripID)
					}
					
					// Create stop record
					insertStopQuery := fmt.Sprintf("INSERT INTO %s.td_vehicle_stops (trip_id, start_time, lat, lon) VALUES ($1, $2, $3, $4)", schema)
					_, err = dbclient.Pool.Exec(ctx, insertStopQuery,
						ts.CurrentTripID, p.Timestamp, p.Latitude, p.Longitude,
					)
					if err != nil {
						logger.Log.Error("Failed to create stop", "err", err, "trip_id", ts.CurrentTripID)
					}
				}
			}
		}
	}
	
	// Save state back
	tsBytes, _ := json.Marshal(ts)
	redclient.Client.Set(ctx, stateKey, string(tsBytes), 7*24*time.Hour) // Keep trip state for a week
}
