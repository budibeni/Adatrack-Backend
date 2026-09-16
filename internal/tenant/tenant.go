package tenant

import (
	"context"
	"fmt"

	"backend/internal/dbclient"
)

// ResolveCompanyCodeByIMEI securely retrieves the tenant ID from the master db
func ResolveCompanyCodeByIMEI(ctx context.Context, imei string) (string, error) {
	var companyCode string
	query := `
		SELECT company_code 
		FROM adatrack_gps_master.tm_vehicle_imei_map
		WHERE imei = $1 LIMIT 1
	`
	// Using parameterized query to prevent SQL injection
	err := dbclient.Pool.QueryRow(ctx, query, imei).Scan(&companyCode)
	if err != nil {
		return "", fmt.Errorf("failed to resolve tenant for imei %s: %w", imei, err)
	}
	return companyCode, nil
}
