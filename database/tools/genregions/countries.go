package main

import (
	"encoding/json"
	"os"
	"sort"
)

// Country mirrors mledoze/countries (subset) — ISO 3166-1 reference.
type Country struct {
	Cca2 string                  `json:"cca2"`
	Cca3 string                  `json:"cca3"`
	Name struct{ Common string } `json:"name"`
	Idd  struct {
		Root     string
		Suffixes []string
	} `json:"idd"`
	Currencies map[string]struct{ Symbol, Name string } `json:"currencies"`
	Status     string                                   `json:"status"`
}

func loadCountries(path string) ([]Country, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []Country
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func writeCountries(list []Country, outDir string) int {
	sort.Slice(list, func(i, j int) bool { return list[i].Cca2 < list[j].Cca2 })
	var rows [][]string
	for _, c := range list {
		phone := c.Idd.Root // mledoze root sudah termasuk "+" (mis. "+6")
		if phone == "" {
			if len(c.Idd.Suffixes) > 0 && c.Idd.Suffixes[0] != "" {
				phone = "+" + c.Idd.Suffixes[0]
			}
		} else if len(c.Idd.Suffixes) > 0 && c.Idd.Suffixes[0] != "" {
			// append first suffix bila masih membentuk kode negara (digit total ≤ 3),
			// bukan area code (NANP: US "+1" + "201" dst).
			rootDigits := len(phone) - 1 // potong "+"
			if rootDigits+len(c.Idd.Suffixes[0]) <= 3 {
				phone += c.Idd.Suffixes[0]
			}
		}
		currency := ""
		if len(c.Currencies) > 0 {
			ks := make([]string, 0, len(c.Currencies))
			for k := range c.Currencies {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			currency = ks[0]
		}
		active := "TRUE"
		if c.Status != "officially-assigned" {
			active = "FALSE"
		}
		rows = append(rows, []string{
			"'" + esc(c.Cca2) + "'",
			"'" + esc(c.Cca3) + "'",
			"'" + esc(c.Name.Common) + "'",
			"'" + esc(phone) + "'",
			"'" + esc(currency) + "'",
			active,
		})
	}
	body := batchInsert("countries",
		[]string{"iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"},
		rows,
		"name=VALUES(name), phone_code=VALUES(phone_code), currency_code=VALUES(currency_code), is_active=VALUES(is_active)")
	writeOut(outDir, "001_countries.sql",
		"MASTER reference 001 — countries (ISO 3166-1, mledoze)",
		"https://raw.githubusercontent.com/mledoze/countries/master/countries.json", body)
	return len(rows)
}
