package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	pool, err := pgxpool.New(context.Background(), "postgres://adatrack:adatrack_dev_pw@localhost:5432/adatrack_gps_db?sslmode=disable")
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	rows, err := pool.Query(context.Background(), `SELECT id, company_code, actor_email, actor_role, action, detail, created_at FROM adatrack_gps_master.tm_global_audit_logs LIMIT 1`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var companyCode, action, createdAt string
		var actorEmail, actorRole, detail *string
		err := rows.Scan(&id, &companyCode, &actorEmail, &actorRole, &action, &detail, &createdAt)
		if err != nil {
			fmt.Println("SCAN ERROR:", err)
		} else {
			fmt.Println("SUCCESS!")
		}
	}
}
