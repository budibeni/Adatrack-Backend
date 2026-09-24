package geo

import (
	"math"
	"strings"
)

// Administrative levels of the master reference tables (PRD §6.1).
const (
	LevelProvince    = "province"
	LevelCity        = "city"
	LevelDistrict    = "district"
	LevelSubdistrict = "subdistrict"
)

// Region is one administrative area with a centroid, loaded from the master
// reference tables (`tm_provinces`→`tm_subdistricts`, B7.3). `Level` documents
// the specificity of `Point`: the seeded reference data currently carries
// coordinates for provinces and cities only, so a coordinate in Indonesia
// resolves at city (or province) granularity — see docs/B6-B7-VERIFICATION.md.
type Region struct {
	Level       string
	Province    string
	City        string
	District    string
	Subdistrict string
	PostalCode  string
	Point       Point
}

// Address is the offline reverse-geocoding result (B7.3).
type Address struct {
	Village    string `json:"village,omitempty"`
	District   string `json:"district,omitempty"`
	City       string `json:"city,omitempty"`
	Province   string `json:"province,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	// Level is the specificity actually resolved (province|city|district|subdistrict).
	Level string `json:"level"`
	// DistanceKM is the distance from the queried coordinate to the centroid of
	// the resolved region — callers can judge how precise the answer is.
	DistanceKM float64 `json:"distance_km"`
}

// String renders the address as "Village, District, City, Province" with the
// empty levels skipped (the UI shows exactly this string).
func (a Address) String() string {
	parts := make([]string, 0, 4)
	for _, p := range []string{a.Village, a.District, a.City, a.Province} {
		if v := strings.TrimSpace(p); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, ", ")
}

// Empty reports whether nothing could be resolved (the documented fallback).
func (a Address) Empty() bool { return a.String() == "" }

// Address projects the region row onto the response DTO.
func (r Region) Address(distanceKM float64) Address {
	return Address{
		Village:    r.Subdistrict,
		District:   r.District,
		City:       r.City,
		Province:   r.Province,
		PostalCode: r.PostalCode,
		Level:      r.Level,
		DistanceKM: distanceKM,
	}
}

// Index is an in-memory region lookup. Lookups are O(n) over the loaded rows
// with a cheap bounding-box prefilter; the geocoder caches results, so the scan
// never sits on the request hot path for repeated coordinates.
type Index struct {
	regions []Region
}

// NewIndex builds an index, dropping rows without a usable coordinate.
func NewIndex(regions []Region) *Index {
	kept := make([]Region, 0, len(regions))
	for _, r := range regions {
		if r.Point.IsNullIsland() {
			continue
		}
		kept = append(kept, r)
	}
	return &Index{regions: kept}
}

// Len reports how many rows are indexed.
func (i *Index) Len() int {
	if i == nil {
		return 0
	}
	return len(i.regions)
}

// Nearest returns the closest indexed region to the coordinate. `maxKM` bounds
// the search (0 = unbounded); the boolean is false when the index is empty or
// the closest row is farther than `maxKM` — the caller then falls back to a
// coarser level (or to "unknown").
func (i *Index) Nearest(lat, lon float64, maxKM float64) (Region, float64, bool) {
	if i == nil || len(i.regions) == 0 {
		return Region{}, 0, false
	}
	p := Point{Lat: lat, Lon: lon}

	// Bounding-box prefilter: a degree of latitude is ~111 km everywhere, a
	// degree of longitude shrinks with cos(lat) — the box is enlarged by 25% so
	// no true nearest row can be filtered out.
	if maxKM > 0 {
		latPad := maxKM / 111.0 * 1.25
		lonPad := maxKM / (111.0 * cosLat(lat)) * 1.25
		best := Region{}
		bestDist := maxKM
		found := false
		for _, r := range i.regions {
			if r.Point.Lat < lat-latPad || r.Point.Lat > lat+latPad ||
				r.Point.Lon < lon-lonPad || r.Point.Lon > lon+lonPad {
				continue
			}
			if d := DistanceKM(p, r.Point); d <= bestDist {
				best, bestDist, found = r, d, true
			}
		}
		if !found {
			return Region{}, 0, false
		}
		return best, bestDist, true
	}

	best := i.regions[0]
	bestDist := DistanceKM(p, best.Point)
	for _, r := range i.regions[1:] {
		if d := DistanceKM(p, r.Point); d < bestDist {
			best, bestDist = r, d
		}
	}
	return best, bestDist, true
}

// cosLat guards the longitude padding against the poles (never happens for the
// Indonesian reference data, but a division by zero must not be reachable).
func cosLat(lat float64) float64 {
	c := math.Cos(lat * DegToRad)
	if c < 0.05 {
		return 0.05
	}
	return c
}
