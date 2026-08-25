package main

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
)

// coord = latitude/longitude (decimal degrees).
type coord struct{ lat, lng float64 }

// GeoNames trimmed coords source TSV columns:
// 1 level|2 admin1|3 admin2|4 geonameid|5 name|6 lat|7 lng|8 alternateNames
func loadGeoCoords(path string) (adm1, adm2 map[string][]coord, err error) {
	adm1, adm2 = map[string][]coord{}, map[string][]coord{}
	if path == "" {
		return
	}
	f, e := os.Open(path)
	if e != nil {
		err = e
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "|")
		if len(p) < 8 {
			continue
		}
		lat, _ := strconv.ParseFloat(p[5], 64)
		lng, _ := strconv.ParseFloat(p[6], 64)
		c := coord{lat, lng}
		tokens := strings.Split(p[7], ",")
		tokens = append(tokens, p[4])
		switch p[0] {
		case "ADM1":
			for _, t := range tokens {
				if t != "" {
					adm1[slug(t)] = append(adm1[slug(t)], c)
				}
			}
		case "ADM2":
			for _, t := range tokens {
				if t != "" {
					adm2[slug(t)] = append(adm2[slug(t)], c)
					adm2[clean(t)] = append(adm2[clean(t)], c)
				}
			}
		}
	}
	err = sc.Err()
	return
}

// one returns coords only when exactly one candidate exists (unambiguous).
func one(m map[string][]coord, key string) (coord, bool) {
	v := m[key]
	if len(v) == 1 {
		return v[0], true
	}
	return coord{}, false
}

func centroid(cs []coord) (coord, bool) {
	if len(cs) == 0 {
		return coord{}, false
	}
	var la, ln float64
	for _, c := range cs {
		la += c.lat
		ln += c.lng
	}
	return coord{la / float64(len(cs)), ln / float64(len(cs))}, true
}

func fmtCoord(c coord, ok bool) string {
	if !ok || math.IsNaN(c.lat) || math.IsNaN(c.lng) {
		return "NULL, NULL"
	}
	return f64(c.lat) + ", " + f64(c.lng)
}
