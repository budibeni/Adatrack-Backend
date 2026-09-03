package controllers

import (
	"encoding/json"
	"errors"

	"ajb_gps/internal/dialect"
	"ajb_gps/worker-alert/models"
)

// ---------------------------------------------------------------------------
// Geofences (migrations 005/006) — circle + polygon, GeoJSON coordinates.
// ---------------------------------------------------------------------------

// ActiveGeofences lists active geofences applicable to the vehicle via
// geofence_vehicles.
func (s *companyStore) ActiveGeofences(vehicleID uint64) ([]models.GeofenceDef, error) {
	// READ path (replica): konfigurasi geofence jarang berubah.
	// PG-parity fix (audit 2026-08-31): JSON_ARRAY() menghasilkan tipe json
	// di PG sedangkan kolom boundary_points adalah jsonb → SQLSTATE 42846
	// ("COALESCE could not convert type json to jsonb") yang membuat lookup
	// geofence gagal TOTAL di provider postgres. Pakai helper dialect
	// (persis pola deliveryDefaultExpr di store_notify_sos.go):
	//   mysql: JSON_ARRAY()   |   pg: '[]'::jsonb
	rows, err := s.ro.Query(
		`SELECT g.id, g.name, g.area_type, g.coordinates, COALESCE(g.radius_meters,0), COALESCE(g.boundary_points, `+dialect.Current().JSONArrayEmpty()+`)
		 FROM geofences g
		 JOIN geofence_vehicles gv ON gv.geofence_id = g.id AND gv.is_enabled = TRUE
		 WHERE g.is_active = TRUE AND gv.vehicle_id = ?`,
		vehicleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.GeofenceDef, 0, 4)
	for rows.Next() {
		var (
			g        models.GeofenceDef
			coords   []byte
			boundary []byte
			radius   float64
		)
		if err := rows.Scan(&g.ID, &g.Name, &g.AreaType, &coords, &radius, &boundary); err != nil {
			return nil, err
		}
		g.RadiusMeters = radius
		clat, clon, ring, perr := parseGeofenceGeometry(g.AreaType, coords, boundary)
		if perr != nil {
			// Malformed geometry: skip loudly (logged upstream), keep pipeline alive.
			continue
		}
		g.CenterLat, g.CenterLon, g.Boundary = clat, clon, ring
		out = append(out, g)
	}
	return out, rows.Err()
}

// parseGeofenceGeometry decodes stored geometry:
//   - circle: coordinates = {"type":"Point","coordinates":[lon,lat]}
//   - polygon: boundary_points = [{lat,lon}...] preferred; fallback to the
//     outer ring of the GeoJSON Polygon ([[ [lon,lat], ... ]]).
func parseGeofenceGeometry(areaType string, coordsJSON, boundaryJSON []byte) (lat, lon float64, ring [][2]float64, err error) {
	if len(coordsJSON) > 0 {
		var geo struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		}
		if jerr := json.Unmarshal(coordsJSON, &geo); jerr == nil {
			switch geo.Type {
			case "Point":
				var ll []float64
				if jerr := json.Unmarshal(geo.Coordinates, &ll); jerr == nil && len(ll) >= 2 {
					lon, lat = ll[0], ll[1] // GeoJSON order: [lon, lat]
				}
			case "Polygon":
				var rings [][][2]float64
				if jerr := json.Unmarshal(geo.Coordinates, &rings); jerr == nil && len(rings) > 0 {
					ring = make([][2]float64, 0, len(rings[0]))
					for _, pt := range rings[0] {
						if len(pt) == 2 {
							ring = append(ring, [2]float64{pt[1], pt[0]}) // → {lat,lon}
						}
					}
				}
			}
		}
	}
	// boundary_points overrides polygons: [{lat,lon}] objects or [[lat,lon]] pairs.
	if areaType == "polygon" && len(boundaryJSON) > 0 {
		var objs []map[string]float64
		if jerr := json.Unmarshal(boundaryJSON, &objs); jerr == nil && len(objs) > 0 && hasLatLon(objs[0]) {
			ring = ring[:0]
			for _, o := range objs {
				ring = append(ring, [2]float64{o["lat"], o["lon"]})
			}
		} else {
			var pairs [][2]float64
			if jerr2 := json.Unmarshal(boundaryJSON, &pairs); jerr2 == nil && len(pairs) > 0 {
				ring = pairs // {lat,lon} ordering per migration 005 doc
			}
		}
	}
	if lat == 0 && lon == 0 && len(ring) == 0 {
		return 0, 0, nil, errors.New("unparseable geofence geometry")
	}
	return lat, lon, ring, nil
}

func hasLatLon(m map[string]float64) bool {
	_, hasLat := m["lat"]
	_, hasLon := m["lon"]
	return hasLat && hasLon
}

// pointInPolygon reports whether (lat,lon) lies inside the ring using the
// ray-casting algorithm. ring entries are {lat,lon}.
func pointInPolygon(lat, lon float64, ring [][2]float64) bool {
	inside := false
	n := len(ring)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi, xi := ring[i][0], ring[i][1]
		yj, xj := ring[j][0], ring[j][1]
		intersect := (yi > lat) != (yj > lat) &&
			lon < (xj-xi)*(lat-yi)/(yj-yi)+xi
		if intersect {
			inside = !inside
		}
	}
	return inside
}
