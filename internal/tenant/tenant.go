package tenant

import (
	"context"
	"fmt"
	"adatrack/internal/dbclient"
)

func ResolveCompanyCodeByIMEI(ctx context.Context, imei string) (string, error) {
	var companyCode string
	query := `
		SELECT company_code 
		FROM adatrack_gps_master.tm_vehicle_imei_map
		WHERE imei = $1 LIMIT 1
	`
	err := dbclient.Pool.QueryRow(ctx, query, imei).Scan(&companyCode)
	if err != nil {
		return "", err
	}
	return companyCode, nil
}
