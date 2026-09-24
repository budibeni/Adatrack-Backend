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

	// Ensure user exists
	dbclient.Pool.Exec(ctx, "INSERT INTO adatrack_gps_master.tm_users (id, email, password_hash) VALUES (8888, 'test8888@test.local', 'hash') ON CONFLICT DO NOTHING")
	// Ensure schema exists
	dbclient.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS adatrack_gps_test8888")
	dbclient.Pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS adatrack_gps_test8888.tm_user_company_access (user_id int, role_code varchar, is_active boolean default true, deleted_at timestamp)")

	tx, _ := dbclient.Pool.Begin(ctx)
	defer tx.Rollback(ctx)

	var existingAccess int
	errCheck := tx.QueryRow(ctx, "SELECT 1 FROM adatrack_gps_test8888.tm_user_company_access WHERE user_id = 8888").Scan(&existingAccess)
	fmt.Println("errCheck:", errCheck)

	_, err := tx.Exec(ctx, "INSERT INTO adatrack_gps_test8888.tm_user_company_access (user_id, role_code, is_active) VALUES (8888, 'ADMIN', true)")
	fmt.Println("Exec err:", err)

	err = tx.Commit(ctx)
	fmt.Println("Commit err:", err)
}
