package main

import (
	"fmt"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbUser := os.Getenv("DB_USER")
		dbPass := os.Getenv("DB_PASSWORD")
		dbName := os.Getenv("DB_NAME")
		dbHost := os.Getenv("DB_HOST")
		if dbHost == "" {
			dbHost = "postgres"
		}
		if dbUser != "" && dbPass != "" && dbName != "" {
			log.Printf("DEBUG: dbUser len=%d, dbPass len=%d, dbName len=%d", len(dbUser), len(dbPass), len(dbName))
			importURL := url.URL{
				Scheme: "postgres",
				User:   url.UserPassword(dbUser, dbPass),
				Host:   dbHost + ":5432",
				Path:   dbName,
				RawQuery: "sslmode=disable",
			}
			dbURL = importURL.String()
		} else {
			dbURL = "postgres://adatrack_local:local_password@localhost:5432/adatrack_gps_master?sslmode=disable"
		}
	} else {
		// Attempt to fix improperly encoded passwords in DATABASE_URL if they exist
		if parsed, err := url.Parse(dbURL); err == nil && parsed.User != nil {
			pass, _ := parsed.User.Password()
			parsed.User = url.UserPassword(parsed.User.Username(), pass)
			dbURL = parsed.String()
		}
	}

	// 1. Ensure master and template schemas exist before migrations run
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to postgres: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS adatrack_gps_master"); err != nil {
		log.Printf("Warning: failed to create adatrack_gps_master schema: %v", err)
	}
	if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS adatrack_gps_template"); err != nil {
		log.Printf("Warning: failed to create adatrack_gps_template schema: %v", err)
	}

	baseDir := os.Getenv("MIGRATION_DIR")
	if baseDir == "" {
		baseDir = "../../database/migrations"
	}

	masterPath, err := filepath.Abs(filepath.Join(baseDir, "master_pg"))
	if err != nil {
		log.Fatalf("Failed to get abs path for master: %v", err)
	}
	companyPath, err := filepath.Abs(filepath.Join(baseDir, "company_pg"))
	if err != nil {
		log.Fatalf("Failed to get abs path for company: %v", err)
	}

	action := "up"
	if len(os.Args) > 1 {
		action = os.Args[1]
	}

	// 2. Run master migrations
	log.Println("=== Running Master Schema Migrations ===")
	runMigrate("file://"+masterPath, dbURL, "adatrack_gps_master", action)

	// 3. Discover all tenant schemas (adatrack_gps_% excluding master)
	rows, err := db.Query(`
		SELECT schema_name 
		FROM information_schema.schemata 
		WHERE schema_name LIKE 'adatrack_gps_%' 
		  AND schema_name != 'adatrack_gps_master'
		ORDER BY schema_name
	`)
	if err != nil {
		log.Fatalf("Failed to query tenant schemas: %v", err)
	}
	defer rows.Close()

	var tenantSchemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			tenantSchemas = append(tenantSchemas, s)
		}
	}

	// Ensure template is included if not discovered
	hasTemplate := false
	for _, s := range tenantSchemas {
		if s == "adatrack_gps_template" {
			hasTemplate = true
			break
		}
	}
	if !hasTemplate {
		tenantSchemas = append([]string{"adatrack_gps_template"}, tenantSchemas...)
	}

	// 4. Run company migrations for each tenant schema
	log.Printf("=== Running Tenant Schema Migrations (%d schemas found) ===", len(tenantSchemas))
	for _, schema := range tenantSchemas {
		log.Printf("Applying company migrations to tenant schema: %s", schema)
		runMigrate("file://"+companyPath, dbURL, schema, action)
	}
	log.Println("=== Multi-Tenant Migrations Completed Successfully ===")
}

func runMigrate(sourceURL, dbURL, targetSchema string, action string) {
	sep := "?"
	if strings.Contains(dbURL, "?") {
		sep = "&"
	}
	finalURL := dbURL + sep + "search_path=" + targetSchema + ",public"

	m, err := migrate.New(sourceURL, finalURL)
	if err != nil {
		log.Fatalf("[%s] Failed to initialize migrations: %v", targetSchema, err)
	}
	defer m.Close()

	if action == "down" {
		err = m.Down()
	} else {
		err = m.Up()
	}
	
	if err != nil && err != migrate.ErrNoChange {
		if strings.Contains(err.Error(), "Dirty database") {
			log.Printf("[%s] Dirty database detected! Resetting schema to recover...", targetSchema)
			
			db, errOpen := sql.Open("postgres", dbURL)
			if errOpen != nil {
				log.Fatalf("[%s] Failed to open db for recovery: %v", targetSchema, errOpen)
			}
			defer db.Close()

			_, dropErr := db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", targetSchema))
			if dropErr != nil {
				log.Fatalf("Failed to drop dirty schema %s: %v", targetSchema, dropErr)
			}
			_, createErr := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", targetSchema))
			if createErr != nil {
				log.Fatalf("Failed to recreate schema %s: %v", targetSchema, createErr)
			}
			
			// Re-instantiate migrate and run again
			m2, err2 := migrate.New(sourceURL, finalURL)
			if err2 != nil {
				log.Fatalf("[%s] Failed to re-initialize migrate: %v", targetSchema, err2)
			}
			
			if action == "down" {
				err = m2.Down()
			} else {
				err = m2.Up()
			}
			
			if err != nil && err != migrate.ErrNoChange {
				log.Fatalf("[%s] Failed to run migrations after reset: %v", targetSchema, err)
			}
			log.Printf("[%s] Successfully recovered from dirty state and applied migrations!", targetSchema)
			return
		}
		log.Fatalf("[%s] Failed to run migrations: %v", targetSchema, err)
	}

	if err == migrate.ErrNoChange {
		log.Printf("[%s] No new migrations to apply.", targetSchema)
	} else {
		log.Printf("[%s] Migrations applied successfully.", targetSchema)
	}
}
