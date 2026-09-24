package geo

import "testing"

// regionFixtures mirrors the shape of the master reference rows (city/province
// centroids — the levels the seed actually carries coordinates for).
func regionFixtures() []Region {
	return []Region{
		{Level: LevelCity, Province: "DKI Jakarta", City: "Kota Jakarta Pusat",
			PostalCode: "10110", Point: Point{Lat: -6.1805, Lon: 106.8284}},
		{Level: LevelCity, Province: "DKI Jakarta", City: "Kota Jakarta Selatan",
			Point: Point{Lat: -6.2615, Lon: 106.8106}},
		{Level: LevelCity, Province: "Jawa Barat", City: "Kota Bandung",
			Point: Point{Lat: -6.9175, Lon: 107.6191}},
		{Level: LevelProvince, Province: "Jawa Barat", Point: Point{Lat: -6.9147, Lon: 107.6098}},
		{Level: LevelProvince, Province: "DKI Jakarta", Point: Point{Lat: -6.1741, Lon: 106.8297}},
		{Level: LevelSubdistrict, Province: "X", City: "Y", District: "Z", Subdistrict: "NoCoord",
			Point: Point{}}, // (0,0) rows must be dropped
	}
}

// byLevel filters the fixtures down to one administrative level, mirroring how
// the geocoder keeps a "specific" index (city/district/subdistrict) apart from
// the coarse province fallback.
func byLevel(regions []Region, level string) []Region {
	out := make([]Region, 0, len(regions))
	for _, r := range regions {
		if r.Level == level {
			out = append(out, r)
		}
	}
	return out
}

// TestIndexNearestCity covers the happy path: Monas resolves to Central Jakarta.
func TestIndexNearestCity(t *testing.T) {
	fixtures := regionFixtures()
	idx := NewIndex(fixtures)
	if idx.Len() != 5 {
		t.Fatalf("index size = %d, want 5 (rows without coordinates are dropped)", idx.Len())
	}

	cities := NewIndex(byLevel(fixtures, LevelCity))
	region, dist, ok := cities.Nearest(-6.1754, 106.8272, 50) // Monas
	if !ok {
		t.Fatal("Monas must resolve to a region")
	}
	if region.City != "Kota Jakarta Pusat" {
		t.Fatalf("resolved city = %q, want Kota Jakarta Pusat", region.City)
	}
	if dist < 0 || dist > 5 {
		t.Fatalf("distance = %.3f km, want < 5 km", dist)
	}

	addr := region.Address(dist)
	if addr.String() != "Kota Jakarta Pusat, DKI Jakarta" {
		t.Fatalf("address = %q", addr.String())
	}
	if addr.Empty() || addr.Level != LevelCity {
		t.Fatalf("address metadata wrong: %+v", addr)
	}

	// The full index may return any level within range; the specificity policy
	// (city before province) belongs to the geocoder, which keeps the indexes
	// separate — here we only pin that *some* region is always found.
	if mixed, _, ok := idx.Nearest(-6.1754, 106.8272, 50); !ok || mixed.Level == "" {
		t.Fatal("the mixed index must still resolve a region")
	}
}

// TestIndexNearestRespectsMaxDistance documents the fallback boundary: outside
// the search radius nothing is returned (the geocoder then falls back to the
// province level or to "unknown").
func TestIndexNearestRespectsMaxDistance(t *testing.T) {
	idx := NewIndex(regionFixtures())
	if _, _, ok := idx.Nearest(-8.65, 115.2167, 20); ok { // Bali, 20 km radius
		t.Fatal("a coordinate 700 km away must not resolve within a 20 km radius")
	}
	if region, _, ok := idx.Nearest(-8.65, 115.2167, 0); !ok || region.Level == "" {
		t.Fatal("an unbounded search must still return the closest region")
	}
}

// TestIndexEmptyAndNoCoordinateRows covers the documented fallback: without
// reference coordinates the resolver reports "not found" instead of guessing.
func TestIndexEmptyAndNoCoordinateRows(t *testing.T) {
	for _, idx := range []*Index{NewIndex(nil), nil} {
		if _, _, ok := idx.Nearest(-6.2, 106.8, 50); ok {
			t.Error("an empty index must not resolve any coordinate")
		}
		if idx.Len() != 0 {
			t.Errorf("empty index length = %d, want 0", idx.Len())
		}
	}
}

// TestAddressRendering asserts the UI string keeps hierarchy order and skips
// empty levels.
func TestAddressRendering(t *testing.T) {
	full := Region{
		Level: LevelSubdistrict, Province: "Jawa Barat", City: "Kota Bandung",
		District: "Coblong", Subdistrict: "Dago",
	}
	if got := full.Address(1.5).String(); got != "Dago, Coblong, Kota Bandung, Jawa Barat" {
		t.Errorf("address = %q", got)
	}
	provinceOnly := Region{Level: LevelProvince, Province: "Banten"}
	if got := provinceOnly.Address(80).String(); got != "Banten" {
		t.Errorf("province-only address = %q", got)
	}
	if !(Address{}).Empty() {
		t.Error("a zero address must report Empty")
	}
}
