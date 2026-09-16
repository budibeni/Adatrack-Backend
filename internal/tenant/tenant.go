package tenant

import (
	"context"
	"fmt"
	"ajb_gps/internal/dbclient"
)

func ResolveSchemaByIMEI(ctx context.Context, imei string) (string, error) {
	var companyCode string
	query := `
		SELECT c.code 
		FROM adatrack_gps_master.tm_user_vehicles uv
		JOIN adatrack_gps_master.tm_users u ON uv.user_id = u.id
		JOIN adatrack_gps_master.tm_companies c ON u.company_id = c.id
		JOIN adatrack_gps_master.tm_vehicles v ON uv.vehicle_id = v.id
		WHERE v.imei = $1 LIMIT 1
	`
	err := dbclient.Pool.QueryRow(ctx, query, imei).Scan(&companyCode)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("adatrack_gps_%s", companyCode), nil
}
