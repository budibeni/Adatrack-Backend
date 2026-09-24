package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"backend/internal/config"
	"backend/internal/dbclient"
)

type UserInfo struct {
	ID         int      `json:"id"`
	Email      string   `json:"email"`
	IsActive   bool     `json:"is_active"`
	CreatedAt  string   `json:"created_at"`
	GlobalRole string   `json:"global_role"`
	Tenants    []string `json:"tenants"`
}

func main() {
	config.LoadConfig()
	dbclient.InitDB(config.AppConfig)
	ctx := context.Background()

	// Add dummy user to master
	dbclient.Pool.Exec(ctx, "INSERT INTO adatrack_gps_master.tm_users (id, email, password_hash) VALUES (9999, 'test@test.local', 'hash') ON CONFLICT DO NOTHING")
	dbclient.Pool.Exec(ctx, "INSERT INTO adatrack_gps_default.tm_user_company_access (user_id, role_code) VALUES (9999, 'ADMIN') ON CONFLICT DO NOTHING")
	dbclient.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS adatrack_gps_lialse01")
	dbclient.Pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS adatrack_gps_lialse01.tm_user_company_access (user_id int, role_code varchar, is_active boolean default true, deleted_at timestamp)")
	dbclient.Pool.Exec(ctx, "INSERT INTO adatrack_gps_lialse01.tm_user_company_access (user_id, role_code) VALUES (9999, 'VIEWER')")

	users := []UserInfo{
		{ID: 9999, Email: "test@test.local"},
	}
	var userIDs []int
	userMap := make(map[int]*UserInfo)

	for i := range users {
		userIDs = append(userIDs, users[i].ID)
		userMap[users[i].ID] = &users[i]
	}

	compRows, _ := dbclient.Pool.Query(ctx, "SELECT code FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL AND business_type = 'b2b'")
	var codes []string
	for compRows.Next() {
		var code string
		compRows.Scan(&code)
		codes = append(codes, code)
	}
	compRows.Close()

	if len(codes) > 0 {
		var queryParts []string
		for _, code := range codes {
			schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(code))
			queryParts = append(queryParts, fmt.Sprintf("SELECT user_id, '%s' as company_code FROM %s.tm_user_company_access WHERE user_id = ANY($1) AND deleted_at IS NULL AND is_active = true", code, schema))
		}
		query := strings.Join(queryParts, " UNION ALL ")
		fmt.Println("Executing query:", query)
		
		accessRows, err := dbclient.Pool.Query(ctx, query, userIDs)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		for accessRows.Next() {
			var uid int
			var ccode string
			if err := accessRows.Scan(&uid, &ccode); err == nil {
				if user, ok := userMap[uid]; ok {
					user.Tenants = append(user.Tenants, ccode)
				}
			}
		}
		accessRows.Close()
	}

	fmt.Printf("User tenants: %v\n", users[0].Tenants)
}
