package geo

import (
	"math"
	"testing"
)

// TestReduceIndicesStraightLine asserts collinear points collapse to the two
// endpoints (playback must not lose the route shape, only its redundancy).
func TestReduceIndicesStraightLine(t *testing.T) {
	points := []Point{
		{Lat: -6.2000, Lon: 106.8000},
		{Lat: -6.2001, Lon: 106.8010},
		{Lat: -6.2002, Lon: 106.8020},
		{Lat: -6.2003, Lon: 106.8030},
	}
	idx := ReduceIndices(points, 25)
	if len(idx) != 2 || idx[0] != 0 || idx[1] != 3 {
		t.Fatalf("collinear points reduced to %v, want [0 3]", idx)
	}
	if got := Reduce(points, 25); len(got) != 2 {
		t.Fatalf("Reduce returned %d points, want 2", len(got))
	}
}

// TestReduceIndicesKeepsApex asserts a genuine detour survives the reduction —
// the acceptance criterion of B7.4 ("tanpa kehilangan bentuk rute").
func TestReduceIndicesKeepsApex(t *testing.T) {
	points := []Point{
		{Lat: -6.2000, Lon: 106.8000},
		{Lat: -6.2002, Lon: 106.8010},
		// ~440 m north of the straight line between the endpoints.
		{Lat: -6.1960, Lon: 106.8020},
		{Lat: -6.2004, Lon: 106.8030},
		{Lat: -6.2006, Lon: 106.8040},
	}
	idx := ReduceIndices(points, 25)
	if len(idx) < 3 {
		t.Fatalf("reduction dropped the detour: kept %v", idx)
	}
	if idx[0] != 0 || idx[len(idx)-1] != len(points)-1 {
		t.Fatalf("endpoints must always be kept, got %v", idx)
	}
	found := false
	for _, i := range idx {
		if i == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the apex (index 2) is outside the tolerance and must be kept: %v", idx)
	}
}

// TestReduceIndicesBoundaries documents the degenerate inputs.
func TestReduceIndicesBoundaries(t *testing.T) {
	if got := ReduceIndices(nil, 10); got != nil {
		t.Errorf("nil input = %v, want nil", got)
	}
	one := []Point{{Lat: -6.2, Lon: 106.8}}
	if got := ReduceIndices(one, 10); len(got) != 1 || got[0] != 0 {
		t.Errorf("single point = %v, want [0]", got)
	}

	poly := []Point{{Lat: -6.2000, Lon: 106.8000}, {Lat: -6.2005, Lon: 106.8050}}
	if got := ReduceIndices(poly, 0); len(got) != 2 {
		t.Errorf("tolerance 0 must disable reduction, got %v", got)
	}
	if got := ReduceIndices(poly, 100); len(got) != 2 {
		t.Errorf("two points are always both kept, got %v", got)
	}
}

// TestReducePercentagesStaysMonotonic guards the property used by the playback
// response (a larger tolerance can never keep MORE points).
func TestReducePercentagesStaysMonotonic(t *testing.T) {
	points := make([]Point, 0, 200)
	for i := 0; i < 200; i++ {
		// A wiggly route: monotonic east, sinusoidal north offset.
		points = append(points, Point{
			Lat: -6.2 + 0.0008*math.Sin(float64(i)/6),
			Lon: 106.8 + float64(i)*0.0004,
		})
	}
	prev := len(points) + 1
	for _, tol := range []float64{5, 15, 40, 120} {
		kept := len(ReduceIndices(points, tol))
		if kept > prev {
			t.Fatalf("tolerance %.0f kept %d points, more than the previous %d", tol, kept, prev)
		}
		if kept < 2 {
			t.Fatalf("tolerance %.0f kept only %d points; endpoints are mandatory", tol, kept)
		}
		prev = kept
	}
}

// TestPerpendicularMeters checks the point-to-segment measure on a known
// segment: 0.001° of latitude is ~110.5 m.
func TestPerpendicularMeters(t *testing.T) {
	a := Point{Lat: -6.2000, Lon: 106.8000}
	b := Point{Lat: -6.2000, Lon: 106.8100} // due east
	p := Point{Lat: -6.2010, Lon: 106.8050}

	if got := PerpendicularMeters(p, a, b); math.Abs(got-110.5) > 3 {
		t.Errorf("perpendicular distance = %.2f m, want ≈110.5 m", got)
	}
	// A point beyond the segment end clamps to the endpoint distance.
	far := Point{Lat: -6.2000, Lon: 106.8200}
	if got := PerpendicularMeters(far, a, b); math.Abs(got-1110) > 40 {
		t.Errorf("clamped distance = %.1f m, want ≈1110 m", got)
	}
	// A degenerate segment (both ends equal) falls back to the point distance.
	if got := PerpendicularMeters(p, a, a); got <= 0 {
		t.Errorf("degenerate segment distance = %.2f, want > 0", got)
	}
}
