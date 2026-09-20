package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
	"backend/internal/tenant"
	"backend/internal/redclient"
	"backend/worker-alert/internal/alert"
	"backend/worker-alert/internal/geo"
)

type Worker struct {
	sub           *nats.Subscription
	offlineSub    *nats.Subscription
	speedCache    sync.Map // cacheKey -> SpeedConfig
	dedupCache    sync.Map // cacheKey -> time.Time
	prevTelemetry sync.Map // cacheKey -> models.TelemetryPayload
	refreshStop   chan struct{}
	safetyEngine  *alert.SafetyEngine
}

func NewWorker() *Worker {
	return &Worker{
		refreshStop:  make(chan struct{}),
		safetyEngine: alert.NewSafetyEngine(),
	}
}

func (w *Worker) Start() {
	var err error
	w.sub, err = natsclient.JS.QueueSubscribe("telemetry.raw.>", "worker-alert-group", func(m *nats.Msg) {
		w.processTelemetry(m)
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe in worker-alert", "err", err)
	}
	w.offlineSub, err = natsclient.NC.Subscribe("alert.internal.offline", func(m *nats.Msg) {
		var payload models.TelemetryPayload
		if err := json.Unmarshal(m.Data, &payload); err == nil {
			if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, "OFFLINE", 30*time.Minute) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				schema := "adatrack_gps_" + payload.CompanyCode
				w.createAlert(ctx, schema, "OFFLINE", "medium", payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
					"status": "OFFLINE",
					"last_seen": payload.Timestamp,
				})
			}
		}
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe offline alerts in worker-alert", "err", err)
	}
}

type SpeedConfig struct {
	MaxSpeed    float64
	GraceMargin float64
	Severity    string
}

func (w *Worker) isDuplicate(companyCode string, vehicleID int, alertType string, window time.Duration) bool {
	cacheKey := fmt.Sprintf("dedup:%s:%d:%s", companyCode, vehicleID, alertType)
	now := time.Now()

	// Check in-memory sync.Map first
	if val, ok := w.dedupCache.Load(cacheKey); ok {
		lastTime := val.(time.Time)
		if now.Sub(lastTime) < window {
			return true
		}
	}
	w.dedupCache.Store(cacheKey, now)

	// Also check Redis if available for multi-instance worker dedup
	if redclient.Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		set, err := redclient.Client.SetNX(ctx, "alert:"+cacheKey, "1", window).Result()
		if err == nil && !set {
			return true
		}
	}

	return false
}

func (w *Worker) processTelemetry(m *nats.Msg) {
	var payload models.TelemetryPayload
	if err := json.Unmarshal(m.Data, &payload); err != nil {
		logger.Log.Error("Failed to decode telemetry in worker-alert", "err", err)
		return
	}

	if payload.CompanyCode == "" || payload.VehicleID == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	schema := "adatrack_gps_" + payload.CompanyCode
	point := geo.Point{Lat: payload.Latitude, Lon: payload.Longitude}

	// 1. OVERSPEEDING LOGIC
	w.evaluateOverspeed(ctx, schema, payload)

	// 1.5. SAFETY ENGINE LOGIC
	cacheKey := fmt.Sprintf("%s:%d", payload.CompanyCode, payload.VehicleID)
	if prev, ok := w.prevTelemetry.Load(cacheKey); ok {
		prevPayload := prev.(models.TelemetryPayload)
		w.safetyEngine.Evaluate(ctx, payload.CompanyCode, payload, prevPayload.Speed, prevPayload.Heading)
	}
	w.prevTelemetry.Store(cacheKey, payload)

	// 2. SOS LOGIC
	if payload.EventCode == 0x26 || payload.EventCode == 0x27 || payload.EventCode == 0x19 {
		if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, "SOS", 3*time.Minute) {
			meta := map[string]interface{}{
				"event_code":         payload.EventCode,
				"escalation_minutes": 5,
				"escalation_max":     3,
				"escalation_level":   0,
				"speed":              payload.Speed,
			}
			w.createAlert(ctx, schema, "SOS", "critical", payload.VehicleID, payload.Latitude, payload.Longitude, meta)
		}
	}

	// 3. DRIVER BEHAVIOR LOGIC (B8)
	if payload.EventCode >= 1 && payload.EventCode <= 3 {
		alertType := ""
		if payload.EventCode == 1 {
			alertType = "HARSH_ACCELERATION"
		} else if payload.EventCode == 2 {
			alertType = "HARSH_BRAKING"
		} else if payload.EventCode == 3 {
			alertType = "HARSH_CORNERING"
		}

		if alertType != "" && !w.isDuplicate(payload.CompanyCode, payload.VehicleID, alertType, 1*time.Minute) {
			w.createAlert(ctx, schema, alertType, "medium", payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
				"speed": payload.Speed,
			})
		}
	}

	// 4. BATTERY LOW LOGIC (< 20% default, < 10% critical)
	if payload.Battery > 0 && payload.Battery < 20 {
		severity := "high"
		if payload.Battery < 10 {
			severity = "critical"
		}
		if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, "BATTERY_LOW", 15*time.Minute) {
			w.createAlert(ctx, schema, "BATTERY_LOW", severity, payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
				"battery_level": payload.Battery,
			})
		}
	}

	// 4. GEOFENCE EVALUATION (Circle + Polygon, Entry + Exit)
	w.evaluateGeofences(ctx, schema, payload, point)

	// 5. ROUTE DEVIATION EVALUATION
	w.evaluateRouteDeviation(ctx, schema, payload, point)

	// 6. FUEL EVALUATION
	w.evaluateFuel(ctx, schema, payload)
}

func (w *Worker) evaluateOverspeed(ctx context.Context, schema string, payload models.TelemetryPayload) {
	cacheKey := fmt.Sprintf("%s:%d", payload.CompanyCode, payload.VehicleID)
	var config SpeedConfig
	val, ok := w.speedCache.Load(cacheKey)
	if !ok {
		// 1. Query vehicle-specific config
		err := tenant.NewReadRouter(payload.CompanyCode).QueryRow(ctx, fmt.Sprintf(`
			SELECT max_speed_kmh, COALESCE(grace_margin_percent, 0), COALESCE(alert_severity, 'medium')
			FROM %s.tm_speed_configs 
			WHERE vehicle_id = $1 AND enabled = true AND deleted_at IS NULL
		`, schema), payload.VehicleID).Scan(&config.MaxSpeed, &config.GraceMargin, &config.Severity)

		// 2. Fallback to global config (vehicle_id IS NULL)
		if err != nil {
			err = tenant.NewReadRouter(payload.CompanyCode).QueryRow(ctx, fmt.Sprintf(`
				SELECT max_speed_kmh, COALESCE(grace_margin_percent, 0), COALESCE(alert_severity, 'medium')
				FROM %s.tm_speed_configs 
				WHERE vehicle_id IS NULL AND enabled = true AND deleted_at IS NULL
			`, schema)).Scan(&config.MaxSpeed, &config.GraceMargin, &config.Severity)
		}

		if err == nil {
			w.speedCache.Store(cacheKey, config)
		} else {
			w.speedCache.Store(cacheKey, SpeedConfig{MaxSpeed: 0})
		}
	} else {
		config = val.(SpeedConfig)
	}

	if config.MaxSpeed > 0 {
		effectiveLimit := config.MaxSpeed * (1.0 + config.GraceMargin/100.0)
		if payload.Speed > effectiveLimit {
			severity := config.Severity
			if severity == "" {
				severity = "high"
			}
			// Critical tier if > 1.5x limit
			if payload.Speed >= config.MaxSpeed*1.5 {
				severity = "critical"
			}

			if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, "OVERSPEEDING", 2*time.Minute) {
				w.createAlert(ctx, schema, "OVERSPEEDING", severity, payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
					"speed":           payload.Speed,
					"limit":           config.MaxSpeed,
					"effective_limit": effectiveLimit,
					"grace_margin":    config.GraceMargin,
				})
			}
		}
	}
}

func (w *Worker) evaluateGeofences(ctx context.Context, schema string, payload models.TelemetryPayload, point geo.Point) {
	rows, err := tenant.NewReadRouter(strings.TrimPrefix(schema, "adatrack_gps_")).Query(ctx, fmt.Sprintf(`
		SELECT g.id, g.name, g.area_type, g.coordinates, g.radius_meters, g.boundary_points
		FROM %s.tm_geofences g
		LEFT JOIN %s.tm_geofence_vehicles gv ON g.id = gv.geofence_id
		WHERE g.deleted_at IS NULL AND (gv.vehicle_id IS NULL OR gv.vehicle_id = $1)
	`, schema, schema), payload.VehicleID)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var gid int
		var name, areaType string
		var coordsJSON, boundsJSON []byte
		var radius float64

		if err := rows.Scan(&gid, &name, &areaType, &coordsJSON, &radius, &boundsJSON); err != nil {
			continue
		}

		isInside := false
		if areaType == "circle" {
			var center geo.Point
			if err := json.Unmarshal(coordsJSON, &center); err == nil {
				dist := geo.Haversine(point, center)
				if dist <= radius {
					isInside = true
				}
			}
		} else if areaType == "polygon" {
			var polygon []geo.Point
			if err := json.Unmarshal(boundsJSON, &polygon); err == nil {
				isInside = geo.RayCasting(point, polygon)
			}
		}

		// State tracking in Redis
		stateKey := fmt.Sprintf("geofence:state:%s:%d:%d", payload.CompanyCode, gid, payload.VehicleID)
		prevState := ""
		if redclient.Client != nil {
			prevState, _ = redclient.Client.Get(ctx, stateKey).Result()
		}

		if isInside && prevState != "inside" {
			// Trigger ENTRY
			if redclient.Client != nil {
				redclient.Client.Set(ctx, stateKey, "inside", 24*time.Hour)
			}
			if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, fmt.Sprintf("GEOFENCE_ENTRY_%d", gid), 5*time.Minute) {
				w.createAlert(ctx, schema, "GEOFENCE_ENTRY", "medium", payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
					"geofence_id":   gid,
					"geofence_name": name,
					"transition":    "entry",
				})
			}
		} else if !isInside && prevState == "inside" {
			// Trigger EXIT
			if redclient.Client != nil {
				redclient.Client.Set(ctx, stateKey, "outside", 24*time.Hour)
			}
			if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, fmt.Sprintf("GEOFENCE_EXIT_%d", gid), 5*time.Minute) {
				w.createAlert(ctx, schema, "GEOFENCE_EXIT", "medium", payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
					"geofence_id":   gid,
					"geofence_name": name,
					"transition":    "exit",
				})
			}
		}
	}
}

func (w *Worker) evaluateRouteDeviation(ctx context.Context, schema string, payload models.TelemetryPayload, point geo.Point) {
	rows, err := tenant.NewReadRouter(strings.TrimPrefix(schema, "adatrack_gps_")).Query(ctx, fmt.Sprintf(`
		SELECT r.id, r.name, r.waypoints, COALESCE(r.deviation_threshold_meters, 200)
		FROM %s.th_route_assignments ra
		JOIN %s.tm_routes r ON ra.route_id = r.id
		WHERE ra.vehicle_id = $1 AND ra.status IN ('assigned', 'in_progress') AND r.deleted_at IS NULL
	`, schema, schema), payload.VehicleID)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var routeID int
		var routeName string
		var waypointsJSON []byte
		var threshold float64

		if err := rows.Scan(&routeID, &routeName, &waypointsJSON, &threshold); err != nil {
			continue
		}

		var waypoints []geo.Point
		if err := json.Unmarshal(waypointsJSON, &waypoints); err != nil || len(waypoints) < 2 {
			continue
		}

		dist := geo.DistanceToPolyline(point, waypoints)
		if dist > threshold {
			isDup := w.isDuplicate(payload.CompanyCode, payload.VehicleID, fmt.Sprintf("ROUTE_DEVIATION_%d", routeID), 3*time.Minute)
			if !isDup {
				w.createAlert(ctx, schema, "ROUTE_DEVIATION", "high", payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
					"route_id":             routeID,
					"route_name":           routeName,
					"deviation_meters":     dist,
					"threshold_meters":     threshold,
					"max_deviation_meters": dist,
				})
			} else {
				// Update max_deviation_meters of open alert
				dbclient.Pool.Exec(ctx, fmt.Sprintf(`
					UPDATE %s.th_alerts 
					SET metadata = jsonb_set(metadata::jsonb, '{max_deviation_meters}', to_jsonb($1::numeric)) 
					WHERE vehicle_id = $2 AND type = 'ROUTE_DEVIATION' AND status = 'open' 
					AND (metadata->>'route_id')::int = $3 AND (metadata->>'max_deviation_meters')::numeric < $1
				`, schema), dist, payload.VehicleID, routeID)
			}
		}
	}
}

func (w *Worker) evaluateFuel(ctx context.Context, schema string, payload models.TelemetryPayload) {
	if payload.FuelLevel == nil && payload.FuelVolume == nil {
		return
	}

	type FuelConfig struct {
		MaxVolume       float64
		RefuelThreshold float64
		DropThreshold   float64
	}

	var config FuelConfig
	err := tenant.NewReadRouter(payload.CompanyCode).QueryRow(ctx, fmt.Sprintf(`
		SELECT max_volume_liters, refuel_threshold_liters, drop_threshold_liters 
		FROM %s.tm_fuel_configs 
		WHERE vehicle_id = $1 AND enabled = true
	`, schema), payload.VehicleID).Scan(&config.MaxVolume, &config.RefuelThreshold, &config.DropThreshold)

	if err != nil {
		return
	}

	var currentVol float64
	if payload.FuelVolume != nil {
		currentVol = *payload.FuelVolume
	} else if payload.FuelLevel != nil {
		currentVol = (*payload.FuelLevel / 100.0) * config.MaxVolume
	}

	fuelKey := fmt.Sprintf("fuel:%s:%d", payload.CompanyCode, payload.VehicleID)
	lastVolRaw, ok := w.speedCache.Load(fuelKey)
	if ok {
		lastVol := lastVolRaw.(float64)
		diff := currentVol - lastVol

		if diff < 0 && (-diff) >= config.DropThreshold {
			if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, "FUEL_DROP", 5*time.Minute) {
				w.createAlert(ctx, schema, "FUEL_DROP", "high", payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
					"drop_volume":     -diff,
					"current_volume":  currentVol,
					"previous_volume": lastVol,
				})
			}
		} else if diff > 0 && diff >= config.RefuelThreshold {
			if !w.isDuplicate(payload.CompanyCode, payload.VehicleID, "REFUEL", 5*time.Minute) {
				w.createAlert(ctx, schema, "REFUEL", "low", payload.VehicleID, payload.Latitude, payload.Longitude, map[string]interface{}{
					"refuel_volume":   diff,
					"current_volume":  currentVol,
					"previous_volume": lastVol,
				})
			}
		}
	}

	w.speedCache.Store(fuelKey, currentVol)
}

func severityRank(s string) int {
	switch s {
	case "low", "info":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	default:
		return 1
	}
}

func (w *Worker) createAlert(ctx context.Context, schema, alertType, severity string, vehicleID int, lat, lon float64, metadata map[string]interface{}) {
	metaJSON, _ := json.Marshal(metadata)
	query := fmt.Sprintf(`
		INSERT INTO %s.th_alerts (type, severity, vehicle_id, lat, lon, metadata, status, created_at) 
		VALUES ($1, $2, $3, $4, $5, $6, 'open', NOW()) RETURNING id
	`, schema)

	var alertID int64
	err := dbclient.Pool.QueryRow(ctx, query, alertType, severity, vehicleID, lat, lon, metaJSON).Scan(&alertID)
	if err != nil {
		logger.Log.Error("Failed to persist alert", "type", alertType, "err", err)
		return
	}

	// 1. Dispatch to Notification Preferences
	w.dispatchNotifications(ctx, schema, alertID, alertType, severity, metadata)

	companyCode := strings.TrimPrefix(schema, "adatrack_gps_")

	// 2. Publish to NATS for real-time WebSocket fanout
	eventPayload := map[string]interface{}{
		"alert_id":     alertID,
		"type":         alertType,
		"severity":     severity,
		"vehicle_id":   vehicleID,
		"lat":          lat,
		"lon":          lon,
		"metadata":     metadata,
		"status":       "open",
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
		"company_code": companyCode,
	}
	data, _ := json.Marshal(eventPayload)
	natsclient.NC.Publish(fmt.Sprintf("alert.%s", alertType), data)
	natsclient.NC.Publish("alert.all", data)
}

func (w *Worker) dispatchNotifications(ctx context.Context, schema string, alertID int64, alertType, severity string, metadata map[string]interface{}) {
	rows, err := tenant.NewReadRouter(strings.TrimPrefix(schema, "adatrack_gps_")).Query(ctx, fmt.Sprintf(`
		SELECT user_id, channel, min_severity 
		FROM %s.tm_notification_preferences 
		WHERE alert_type = $1 AND enabled = true
	`, schema), alertType)
	if err != nil {
		return
	}
	defer rows.Close()

	alertRank := severityRank(severity)
	for rows.Next() {
		var userID int
		var channel, minSev string
		if err := rows.Scan(&userID, &channel, &minSev); err != nil {
			continue
		}

		if alertRank >= severityRank(minSev) {
			// Record pending notification in td_notifications
			metaBytes, _ := json.Marshal(map[string]interface{}{
				"alert_id":   alertID,
				"alert_type": alertType,
				"severity":   severity,
			})
			dbclient.Pool.Exec(ctx, fmt.Sprintf(`
				INSERT INTO %s.td_notifications (alert_id, user_id, channel, status, provider_response, created_at)
				VALUES ($1, $2, $3, 'pending', $4, NOW())
			`, schema), alertID, userID, channel, metaBytes)

			// Publish notification channel message
			natsclient.NC.Publish(fmt.Sprintf("notify.%s.%d", channel, userID), metaBytes)
		}
	}
}

func (w *Worker) Stop() {
	if w.sub != nil {
		w.sub.Unsubscribe()
	}
	if w.offlineSub != nil {
		w.offlineSub.Unsubscribe()
	}
}
