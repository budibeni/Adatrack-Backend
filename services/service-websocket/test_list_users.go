package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"backend/internal/config"
	"backend/internal/dbclient"
)

func main() {
	config.LoadConfig()
	dbclient.InitDB(config.AppConfig)
	ctx := context.Background()

	// 1. Get user admin@tesst001.local
	var userID int
	err := dbclient.Pool.QueryRow(ctx, "SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'admin@tesst001.local'").Scan(&userID)
	if err != nil {
		fmt.Println("User not found:", err)
		return
	}
	fmt.Println("User ID:", userID)

	// 2. Run the dynamic query for this user
	compRows, _ := dbclient.Pool.Query(ctx, "SELECT code FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL AND business_type = 'b2b'")
	var codes []string
	for compRows.Next() {
		var code string
		compRows.Scan(&code)
		codes = append(codes, code)
	}
	compRows.Close()

	fmt.Println("Companies found:", codes)

	var queryParts []string
	for _, code := range codes {
		schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(code))
		queryParts = append(queryParts, fmt.Sprintf("SELECT user_id, '%s' as company_code FROM %s.tm_user_company_access WHERE user_id = ANY($1) AND deleted_at IS NULL AND is_active = true", code, schema))
	}

	query := strings.Join(queryParts, " UNION ALL ")
	fmt.Println("Query:", query)

	accessRows, err := dbclient.Pool.Query(ctx, query, []int{userID})
	if err != nil {
		fmt.Println("Query error:", err)
		return
	}
	
	var userCompanies []string
	for accessRows.Next() {
		var uid int
		var ccode string
		accessRows.Scan(&uid, &ccode)
		userCompanies = append(userCompanies, ccode)
	}
	accessRows.Close()

	fmt.Println("User Companies:", userCompanies)
}
