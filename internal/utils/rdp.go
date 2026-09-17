package utils

import "math"

type Point struct {
	X float64
	Y float64
}

// PerpendicularDistance calculates the distance of a point (x, y) from a line segment defined by p1 and p2.
func PerpendicularDistance(p Point, p1 Point, p2 Point) float64 {
	// If p1 and p2 are the same point, return the distance to that point
	if p1.X == p2.X && p1.Y == p2.Y {
		return math.Sqrt(math.Pow(p.X-p1.X, 2) + math.Pow(p.Y-p1.Y, 2))
	}

	num := math.Abs((p2.Y-p1.Y)*p.X - (p2.X-p1.X)*p.Y + p2.X*p1.Y - p2.Y*p1.X)
	den := math.Sqrt(math.Pow(p2.Y-p1.Y, 2) + math.Pow(p2.X-p1.X, 2))
	return num / den
}

// RamerDouglasPeucker reduces a series of points using the RDP algorithm.
func RamerDouglasPeucker(points []Point, epsilon float64) []Point {
	if len(points) < 3 {
		return points
	}

	dmax := 0.0
	index := 0
	end := len(points) - 1

	for i := 1; i < end; i++ {
		d := PerpendicularDistance(points[i], points[0], points[end])
		if d > dmax {
			index = i
			dmax = d
		}
	}

	var results []Point
	if dmax > epsilon {
		// Recursive call
		recResults1 := RamerDouglasPeucker(points[:index+1], epsilon)
		recResults2 := RamerDouglasPeucker(points[index:], epsilon)

		results = append(results, recResults1[:len(recResults1)-1]...)
		results = append(results, recResults2...)
	} else {
		results = []Point{points[0], points[end]}
	}

	return results
}
