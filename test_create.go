package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := "postgres://adatrack_gps_user:adatrack_gps_password@localhost:5432/adatrack_gps_master?sslmode=disable"
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	schema := "adatrack_gps_TESST001"
	_, err = pool.Exec(context.Background(), "CREATE SCHEMA " + schema)
	if err != nil {
		log.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	files, _ := filepath.Glob("database/migrations/company_pg/*.up.sql")
	sort.Strings(files)
	for _, file := range files {
		sqlBytes, err := os.ReadFile(file)
		if err == nil {
			execSQL := fmt.Sprintf("SET search_path TO %s, public; %s", schema, string(sqlBytes))
			_, err := tx.Exec(context.Background(), execSQL)
			if err != nil {
				log.Printf("Error in %s: %v", file, err)
			}
		}
	}
	
	// Test the grant admin query
	_, err = tx.Exec(context.Background(), fmt.Sprintf(`
		INSERT INTO %s.tm_user_company_access (user_id, role_code, is_active)
		VALUES (1, 'ADMIN', true)
		ON CONFLICT DO NOTHING
	`, schema))
	if err != nil {
		log.Printf("Error granting admin: %v", err)
	} else {
		log.Printf("Success granting admin!")
	}

	tx.Rollback(context.Background())
}
