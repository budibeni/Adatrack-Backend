package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v4/pgxpool"
)

func main() {
	ctx := context.Background()
	connStr := "postgres://adatrack_gps_user:adatrack_gps_password@localhost:5432/adatrack_gps_master"
	pool, err := pgxpool.Connect(ctx, connStr)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	pool.Exec(ctx, "INSERT INTO adatrack_gps_master.tm_users (id, email, password_hash) VALUES (8888, 'test8888@test.local', 'hash') ON CONFLICT DO NOTHING")
	pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS adatrack_gps_test8888")
	pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS adatrack_gps_test8888.tm_user_company_access (user_id int, role_code varchar, is_active boolean default true, deleted_at timestamp)")

	tx, _ := pool.Begin(ctx)
	defer tx.Rollback(ctx)

	var existingAccess int
	errCheck := tx.QueryRow(ctx, "SELECT 1 FROM adatrack_gps_test8888.tm_user_company_access WHERE user_id = 8888").Scan(&existingAccess)
	fmt.Println("errCheck:", errCheck)

	_, err = tx.Exec(ctx, "INSERT INTO adatrack_gps_test8888.tm_user_company_access (user_id, role_code, is_active) VALUES (8888, 'ADMIN', true)")
	fmt.Println("Exec err:", err)

	err = tx.Commit(ctx)
	fmt.Println("Commit err:", err)
}
