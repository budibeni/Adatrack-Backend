package controllers

import (
	"context"
	"fmt"

	"adatrack_gps/internal/geo"
)

// RegionStore supplies the master reference rows used by the offline reverse
// geocoder (B7.3). It is a separate interface on purpose: the core `Store`
// surface (and every fake implementing it) stays untouched.
type RegionStore interface {
	// RegionCentroids returns every administrative row that carries a centroid,
	// most specific level first.
	RegionCentroids(ctx context.Context) ([]geo.Region, error)
}

// RegionCentroids loads the reference centroids from the master schema.
//
// Rows come from the most specific level that actually has coordinates
// (`COALESCE` onto the parent centroid is NOT used: inventing a village centroid
// from its city would fabricate precision). The seeded reference data currently
// carries coordinates for provinces and cities only, so the SQL also asks for
// districts/subdistricts — the moment the seed gains their coordinates the
// resolver automatically becomes more precise, without a code change.
func (s *PostgresStore) RegionCentroids(ctx context.Context) ([]geo.Region, error) {
	rows, err := s.tenants.Master().DB.QueryContext(ctx, `
		SELECT 'subdistrict' AS level,
		       COALESCE(p.name, ''), COALESCE(c.name, ''), d.name, sd.name,
		       COALESCE(sd.postal_code, d.postal_code, ''),
		       sd.latitude, sd.longitude
		  FROM tm_subdistricts sd
		  JOIN tm_districts d ON d.id = sd.district_id
		  LEFT JOIN tm_cities c ON c.id = d.city_id
		  LEFT JOIN tm_provinces p ON p.id = c.province_id
		 WHERE sd.deleted_at IS NULL
		   AND sd.latitude IS NOT NULL AND sd.longitude IS NOT NULL
		UNION ALL
		SELECT 'district',
		       COALESCE(p.name, ''), COALESCE(c.name, ''), d.name, '',
		       COALESCE(d.postal_code, ''), d.latitude, d.longitude
		  FROM tm_districts d
		  LEFT JOIN tm_cities c ON c.id = d.city_id
		  LEFT JOIN tm_provinces p ON p.id = c.province_id
		 WHERE d.deleted_at IS NULL
		   AND d.latitude IS NOT NULL AND d.longitude IS NOT NULL
		UNION ALL
		SELECT 'city',
		       COALESCE(p.name, ''), c.name, '', '', '',
		       c.latitude, c.longitude
		  FROM tm_cities c
		  LEFT JOIN tm_provinces p ON p.id = c.province_id
		 WHERE c.deleted_at IS NULL
		   AND c.latitude IS NOT NULL AND c.longitude IS NOT NULL
		UNION ALL
		SELECT 'province',
		       p.name, '', '', '', '',
		       p.latitude, p.longitude
		  FROM tm_provinces p
		 WHERE p.deleted_at IS NULL
		   AND p.latitude IS NOT NULL AND p.longitude IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: region centroids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]geo.Region, 0, 512)
	for rows.Next() {
		var r geo.Region
		if err := rows.Scan(&r.Level, &r.Province, &r.City, &r.District, &r.Subdistrict,
			&r.PostalCode, &r.Point.Lat, &r.Point.Lon); err != nil {
			return nil, fmt.Errorf("store: scan region centroid: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: region centroid rows: %w", err)
	}
	return out, nil
}

// Compile-time guarantee that the production store satisfies RegionStore.
var _ RegionStore = (*PostgresStore)(nil)
