package main

import (
	"bufio"
	"os"
)

// postal: file pos_data.csv pipe-delimited (kodepos|kelurahan|kecamatan|kabupaten|provinsi).
// Returns map keyed by slug(provinsi|kabupaten|kecamatan|kelurahan) → postal (first wins).
func loadPostal(path string) (map[string]string, error) {
	m := map[string]string{}
	if path == "" {
		return m, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := splitPipe(sc.Text())
		if len(p) < 5 {
			continue
		}
		postal := trimSp(p[0])
		if postal == "" {
			continue
		}
		tup := clean(p[4]) + "|" + clean(p[3]) + "|" + clean(p[2]) + "|" + clean(p[1])
		if tup != "" {
			m[tup] = postal
		}
	}
	return m, sc.Err()
}
