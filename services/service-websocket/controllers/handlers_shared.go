package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// paginationParams parses page/limit (default 1/100, max 500).
func paginationParams(c *gin.Context) (page, limit int) {
	page = atoiDefault(c.Query("page"), 1)
	limit = atoiDefault(c.Query("limit"), 100)
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return page, limit
}

// atoiDefault parses an int with a fallback default.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// accessibleVehicleIDsFromCtx returns the caller's allowed vehicle ID map.
func accessibleVehicleIDsFromCtx(c *gin.Context) map[uint64]struct{} {
	v, _ := c.Get(ctxAllowedKey)
	allowed, _ := v.(map[uint64]struct{})
	return allowed
}

// companyCodeFromCtx returns the caller's company_code.
func companyCodeFromCtx(c *gin.Context) string {
	v, _ := c.Get(ctxCompanyCodeKey)
	code, _ := v.(string)
	return code
}

// placeholders builds a comma-separated list of N "?" placeholders.
func placeholders(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += "?"
	}
	return s
}

// mapKeys extracts keys from a map[uint64]struct{} as interface{} slice.
func mapKeys(m map[uint64]struct{}) []interface{} {
	out := make([]interface{}, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// nullableStr converts an empty string to a SQL NULL.
func nullableStr(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// nullableStrP converts a SQL NULL string to a *string zero value.
func nullableStrP(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func nullableFloat(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func nullableTimeP(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

func nullableUint(n sql.NullInt64) *uint64 {
	if !n.Valid {
		return nil
	}
	v := uint64(n.Int64)
	return &v
}

// enrichVehicles converts vehicleModel rows to response items and overlays
// live Redis state (adatrack_gps:{company}:vehicle:state:<IMEI>) with a single
// batched MGET.
func enrichVehicles(c *gin.Context, vehicles []vehicleModel) []models.VehicleListItem {
	items := make([]models.VehicleListItem, 0, len(vehicles))
	if len(vehicles) == 0 {
		return items
	}
	company := companyCodeFromCtx(c)

	keys := make([]string, 0, len(vehicles))
	for _, v := range vehicles {
		keys = append(keys, redisVehicleStateKey(company, v.IMEI))
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	var live map[string]models.RedisState
	if res := appRedis.Client().MGet(ctx, keys...); res.Err() == nil {
		vals := res.Val()
		live = make(map[string]models.RedisState, len(vehicles))
		for i, v := range vehicles {
			if i < len(vals) {
				if s, ok := vals[i].(string); ok && s != "" {
					var st models.RedisState
					if err := json.Unmarshal([]byte(s), &st); err == nil {
						live[v.IMEI] = st
					}
				}
			}
		}
	}

	for _, v := range vehicles {
		item := models.VehicleListItem{
			ID:          v.ID,
			IMEI:        v.IMEI,
			PlateNumber: v.PlateNumber,
			Status:      v.Status,
		}
		if v.DeviceModel.Valid {
			item.DeviceModel = v.DeviceModel.String
		}

		pos := &models.LastPos{}
		if st, ok := live[v.IMEI]; ok {
			pos.Lat = st.Lat
			pos.Lon = st.Lon
			pos.Speed = st.Speed
			if st.LastSeen > 0 {
				ts := time.Unix(st.LastSeen, 0)
				pos.Timestamp = &ts
			}
			if item.Status == "active" || item.Status == "inactive" || item.Status == "maintenance" {
				item.Status = st.Status // ONLINE/IDLE/OFFLINE dari live
			}
		} else {
			if v.CurrentLat.Valid && v.CurrentLon.Valid {
				pos.Lat = v.CurrentLat.Float64
				pos.Lon = v.CurrentLon.Float64
				if v.CurrentSpeed.Valid {
					pos.Speed = v.CurrentSpeed.Float64
				}
				if v.LastSeenAt.Valid {
					ts := v.LastSeenAt.Time
					pos.Timestamp = &ts
				}
			}
		}
		if pos.Timestamp == nil && pos.Lat == 0 && pos.Lon == 0 {
			item.LastPosition = nil
		} else {
			item.LastPosition = pos
		}
		items = append(items, item)
	}
	return items
}

// loadAuthUserID is a small wrapper used by handlers when ctx is available.
func loadAuthUserID(c *gin.Context) uint64 {
	u, ok := loadAuthUser(c)
	if !ok {
		return 0
	}
	return u.ID
}
