// Command migrate-tenant men-provision & mengelola tenant database per-company
// (PRD §6 auto-provision, roadmap task #11).
//
// Subcommands:
//
//	provision  -code ABLE01 -name "Company Name" [-country ID] [-tz Asia/Jakarta]
//	    Membuat/men-ensure company di master.companies + database
//	    adatrack_gps_able01 + menerapkan migration template company.
//	seed-admin -code ABLE01 -email a@x.io -password secret [-name "Admin"]
//	    Membuat user di master.users (bcrypt) + user_company_access di company DB.
//	list       Menampilkan company + status pool.
//	health     Health check master/default/semua company pool.
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

	"ajb_gps/internal/tenant"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg := tenant.NewConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "config invalid:", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect tanpa metrics/registry — tool CLI cukup sederhana.
	mgr, err := tenant.New(ctx, cfg, nil, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tenant init:", err)
		os.Exit(1)
	}
	defer mgr.Close()

	switch os.Args[1] {
	case "provision":
		runProvision(ctx, mgr, cfg)
	case "seed-admin":
		runSeedAdmin(ctx, mgr, cfg)
	case "list":
		runList(mgr)
	case "resolve-imei":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "resolve-imei: usage: migrate-tenant resolve-imei <IMEI>")
			os.Exit(2)
		}
		code, err := mgr.ResolveCompanyByIMEI(ctx, os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolve-imei:", err)
			os.Exit(1)
		}
		db, _ := mgr.DB(code)
		fmt.Printf("imei %s → company %s (db=%s, pool=%v)\n", os.Args[2], code, cfg.CompanyDBName(code), db != nil)
	case "health":
		runHealth(ctx, mgr)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: migrate-tenant <command> [flags]

Commands:
  provision -code <CODE> -name <NAME> [-country ID] [-tz Asia/Jakarta]
      Ensure company row in master.companies + create & migrate its database.
  seed-admin -code <CODE> -email <EMAIL> -password <PASS> [-name <NAME>]
      Create a bcrypt-hashed user in master.users + user_company_access.
  list
      Print registered tenants and pool status.
  resolve-imei <IMEI>
      Resolve IMEI → company_code via master (anti-spoofing lookup).
  health
      Health-check master, default and all company pools.

Env (PRD $7): MASTER_DB_*, COMPANY_DB_PREFIX, MYSQL_POOL_MIN/MAX
`)
}

// findCompanyMigrations locates backend/database/migrations/company by walking
// upward from the executable directory until the directory is found.
func findCompanyMigrations() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 6 && dir != filepath.Dir(dir); i++ {
		cand := filepath.Join(dir, "database", "migrations", "company")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand
		}
		dir = filepath.Dir(dir)
	}
	// Fallback: relative to CWD (development: run from repo/backend root).
	for _, c := range []string{"backend/database/migrations/company", "database/migrations/company", "../database/migrations/company"} {
		if abs, err := filepath.Abs(c); err == nil {
			if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
				return abs
			}
		}
	}
	return ""
}

func runProvision(ctx context.Context, mgr *tenant.Manager, cfg tenant.Config) {
	fs := flag.NewFlagSet("provision", flag.ExitOnError)
	code := fs.String("code", "", "company code, e.g. DEV001")
	name := fs.String("name", "", "company name")
	country := fs.String("country", "ID", "ISO 3166-1 alpha-2 country code")
	tz := fs.String("tz", "Asia/Jakarta", "IANA timezone")
	_ = fs.Parse(os.Args[2:])

	if *code == "" || *name == "" {
		fmt.Fprintln(os.Stderr, "provision: -code and -name are required")
		os.Exit(2)
	}

	// Ensure MigrationsDir is populated — fall back to filesystem discovery
	// if the env var COMPANY_MIGRATIONS_DIR was not set.
	if cfg.MigrationsDir == "" {
		cfg.MigrationsDir = findCompanyMigrations()
	}
	if cfg.MigrationsDir == "" {
		fmt.Fprintln(os.Stderr, "provision: company migrations dir not found")
		os.Exit(1)
	}

	// Use the shared ProvisionCompany method (PRD §6.1 auto-provision).
	result, err := mgr.ProvisionCompany(ctx, tenant.ProvisionCompanyInput{
		Code:        *code,
		Name:        *name,
		CountryCode: *country,
		Timezone:    *tz,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "provision:", err)
		os.Exit(1)
	}
	fmt.Printf("provisioned tenant %s (db=%s, migrations=%d)\n", result.Code, result.DBName, result.MigrationsApplied)
}

func runSeedAdmin(ctx context.Context, mgr *tenant.Manager, cfg tenant.Config) {
	fs := flag.NewFlagSet("seed-admin", flag.ExitOnError)
	code := fs.String("code", "", "company code")
	email := fs.String("email", "", "user email")
	password := fs.String("password", "", "plain password (bcrypt-hashed before storage)")
	fullName := fs.String("name", "Administrator", "full name")
	_ = fs.Parse(os.Args[2:])

	if *code == "" || *email == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "seed-admin: -code, -email and -password are required")
		os.Exit(2)
	}

	// PRD §4.2 / B4: password hash memakai bcrypt cost 12.
	hash, err := bcrypt.GenerateFromPassword([]byte(*password), 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin: bcrypt:", err)
		os.Exit(1)
	}

	master := mgr.Master()
	var companyID int64
	if err := master.QueryRowContext(ctx, "SELECT id FROM companies WHERE code = ?", *code).Scan(&companyID); err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin: company not found:", *code, err)
		os.Exit(1)
	}

	// Enterprise-standard: split full name into first/last, mark email verified,
	// stamp password_changed_at, default locale 'id' (migration 011).
	firstName := *fullName
	lastName := ""
	if i := strings.IndexByte(*fullName, ' '); i > 0 {
		firstName = (*fullName)[:i]
		lastName = strings.TrimSpace((*fullName)[i+1:])
	}

	res, err := master.ExecContext(ctx, `
		INSERT INTO users (company_id, company_code, email, password_hash, full_name,
		                   first_name, last_name, role, status, email_verified,
		                   mfa_enabled, failed_login_attempts, locale, password_changed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'Admin', 'active', TRUE, FALSE, 0, 'id', NOW())
		ON DUPLICATE KEY UPDATE
			company_id = VALUES(company_id),
			company_code = VALUES(company_code),
			password_hash = VALUES(password_hash),
			full_name = VALUES(full_name),
			first_name = VALUES(first_name),
			last_name = VALUES(last_name),
			password_changed_at = VALUES(password_changed_at),
			status = 'active'`,
		companyID, *code, *email, string(hash), *fullName, firstName, lastName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin: upsert user:", err)
		os.Exit(1)
	}

	var userID int64
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		userID = id
	} else {
		_ = master.QueryRowContext(ctx, "SELECT id FROM users WHERE email = ?", *email).Scan(&userID)
	}

	pool, err := mgr.DB(*code)
	if err != nil {
		// Company DB belum di-provision: minta user run provision dulu.
		fmt.Fprintln(os.Stderr, "seed-admin: company DB not warmed (jalankan 'migrate-tenant provision' dulu):", err)
		os.Exit(1)
	}
	if _, err := pool.ExecContext(ctx, `
		INSERT INTO user_company_access (user_id, role_override, is_active)
		VALUES (?, 'Admin', TRUE)
		ON DUPLICATE KEY UPDATE role_override = 'Admin', is_active = TRUE`, userID); err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin: upsert user_company_access:", err)
		os.Exit(1)
	}

	fmt.Printf("admin seeded: user_id=%d email=%s company=%s\n", userID, *email, *code)
}

func runList(mgr *tenant.Manager) {
	companies := mgr.Companies()
	if len(companies) == 0 {
		fmt.Println("no companies registered")
		return
	}
	for _, c := range companies {
		status := "pool-ok"
		if _, err := mgr.DB(c.Code); err != nil {
			status = "pool-missing"
		}
		fmt.Printf("%-10s %-25s %-28s %s\n", c.Code, c.Name, c.DBName, status)
	}
}

func runHealth(ctx context.Context, mgr *tenant.Manager) {
	start := time.Now()
	healthy := "ok"
	if err := mgr.Health(ctx); err != nil {
		healthy = "error: " + err.Error()
		fmt.Println("tenant health:", healthy)
		os.Exit(1)
	}
	fmt.Printf("tenant health: %s (%s)\n", healthy, time.Since(start).Round(time.Millisecond))
}
