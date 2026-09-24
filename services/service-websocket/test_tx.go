package main
import (
	"context"
	"fmt"
	"backend/internal/config"
	"backend/internal/dbclient"
)
func main() {
	config.LoadConfig()
	dbclient.InitDB(config.AppConfig)
	ctx := context.Background()

	tx, err := dbclient.Pool.Begin(ctx)
	if err != nil { panic(err) }
	defer tx.Rollback(ctx)

	var newUserID int
	err = tx.QueryRow(ctx, "SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'test@test.local'").Scan(&newUserID)
	fmt.Println("First SELECT err:", err) // should be nil because we inserted it earlier

	var existingAccess int
	errCheck := tx.QueryRow(ctx, "SELECT 1 FROM adatrack_gps_default.tm_user_company_access WHERE user_id = 99999").Scan(&existingAccess)
	fmt.Println("errCheck:", errCheck) // should be no rows

	_, err = tx.Exec(ctx, "INSERT INTO adatrack_gps_default.tm_user_company_access (user_id, role_code, is_active) VALUES ($1, 'TEST', true)", newUserID)
	fmt.Println("INSERT err:", err)

	tx.Commit(ctx)
}
