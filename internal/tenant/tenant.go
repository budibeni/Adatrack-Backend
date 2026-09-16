package tenant
import (
	"context"
	"fmt"
	"backend/internal/dbclient"
)
func ResolveCompanyCodeByIMEI(ctx context.Context, imei string) (string, error) {
	var code string
	err := dbclient.Pool.QueryRow(ctx, "SELECT company_code FROM adatrack_gps_master.tm_vehicle_imei_map WHERE imei = $1 LIMIT 1", imei).Scan(&code)
	if err != nil { return "", fmt.Errorf("imei %s not found: %w", imei, err) }
	return code, nil
}
