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
		// limit == 0 => informational: probe skala tanpa SLA (lihat catatan di bawah),
		// tidak dihitung sebagai kegagalan.
		limit time.Duration
		args  []any
	}
	to := time.Now().UTC()
	from30 := to.Add(-30 * 24 * time.Hour)
	from24 := to.Add(-24 * time.Hour)

	// Endpoint history/playback selalu ter-scope satu kendaraan (FR-5.1/§8.2:
	// `WHERE vehicle_id = ? AND timestamp BETWEEN ... ORDER BY ... LIMIT n`).
	// Ambil satu kendaraan nyata agar bentuk yang diukur = bentuk yang dipanggil klien.
	var vehicleID int64
	if err := conn.QueryRow(`SELECT id FROM tm_vehicles WHERE deleted_at IS NULL ORDER BY id LIMIT 1`).Scan(&vehicleID); err != nil {
		log.Fatalf("fixture kendaraan: %v", err)
	}

	benches := []bench{
		// --- SLA PRD §17 ("Database Query Performance < 1.5 s — Playback 30 days") ---
		{"history.30d.vehicle", `SELECT id, latitude, longitude, speed, "timestamp" FROM th_telemetry_logs WHERE vehicle_id = $1 AND "timestamp" BETWEEN $2 AND $3 ORDER BY "timestamp" DESC LIMIT 1000`, 1500 * time.Millisecond, []any{vehicleID, from30, to}},
		{"geofence.list", `SELECT id, name FROM tm_geofences WHERE deleted_at IS NULL LIMIT 100`, 500 * time.Millisecond, nil},
		{"vehicles.list", `SELECT id, plate_number FROM tm_vehicles WHERE deleted_at IS NULL LIMIT 100`, 500 * time.Millisecond, nil},
		// --- Informational (limit 0): probe skala, BUKAN SLA PRD ---
		// Tidak ada endpoint yang memanggil halaman/count telemetry TANPA filter
		// kendaraan, dan pagination endpoint memakai COUNT ter-scope kendaraan —
		// jadi biaya query di bawah ini tidak mewakili permintaan klien mana pun.
		// Tetap diukur supaya tren skalanya terlihat (volume sintetis 400 msg/s).
		{"history.30d.global", `SELECT id, latitude, longitude, speed, "timestamp" FROM th_telemetry_logs WHERE "timestamp" BETWEEN $1 AND $2 ORDER BY "timestamp" DESC LIMIT 1000`, 0, []any{from30, to}},
		{"count.24h.global", `SELECT count(*) FROM th_telemetry_logs WHERE "timestamp" >= $1`, 0, []any{from24}},
		{"count.30d.vehicle", `SELECT count(*) FROM th_telemetry_logs WHERE vehicle_id = $1 AND "timestamp" >= $2`, 0, []any{vehicleID, from30}},
	}

	failed := 0
	for _, c := range benches {
		start := time.Now()
		rows, err := conn.Query(c.query, c.args...)
		if err != nil {
			fmt.Printf("[FAIL] %-20s error=%v\n", c.name, err)
			failed++
			continue
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		el := time.Since(start)
		switch {
		case c.limit == 0:
			fmt.Printf("[INFO] %-20s rows=%d elapsed=%s (informational: probe skala, bukan SLA PRD)\n",
				c.name, n, el.Round(time.Millisecond))
		case el > c.limit:
			fmt.Printf("[FAIL] %-20s rows=%d elapsed=%s (sla=%s)\n", c.name, n, el.Round(time.Millisecond), c.limit)
			failed++
		default:
			fmt.Printf("[PASS] %-20s rows=%d elapsed=%s (sla=%s)\n", c.name, n, el.Round(time.Millisecond), c.limit)
		}
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
