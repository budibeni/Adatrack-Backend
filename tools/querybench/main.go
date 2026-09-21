// Command querybench measures the B4 query SLA (PRD §16/§17): 30-day history
// < 1.5 s, 24 h count fast, geofence lookup < 500 ms — against a live tenant.
// Usage (from backend/): go run ./tools/querybench --company=DEV001
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	company := flag.String("company", envOr("E2E_COMPANY", "DEV001"), "tenant code")
	host := flag.String("pg-host", envOr("POSTGRES_HOST", "127.0.0.1"), "pg host")
	port := flag.String("pg-port", envOr("POSTGRES_PORT", "5533"), "pg port")
	user := flag.String("pg-user", envOr("POSTGRES_USER", "adatrack"), "pg user")
	pass := flag.String("pg-password", envOr("POSTGRES_PASSWORD", ""), "pg password")
	db := flag.String("pg-db", envOr("POSTGRES_DB", "adatrack_gps_db"), "pg db")
	flag.Parse()

	schema := "adatrack_gps_" + lower(*company)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&search_path=%s",
		*user, *pass, *host, *port, *db, schema)
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer conn.Close()

	type bench struct {
		name  string
		query string
		limit time.Duration
		args  []any
	}
	to := time.Now().UTC()
	from30 := to.Add(-30 * 24 * time.Hour)
	from24 := to.Add(-24 * time.Hour)
	benches := []bench{
		{"history.30d", `SELECT id, latitude, longitude, speed, "timestamp" FROM th_telemetry_logs WHERE "timestamp" BETWEEN $1 AND $2 ORDER BY "timestamp" DESC LIMIT 1000`, 1500 * time.Millisecond, []any{from30, to}},
		{"count.24h", `SELECT count(*) FROM th_telemetry_logs WHERE "timestamp" >= $1`, 1500 * time.Millisecond, []any{from24}},
		{"geofence.list", `SELECT id, name FROM tm_geofences WHERE deleted_at IS NULL LIMIT 100`, 500 * time.Millisecond, nil},
		{"vehicles.list", `SELECT id, plate_number FROM tm_vehicles WHERE deleted_at IS NULL LIMIT 100`, 500 * time.Millisecond, nil},
	}

	failed := 0
	for _, c := range benches {
		start := time.Now()
		rows, err := conn.Query(c.query, c.args...)
		if err != nil {
			fmt.Printf("[FAIL] %-15s error=%v\n", c.name, err)
			failed++
			continue
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		el := time.Since(start)
		status := "PASS"
		if el > c.limit {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %-15s rows=%d elapsed=%s (sla=%s)\n", status, c.name, n, el.Round(time.Millisecond), c.limit)
	}
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("querybench: all SLA checks passed")
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func lower(s string) string {
	out := ""
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out += string(r)
	}
	return out
}
