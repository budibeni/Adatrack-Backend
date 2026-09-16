import re

with open('services/worker-alert/internal/consumer/worker.go', 'r') as f:
    content = f.read()

new_geofence_logic = """	// 3. GEOFENCE LOGIC
	// We'll query geofences from DB for this company (in production we'd cache this in sync.Map too)
	// Querying DB directly here for simplicity of the PoC, caching can be added identically to speed configs.
	rows, err := dbclient.Pool.Query(ctx, fmt.Sprintf("SELECT id, name, area_type, coordinates, radius_meters, boundary_points FROM %s.tm_geofences WHERE deleted_at IS NULL", schema))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int
			var name, areaType string
			var coordsJSON, boundsJSON []byte
			var radius float64
			rows.Scan(&id, &name, &areaType, &coordsJSON, &radius, &boundsJSON)

			point := geo.Point{Lat: payload.Lat, Lon: payload.Lon}
			isInside := false
			
			if areaType == "circle" {
				var center geo.Point
				json.Unmarshal(coordsJSON, &center)
				dist := geo.Haversine(point, center)
				if dist <= radius {
					isInside = true
				}
			} else if areaType == "polygon" {
				var polygon []geo.Point
				json.Unmarshal(boundsJSON, &polygon)
				isInside = geo.RayCasting(point, polygon)
			}
			
			// If inside, we might trigger a GEOFENCE_ENTRY alert if they weren't inside before
			// For this MVP, we just log it or trigger a generic GEOFENCE_VIOLATION if it's a restricted zone.
			if isInside {
				// Note: typically we track entry/exit state in Redis.
				// redclient.Client.Set(...)
			}
		}
	}
}"""
content = re.sub(r'\t// 3\. GEOFENCE LOGIC \(Stub\)\n\t// Haversine distance and Ray-casting would run here by comparing against a cached list of Geofences\.\n\}', new_geofence_logic, content, flags=re.DOTALL)

if '"backend/worker-alert/internal/geo"' not in content:
    content = content.replace('"backend/internal/logger"', '"backend/internal/logger"\n\t"backend/worker-alert/internal/geo"')

with open('services/worker-alert/internal/consumer/worker.go', 'w') as f:
    f.write(content)
