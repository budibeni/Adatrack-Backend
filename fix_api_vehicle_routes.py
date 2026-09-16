import re

with open('services/api-vehicle/internal/api/handlers.go', 'r') as f:
    content = f.read()

new_routes = """// Geofence CRUD
type GeofenceRequest struct {
	Name           string      `json:"name"`
	AreaType       string      `json:"area_type"` // circle or polygon
	Coordinates    interface{} `json:"coordinates"` // GeoJSON or custom struct
	RadiusMeters   float64     `json:"radius_meters"`
	BoundaryPoints interface{} `json:"boundary_points"`
}

func (h *Handler) CreateGeofence(w http.ResponseWriter, r *http.Request) {
	var req GeofenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	coordsJSON, _ := json.Marshal(req.Coordinates)
	boundsJSON, _ := json.Marshal(req.BoundaryPoints)
	
	var id int
	err := dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_geofences (name, area_type, coordinates, radius_meters, boundary_points, created_by) 
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, schema), 
		req.Name, req.AreaType, coordsJSON, req.RadiusMeters, boundsJSON, claims.UserID).Scan(&id)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create geofence")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Geofence %d created", id))
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": id}})
}

type RouteRequest struct {
	Name                   string      `json:"name"`
	Waypoints              interface{} `json:"waypoints"`
	VehicleID              int         `json:"vehicle_id"`
	DeviationThresholdMeters float64     `json:"deviation_threshold_meters"`
}

func (h *Handler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	var req RouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	waypointsJSON, _ := json.Marshal(req.Waypoints)
	
	var id int
	err := dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_routes (name, waypoints, vehicle_id, deviation_threshold_meters) 
		VALUES ($1, $2, $3, $4) RETURNING id`, schema), 
		req.Name, waypointsJSON, req.VehicleID, req.DeviationThresholdMeters).Scan(&id)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create route")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Route %d created", id))
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": id}})
}"""

content = re.sub(r'// Geofence CRUD \(basic stub for speed\).*?func \(h \*Handler\) CreateRoute.*?\}\n\}', new_routes, content, flags=re.DOTALL)

with open('services/api-vehicle/internal/api/handlers.go', 'w') as f:
    f.write(content)
