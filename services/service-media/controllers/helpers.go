package controllers

import (
	"database/sql"
	"sort"
	"strings"
	"time"
)

// joinPlaceholders joins placeholder tokens ("?,?,?").
func joinPlaceholders(ph []string) string { return strings.Join(ph, ",") }

// joinConditions joins SQL WHERE conditions with " AND ".
func joinConditions(conds []string) string { return strings.Join(conds, " AND ") }

// sortedKeys returns vehicle IDs from a scope set in ascending order (stable
// SQL argument order for the vehicle_id IN (...) filter).
func sortedKeys(m map[uint64]struct{}) []uint64 {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(v *int) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

// normalizeMeta returns nil for empty JSON so the DB column stays NULL.
func normalizeMeta(b []byte) (interface{}, error) {
	if len(b) == 0 || strings.TrimSpace(string(b)) == "null" {
		return nil, nil
	}
	return b, nil
}

// paginationParams reads ?page=&limit= with sane bounds.
func paginationParams(c interface{ Query(string) string }) (int, int) {
	page := atoiDefault(c.Query("page"), 1)
	limit := atoiDefault(c.Query("limit"), 50)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 50
	}
	return page, limit
}

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return def
		}
		n = n*10 + int(s[i]-'0')
	}
	if n == 0 && s != "0" {
		return def
	}
	return n
}

// nullableTimeP converts sql.NullTime into a *time.Time DTO field.
func nullableTimeP(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}
