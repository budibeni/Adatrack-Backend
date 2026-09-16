import re

with open('services/worker-alert/internal/consumer/worker.go', 'r') as f:
    content = f.read()

new_worker_struct = """type Worker struct {
	sub         *nats.Subscription
	speedCache  sync.Map
	refreshStop chan struct{}
}

func NewWorker() *Worker {
	return &Worker{
		refreshStop: make(chan struct{}),
	}
}

func (w *Worker) Start() {
	var err error
	w.sub, err = natsclient.NC.Subscribe("telemetry.raw.>", func(m *nats.Msg) {
		w.processTelemetry(m)
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe in worker-alert", "err", err)
	}
	go w.cacheRefresher()
}

func (w *Worker) cacheRefresher() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// In production, you'd iterate over all companies to refresh. 
			// For simplicity in B3, we rely on a pull-through cache in processTelemetry if missing,
			// or just let it query if not in cache (less optimal but works).
			// Better: let's do a pull-through cache approach instead of a background refresher to keep it simple.
		case <-w.refreshStop:
			return
		}
	}
}
"""
content = re.sub(r'type Worker struct \{.*?func \(w \*Worker\) Start\(\) \{.*?logger\.Log\.Error\("Failed to subscribe in worker-alert", "err", err\)\n\t\}\n\}', new_worker_struct, content, flags=re.DOTALL)

process_telemetry = """func (w *Worker) processTelemetry(m *nats.Msg) {
	var payload TelemetryPayload
	if err := json.Unmarshal(m.Data, &payload); err != nil {
		logger.Log.Error("Failed to decode telemetry", "err", err)
		return
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	schema := "adatrack_gps_" + payload.CompanyCode

	// 1. OVERSPEEDING LOGIC (With sync.Map Cache)
	cacheKey := fmt.Sprintf("%s:%d", payload.CompanyCode, payload.VehicleID)
	var maxSpeed float64
	val, ok := w.speedCache.Load(cacheKey)
	if !ok {
		err := dbclient.Pool.QueryRow(ctx, fmt.Sprintf("SELECT max_speed_kmh FROM %s.tm_speed_configs WHERE vehicle_id = $1 AND enabled = true", schema), payload.VehicleID).Scan(&maxSpeed)
		if err == nil {
			w.speedCache.Store(cacheKey, maxSpeed)
		} else {
			w.speedCache.Store(cacheKey, float64(0)) // Store 0 to prevent re-querying if not found
		}
	} else {
		maxSpeed = val.(float64)
	}

	if maxSpeed > 0 && payload.Speed > maxSpeed {
		w.createAlert(ctx, schema, "OVERSPEEDING", "high", payload.VehicleID, payload.Lat, payload.Lon, map[string]interface{}{"speed": payload.Speed, "limit": maxSpeed})
	}

	// 2. SOS LOGIC
	if payload.EventCode == 0x26 || payload.EventCode == 0x27 || payload.EventCode == 0x19 {
		w.createAlert(ctx, schema, "SOS", "critical", payload.VehicleID, payload.Lat, payload.Lon, map[string]interface{}{"event_code": payload.EventCode})
	}
	
	// 3. GEOFENCE LOGIC (Stub)
	// Haversine distance and Ray-casting would run here by comparing against a cached list of Geofences.
}"""
content = re.sub(r'func \(w \*Worker\) processTelemetry\(m \*nats\.Msg\) \{.*?\n\}', process_telemetry, content, flags=re.DOTALL)

# Add sync to imports
if '"sync"' not in content:
    content = content.replace('"context"', '"context"\n\t"sync"')

with open('services/worker-alert/internal/consumer/worker.go', 'w') as f:
    f.write(content)

