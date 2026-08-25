package main

import (
	"sort"
	"strings"
)

// cityInfo: satu kota/kabupaten + koordinat GeoNames (jika unik).
type cityInfo struct {
	code, provCode, name string
	coord                coord
	has                  bool
}

// disInfo: satu kecamatan + kode kota induknya.
type disInfo struct {
	code, cityCode, name string
}

// buildCities: baca regencies.csv → city list + kumpulan koordinat per provinsi
// untuk centroid provinsi.
func buildCities(regRows [][]string, adm2g map[string][]coord) (cities []cityInfo, childByProv map[string][]coord) {
	childByProv = map[string][]coord{}
	for _, r := range regRows[1:] {
		if len(r) < 3 || r[0] == "" {
			continue
		}
		ci := cityInfo{code: r[0], provCode: r[1], name: r[2]}
		c, ok := one(adm2g, slug(r[2]))
		if !ok {
			c, ok = one(adm2g, clean(r[2])) // fallback tanpa prefiks Kabupaten/Kota
		}
		if ok {
			ci.coord = c
			ci.has = true
			childByProv[r[1]] = append(childByProv[r[1]], c)
		}
		cities = append(cities, ci)
	}
	return
}

func computeProvCoords(provByName map[string]string, childByProv map[string][]coord) map[string]coord {
	m := map[string]coord{}
	for code := range provByName {
		if len(childByProv[code]) > 0 {
			m[code], _ = centroid(childByProv[code])
		}
	}
	return m
}

func writeProvinces(provByName map[string]string, provCoord map[string]coord, outDir, iso string) {
	var rows [][]string
	for _, code := range sortKeys(provByName) {
		cc := fmtCoord(provCoord[code], provCoord[code] != coord{})
		rows = append(rows, []string{
			"(SELECT id FROM countries WHERE iso_code = '" + esc(iso) + "')",
			"'" + esc(code) + "'",
			"'" + esc(provByName[code]) + "'",
			cc,
		})
	}
	body := batchInsert("provinces",
		[]string{"country_id", "code", "name", "latitude", "longitude"}, rows,
		"name=VALUES(name), latitude=VALUES(latitude), longitude=VALUES(longitude)")
	writeOut(outDir, "002_provinces.sql",
		"MASTER reference 002 — provinces (BPS Kemendagri, ID only)",
		"fityannugroho/idn-area-data provinces.csv + geonames ADM2 centroid coords", body)
}

func writeCities(cities []cityInfo, outDir, iso string) {
	sort.Slice(cities, func(i, j int) bool { return cities[i].code < cities[j].code })
	var rows [][]string
	for _, ci := range cities {
		cc := fmtCoord(ci.coord, ci.has)
		rows = append(rows, []string{
			"(SELECT id FROM countries WHERE iso_code = '" + esc(iso) + "')",
			"(SELECT id FROM provinces WHERE code = '" + esc(ci.provCode) + "')",
			"'" + esc(ci.code) + "'",
			"'" + esc(ci.name) + "'",
			cc,
		})
	}
	body := batchInsert("cities",
		[]string{"country_id", "province_id", "code", "name", "latitude", "longitude"}, rows,
		"name=VALUES(name), latitude=VALUES(latitude), longitude=VALUES(longitude)")
	writeOut(outDir, "003_cities.sql",
		"MASTER reference 003 — cities / kabupaten-kota (BPS)",
		"fityannugroho/idn-area-data regencies.csv + geonames ADM2 coords", body)
}

func buildDistricts(idnDir string, cityCodeSet map[string]bool) []disInfo {
	rows, _ := readCSV(join(idnDir, "districts.csv"))
	var list []disInfo
	for _, r := range rows[1:] {
		if len(r) < 3 || r[0] == "" {
			continue
		}
		parts := strings.Split(r[0], ".")
		if len(parts) < 2 {
			continue
		}
		cityCode := parts[0] + "." + parts[1]
		if !cityCodeSet[cityCode] {
			continue
		}
		list = append(list, disInfo{code: r[0], cityCode: cityCode, name: r[2]})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].code < list[j].code })
	return list
}

func writeDistricts(list []disInfo, outDir string) {
	var rows [][]string
	for _, d := range list {
		rows = append(rows, []string{
			"(SELECT id FROM cities WHERE code = '" + esc(d.cityCode) + "')",
			"'" + esc(d.code) + "'",
			"'" + esc(d.name) + "'",
			"NULL",
			"NULL, NULL",
		})
	}
	body := batchInsert("districts",
		[]string{"city_id", "code", "name", "postal_code", "latitude", "longitude"}, rows, "name=VALUES(name)")
	writeOut(outDir, "004_districts.sql",
		"MASTER reference 004 — districts / kecamatan (BPS)",
		"fityannugroho/idn-area-data districts.csv", body)
}

func writeSuburbs(idnDir string, disList []disInfo, provByName, regNameByCode, disNameByCode, postMap map[string]string, outDir string) (nSub, nPost int) {
	disSet := map[string]bool{}
	for _, d := range disList {
		disSet[d.code] = true
	}
	rows, _ := readCSV(join(idnDir, "villages.csv"))
	type vrow struct{ code, disCode, name, postal string }
	var vs []vrow
	for _, r := range rows[1:] {
		if len(r) < 3 || r[0] == "" || !disSet[r[1]] {
			continue
		}
		parts := strings.Split(r[1], ".")
		provCode := parts[0]
		cityCode := parts[0] + "." + parts[1]
		tup := clean(provByName[provCode]) + "|" + clean(regNameByCode[cityCode]) + "|" + clean(disNameByCode[r[1]]) + "|" + clean(r[2])
		p, ok := postMap[tup]
		if ok && p != "" {
			nPost++
		}
		vs = append(vs, vrow{code: r[0], disCode: r[1], name: r[2], postal: p})
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].code < vs[j].code })

	var rows2 [][]string
	for _, v := range vs {
		rows2 = append(rows2, []string{
			"(SELECT id FROM districts WHERE code = '" + esc(v.disCode) + "')",
			"'" + esc(v.code) + "'",
			"'" + esc(v.name) + "'",
			sqlStr(v.postal),
			"NULL, NULL",
		})
	}
	body := batchInsert("subdistricts",
		[]string{"district_id", "code", "name", "postal_code", "latitude", "longitude"}, rows2,
		"name=VALUES(name), postal_code=VALUES(postal_code)")
	writeOut(outDir, "005_subdistricts.sql",
		"MASTER reference 005 — subdistricts / desa-kelurahan (BPS + kodepos best-effort)",
		"fityannugroho/idn-area-data villages.csv + cahyadsn/wilayah_kodepos", body)
	return len(vs), nPost
}
