package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://adatrack_local:local_password@localhost:5432/adatrack_gps_master?sslmode=disable"
	}

	// Ensure master schema exists before migrations run
	db, err := sql.Open("postgres", dbURL)
	if err == nil {
		db.Exec("CREATE SCHEMA IF NOT EXISTS adatrack_gps_master")
		db.Close()
	}

	masterPath, err := filepath.Abs("../../database/migrations/master_pg")
	if err != nil {
		log.Fatalf("Failed to get abs path for master: %v", err)
	}
	companyPath, err := filepath.Abs("../../database/migrations/company_pg")
	if err != nil {
		log.Fatalf("Failed to get abs path for company: %v", err)
	}

	runMigrate("file://"+masterPath, dbURL, "master_pg")
	runMigrate("file://"+companyPath, dbURL, "company_pg")
}

func runMigrate(sourceURL, dbURL, name string) {
	// For company_pg, we might need to apply it per company schema, but for MVP,
	// if we assume company_pg is a template, we just validate it compiles.
	// We will apply it to public or a specific schema.
	// Since PRD says migrations are run by Coolify, we apply them here.
	
	// Add search_path for master
	var finalURL string
	if name == "master_pg" {
		finalURL = dbURL + "&search_path=adatrack_gps_master"
	} else {
		finalURL = dbURL + "&search_path=public"
	}

	m, err := migrate.New(sourceURL, finalURL)
	if err != nil {
		log.Fatalf("Failed to initialize %s migrations: %v", name, err)
	}

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		log.Fatalf("Failed to run %s migrations: %v", name, err)
	}
	
	if err == migrate.ErrNoChange {
		log.Printf("[%s] No new migrations to apply.", strings.ToUpper(name))
	} else {
		log.Printf("[%s] Migrations applied successfully.", strings.ToUpper(name))
	}
}
