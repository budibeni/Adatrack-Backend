package geo

import "math"

// ReduceIndices runs the Ramer–Douglas–Peucker algorithm (B7.4) over `points`
// and returns the indices that must be kept, in ascending order. The first and
// the last point are always kept, so the route shape (endpoints) never changes.
//
// `toleranceM` is the maximum allowed perpendicular deviation in metres:
//   - toleranceM <= 0 disables reduction (every index is kept),
//   - fewer than 3 points are returned unchanged.
//
// The implementation is iterative (explicit stack) so a long history cannot
// exhaust the goroutine stack.
func ReduceIndices(points []Point, toleranceM float64) []int {
	if len(points) == 0 {
		return nil
	}
	all := make([]int, len(points))
	for i := range points {
		all[i] = i
	}
	if toleranceM <= 0 || len(points) < 3 {
		return all
	}

	keep := make([]bool, len(points))
	keep[0] = true
	keep[len(points)-1] = true

	type span struct{ first, last int }
	stack := []span{{0, len(points) - 1}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s.last <= s.first+1 {
			continue
		}
		worst, worstIdx := 0.0, -1
		for i := s.first + 1; i < s.last; i++ {
			d := PerpendicularMeters(points[i], points[s.first], points[s.last])
			if d > worst {
				worst, worstIdx = d, i
			}
		}
		if worstIdx < 0 || worst <= toleranceM {
			continue
		}
		keep[worstIdx] = true
		stack = append(stack, span{s.first, worstIdx}, span{worstIdx, s.last})
	}

	out := make([]int, 0, len(points))
	for i, k := range keep {
		if k {
			out = append(out, i)
		}
	}
	return out
}

// Reduce applies ReduceIndices to the coordinate slice (convenience for callers
// that do not need the surviving indices).
func Reduce(points []Point, toleranceM float64) []Point {
	idx := ReduceIndices(points, toleranceM)
	out := make([]Point, 0, len(idx))
	for _, i := range idx {
		out = append(out, points[i])
	}
	return out
}

// PerpendicularMeters returns the distance in metres from `p` to the segment
// `a`→`b`. Both the cross-product test (distance to the infinite line) and the
// clamped endpoint distances are evaluated, which is the standard "point to
// segment" measure used by RDP implementations.
//
// The projection is equirectangular around `a` (metres per degree), which is
// accurate to well below the playback tolerance for segment lengths of a few
// kilometres.
func PerpendicularMeters(p, a, b Point) float64 {
	ax, ay := 0.0, 0.0
	bx, by := projectMeters(a, b)
	px, py := projectMeters(a, p)

	dx, dy := bx-ax, by-ay
	if dx == 0 && dy == 0 {
		return math.Hypot(px, py)
	}
	t := (px*dx + py*dy) / (dx*dx + dy*dy)
	if t < 0 {
		return math.Hypot(px, py)
	}
	if t > 1 {
		return math.Hypot(px-bx, py-by)
	}
	return math.Abs(dy*px-dx*py) / math.Hypot(dx, dy)
}

// projectMeters maps `p` onto a local metre plane anchored at `origin` (Web
// Mercator-free planar approximation; degree lengths are the standard geodesic
// values at the anchor latitude).
func projectMeters(origin, p Point) (x, y float64) {
	const metersPerDegLat = 110540.0
	metersPerDegLon := 111320.0 * math.Cos(origin.Lat*DegToRad)
	return (p.Lon - origin.Lon) * metersPerDegLon, (p.Lat - origin.Lat) * metersPerDegLat
}
