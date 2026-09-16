package handler

import (
	"context"
	"fmt"

	"backend/internal/dbclient"
)

type TenantInfo struct {
	CompanyCode string
	VehicleID   int
}

// ResolveDevice validates the IMEI and fetches tenant context
func ResolveDevice(ctx context.Context, imei string) (TenantInfo, error) {
	var info TenantInfo
	query := `
		SELECT company_code, vehicle_id 
		FROM adatrack_gps_master.tm_vehicle_imei_map 
		WHERE imei = $1 LIMIT 1
	`
	err := dbclient.Pool.QueryRow(ctx, query, imei).Scan(&info.CompanyCode, &info.VehicleID)
	if err != nil {
		return TenantInfo{}, fmt.Errorf("device lookup failed for %s: %w", imei, err)
	}
	return info, nil
}
