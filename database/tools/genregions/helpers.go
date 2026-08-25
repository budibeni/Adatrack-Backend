package main

import (
	"sort"
	"strconv"
	"strings"
)

func slug(s string) string {
	s = strings.ToLower(s)
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		}
	}
	return string(b)
}

func trimSp(s string) string { return strings.TrimSpace(s) }

func splitPipe(s string) []string { return strings.Split(s, "|") }

var stopTokens = map[string]bool{
	"kabupaten": true, "kota": true, "kab": true, "kecamatan": true, "kelurahan": true,
	"desa": true, "provinsi": true, "prov": true, "negara": true, "bagian": true,
	"daerah": true, "istimewa": true,
}

// clean menghasilkan kunci pencocokan normal: lowercased, tanpa prefiks
// administratif (kabupaten/kota/kecamatan/desa/...), alnum saja (slug).
func clean(s string) string {
	s = strings.ToLower(s)
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if !stopTokens[p] {
			out = append(out, p)
		}
	}
	return slug(strings.Join(out, " "))
}

func esc(s string) string { // MySQL string literal (single-quote doubling)
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

func sqlStr(s string) string {
	if s == "" {
		return "NULL"
	}
	return "'" + esc(s) + "'"
}

func sortKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func must(err error) {
	if err != nil {
		panic("FATAL: " + err.Error())
	}
}

func f64(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
