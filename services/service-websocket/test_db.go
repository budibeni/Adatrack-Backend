package main

import (
	"context"
	"fmt"
	"time"
	"backend/internal/dbclient"
	"backend/internal/config"
)

func main() {
	cfg := config.LoadConfig()
	dbclient.InitDB(cfg)
	defer dbclient.CloseDB()
	
	ctx := context.Background()
	rows, err := dbclient.Pool.Query(ctx, "SELECT id, email, is_active, created_at FROM adatrack_gps_master.tm_users WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT 50 OFFSET 0")
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var email string
		var isActive *bool
		var t *time.Time
		err := rows.Scan(&id, &email, &isActive, &t)
		fmt.Printf("id: %d, email: %s, err: %v\n", id, email, err)
	}
}
