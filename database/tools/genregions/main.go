package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	countriesP := flag.String("countries", "", "mledoze countries.json (required)")
	idnDir := flag.String("idn-dir", "", "dir with provinces/regencies/districts/villages CSV (required)")
	geoP := flag.String("geonames-coords", "", "trimmed geonames coords TSV ADM1/ADM2 (required)")
	postalP := flag.String("postal", "", "pos_data.csv kodepos (optional)")
	outDir := flag.String("out", "", "output dir for seed/reference/*.sql (required)")
	countryIso := flag.String("country-iso", "ID", "ISO alpha-2 of country owning provinces")
	flag.Parse()
	if *countriesP == "" || *idnDir == "" || *geoP == "" || *outDir == "" {
		flag.Usage()
		os.Exit(2)
	}
	must(os.MkdirAll(*outDir, 0755))

	clist, err := loadCountries(*countriesP)
	must(err)
	nC := writeCountries(clist, *outDir)

	_, adm2g, _ := loadGeoCoords(*geoP) // adm1g derived via province centroid of cities instead

	provCSV, _ := readCSV(join(*idnDir, "provinces.csv"))
	provByName := map[string]string{}
	for _, r := range provCSV[1:] {
		if len(r) >= 2 && r[0] != "" {
			provByName[r[0]] = r[1]
		}
	}

	regRows, _ := readCSV(join(*idnDir, "regencies.csv"))
	cities, childByProv := buildCities(regRows, adm2g)
	regNameByCode := map[string]string{}
	for _, r := range regRows[1:] {
		if len(r) >= 3 && r[0] != "" {
			regNameByCode[r[0]] = r[2]
		}
	}

	provCoord := computeProvCoords(provByName, childByProv)
	writeProvinces(provByName, provCoord, *outDir, *countryIso)

	cityCodeSet := map[string]bool{}
	for _, ci := range cities {
		cityCodeSet[ci.code] = true
	}
	writeCities(cities, *outDir, *countryIso)

	disList := buildDistricts(*idnDir, cityCodeSet)
	writeDistricts(disList, *outDir)

	postMap, _ := loadPostal(*postalP)
	disNameByCode := map[string]string{}
	for _, d := range disList {
		disNameByCode[d.code] = d.name
	}
	nSub, nPost := writeSuburbs(*idnDir, disList, provByName, regNameByCode, disNameByCode, postMap, *outDir)

	fmt.Fprintf(os.Stderr,
		"[genregions] countries=%d provinces=%d cities=%d districts=%d subdistricts=%d (postal matched=%d)\n",
		nC, len(provByName), len(cities), len(disList), nSub, nPost)
}

func join(a, b string) string { return strings.TrimRight(a, "/") + "/" + b }
