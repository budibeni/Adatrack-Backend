// Command loadtest-provision menyiapkan dummy companies dengan vehicles/IMEIs
// untuk load test multi-tenant B4.
//
// Cara pakai:
//
//	go run . -companies 10 -devices-per-company 100
//	go run . -reset // hapus tenant dummy lama
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ajb_gps/internal/dialect"

	"github.com/jackc/pgx/v5"
)

var (
	companiesFlag = flag.Int("companies", 10, "jumlah company dummy (TENANT01..TENANT10)")
	devicesFlag   = flag.Int("devices-per-company", 100, "jumlah device/IMEI per company")
	resetFlag     = flag.Bool("reset", false, "hapus tenant dummy lama sebelum provision")
)

const imeiBase = "86420104"

func main() {
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr())
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect failed:", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	if *resetFlag {
		slog.Info("resetting dummy tenants...")
		if err := resetTenants(ctx, conn); err != nil {
			fmt.Fprintln(os.Stderr, "reset failed:", err)
			os.Exit(1)
		}
	}

	migrationsDir := findMigrationsDir()
	if migrationsDir == "" {
		fmt.Fprintln(os.Stderr, "migrations dir not found")
		os.Exit(1)
	}
	migrationFiles, _ := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	slog.Info("found migrations", "count", len(migrationFiles))

	totalDevices := 0
	for i := 1; i <= *companiesFlag; i++ {
		code := fmt.Sprintf("TENANT%02d", i)
		if err := provisionCompany(ctx, conn, code, migrationFiles); err != nil {
			fmt.Fprintln(os.Stderr, "provision", code, "failed:", err)
			os.Exit(1)
		}
		vehicleIDs, err := createVehicles(ctx, conn, code, totalDevices, *devicesFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create vehicles", code, "failed:", err)
			os.Exit(1)
		}
		if err := registerIMEIs(ctx, conn, code, vehicleIDs, totalDevices); err != nil {
			fmt.Fprintln(os.Stderr, "register IMEIs", code, "failed:", err)
			os.Exit(1)
		}
		totalDevices += len(vehicleIDs)
		slog.Info("company done", "code", code, "devices", len(vehicleIDs))
	}

	fmt.Printf("\n✅ %d companies × %d devices = %d total\n", *companiesFlag, *devicesFlag, totalDevices)
}

func connStr() string {
	h := os.Getenv("POSTGRES_HOST")
	if h == "" { h = "127.0.0.1" }
	p := os.Getenv("POSTGRES_PORT")
	if p == "" { p = "5532" }
	u := os.Getenv("POSTGRES_USER")
	if u == "" { u = "adatrack_gps_user" }
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" { pw = "user@gps2608" }
	db := os.Getenv("POSTGRES_DB")
	if db == "" { db = "adatrack_gps_db" }
	ssl := os.Getenv("POSTGRES_SSLMODE")
	if ssl == "" { ssl = "disable" }
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", u, pw, h, p, db, ssl)
}

func findMigrationsDir() string {
	candidates := []string{
		"database/migrations/company_pg",
		"../../database/migrations/company_pg",
		"../../../database/migrations/company_pg",
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	for i := 0; i < 6 && dir != filepath.Dir(dir); i++ {
		cand := filepath.Join(dir, "database", "migrations", "company_pg")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

func resetTenants(ctx context.Context, conn *pgx.Conn) error {
	for i := 1; i <= *companiesFlag; i++ {
		code := fmt.Sprintf("TENANT%02d", i)
		schema := "adatrack_gps_" + strings.ToLower(code)
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)); err != nil {
			return fmt.Errorf("drop schema %s: %w", schema, err)
		}
		if _, err := conn.Exec(ctx, "DELETE FROM adatrack_gps_master.companies WHERE code = $1", code); err != nil {
			return fmt.Errorf("delete company %s: %w", code, err)
		}
		if _, err := conn.Exec(ctx, "DELETE FROM adatrack_gps_master.vehicle_imei_map WHERE company_code = $1", code); err != nil {
			return fmt.Errorf("delete imei map %s: %w", code, err)
		}
	}
	return nil
}

func provisionCompany(ctx context.Context, conn *pgx.Conn, code string, files []string) error {
	schema := "adatrack_gps_" + strings.ToLower(code)

	_, err := conn.Exec(ctx, `
		INSERT INTO adatrack_gps_master.companies (code, name, is_active, timezone, country_code, created_at, updated_at)
		VALUES ($1, $2, true, 'Asia/Jakarta', 'ID', NOW(), NOW())
		ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, is_active = true, updated_at = NOW()`,
		code, "Load Test "+code)
	if err != nil {
		return fmt.Errorf("insert company: %w", err)
	}

	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	// Set search_path so unqualified table names land in the company schema
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		for _, stmt := range dialect.SplitSQLStatements(string(content)) {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := conn.Exec(ctx, stmt); err != nil {
				if strings.Contains(err.Error(), "already exists") {
					continue
				}
				return fmt.Errorf("exec %s: %w", filepath.Base(f), err)
			}
		}
	}

	// Reset search_path to master for vehicle/IMEI operations
	if _, err := conn.Exec(ctx, "SET search_path TO adatrack_gps_master"); err != nil {
		return fmt.Errorf("reset search_path: %w", err)
	}

	return nil
}

func createVehicles(ctx context.Context, conn *pgx.Conn, code string, startID, count int) ([]int64, error) {
	schema := "adatrack_gps_" + strings.ToLower(code)
	// Set search_path to company schema for unqualified table references
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		imei := fmt.Sprintf("%s%07d", imeiBase, startID+i)
		plate := fmt.Sprintf("B%d %s", startID+i, code)
		var vid int64
		err := conn.QueryRow(ctx, `
			INSERT INTO vehicles (imei, plate_number, status, created_at, updated_at)
			VALUES ($1, $2, 'active', NOW(), NOW())
			RETURNING id`, imei, plate).Scan(&vid)
		if err != nil {
			return nil, fmt.Errorf("insert vehicle %s: %w", imei, err)
		}
		ids = append(ids, vid)
	}
	return ids, nil
}

func registerIMEIs(ctx context.Context, conn *pgx.Conn, code string, vehicleIDs []int64, startID int) error {
	for i, vid := range vehicleIDs {
		imei := fmt.Sprintf("%s%07d", imeiBase, startID+i)
		_, err := conn.Exec(ctx, `
			INSERT INTO adatrack_gps_master.vehicle_imei_map (imei, company_code, vehicle_id, created_at, updated_at)
			VALUES ($1, $2, $3, NOW(), NOW())
			ON CONFLICT (imei) DO UPDATE SET company_code = $2, vehicle_id = $3, updated_at = NOW()`,
			imei, code, vid)
		if err != nil {
			return fmt.Errorf("register imei %s: %w", imei, err)
		}
	}
	return nil
}

