import re

with open('services/service-websocket/internal/api/handlers.go', 'r') as f:
    content = f.read()

new_func = """func (h *Handler) ListVehicles(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	type Vehicle struct {
		ID          int64  `json:"id"`
		PlateNumber string `json:"plate_number"`
		Status      string `json:"status"`
	}
	var vehicles []Vehicle
	
	var query string
	var args []interface{}
	
	if claims.Role == "Admin" || claims.Role == "SuperAdmin" {
		query = fmt.Sprintf("SELECT id, plate_number, status FROM %s.tm_vehicles WHERE deleted_at IS NULL", schema)
	} else {
		query = fmt.Sprintf("SELECT v.id, v.plate_number, v.status FROM %s.tm_vehicles v JOIN %s.tm_user_vehicles uv ON v.id = uv.vehicle_id WHERE uv.user_id = $1 AND v.deleted_at IS NULL", schema, schema)
		args = append(args, claims.UserID)
	}
	
	rows, err := dbclient.Pool.Query(r.Context(), query, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var v Vehicle
			rows.Scan(&v.ID, &v.PlateNumber, &v.Status)
			vehicles = append(vehicles, v)
		}
	}
	
	// Enrich with Redis Live State
	var enriched []map[string]interface{}
	for _, v := range vehicles {
		vMap := map[string]interface{}{
			"id": v.ID,
			"plate_number": v.PlateNumber,
			"db_status": v.Status,
		}
		
		key := fmt.Sprintf("telemetry:live:%s:%d", claims.CompanyCode, v.ID)
		val, err := redclient.Client.Get(r.Context(), key).Result()
		if err == nil && val != "" {
			var state map[string]interface{}
			if err := json.Unmarshal([]byte(val), &state); err == nil {
				vMap["live_state"] = state
			}
		} else {
			vMap["live_state"] = nil
		}
		enriched = append(enriched, vMap)
	}
	
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": enriched,
	})
}
"""

content = re.sub(r'func \(h \*Handler\) ListVehicles.*?json\.NewEncoder\(w\)\.Encode\(map\[string\]interface\{\}\{\n\t\t"status": "success",\n\t\t"data": vehicleIDs,\n\t\}\)\n\}', new_func, content, flags=re.DOTALL)

# Add "backend/internal/redclient" if not imported
if '"backend/internal/redclient"' not in content:
    content = content.replace('"backend/internal/logger"', '"backend/internal/logger"\n\t"backend/internal/redclient"')

with open('services/service-websocket/internal/api/handlers.go', 'w') as f:
    f.write(content)
