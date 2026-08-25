// =====================================================================
// querybench — Phase B4 acceptance tooling:
//   "query pembacaan 30 hari < 1.5 detik" + "geofence query < 500 ms"
//   (roadmap B4 / PRD §12).
//
// Mode:
//   -mode seed   : isi telemetry_logs dgn data sintetis realistis
//                  (default: 20 vehicle × 30 hari × interval 5 detik)
//   -mode bench  : jalankan query PERSIS seperti handler produksi
//                  service-websocket GET /vehicles/{id}/history
//                  utk window 24 jam & 30 hari, R repetisi → min/avg/p95,
//                  + EXPLAIN ANALYZE sekali per skenario.
//
// Contoh:
//   go build -o querybench .
//   ./querybench -mode seed -days 30 -interval 5 -vehicles 20
//   ./querybench -mode bench -reps 5
//
// Catatan jujur: angka adalah hasil mesin dev lokal (docker MySQL) — bukan
// pengganti RDS produksi; metodologi & EXPLAIN dilampirkan di output.
// =====================================================================
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var (
	mode      = flag.String("mode", "bench", "seed | bench")
	dbName    = flag.String("db", envOr("COMPANY_DB_NAME", "adatrack_gps_def001"), "company database name")
	vehiclesN = flag.Int("vehicles", 20, "[seed] jumlah vehicle sintetis")
	days      = flag.Int("days", 30, "[seed] jumlah hari data ke belakang")
	intervalS = flag.Int("interval", 5, "[seed] interval antar titik (detik)")
	batchSize = flag.Int("batch", 2000, "[seed] baris per multi-value INSERT")
	workers   = flag.Int("workers", 4, "[seed] goroutine insert paralel")
	dropFirst = flag.Bool("truncate", false, "[seed] TRUNCATE telemetry_logs sebelum seed")

	imei    = flag.String("imei", "", "[bench] IMEI yang di-query (kosong = auto IMEI terbesar)")
	reps    = flag.Int("reps", 5, "[bench] repetisi per skenario")
	explain = flag.Bool("explain", true, "[bench] cetak EXPLAIN ANALYZE per skenario")

	companyCode = envOr("COMPANY_CODE", "DEF001")
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func dsn() string {
	host := envOr("MYSQL_HOST", "127.0.0.1")
	port := envOr("MYSQL_PORT", "3307")
	user := envOr("MYSQL_USER", "adatrack_app")
	pass := os.Getenv("MYSQL_PASSWORD")
	if user == "" {
		user = "root"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		user, pass, host, port, *dbName)
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	db, err := sql.Open("mysql", dsn())
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(*workers + 4)
	must(db.Ping())

	switch *mode {
	case "seed":
		seedMode(db)
	case "bench":
		benchMode(db)
	default:
		log.Fatalf("mode tidak dikenal: %q (seed|bench)", *mode)
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
// ---------------------------------------------------------------------
// SEED
// ---------------------------------------------------------------------

type seedJob struct {
	vehicleID int64
	imei      string
}

func seedMode(db *sql.DB) {
	if *dropFirst {
		log.Printf("TRUNCATE telemetry_logs ...")
		mustExec(nil, exec(db, "TRUNCATE TABLE telemetry_logs"))
	}

	totalPerVehicle := int64((*days * 86400) / *intervalS)
	total := totalPerVehicle * int64(*vehiclesN)
	log.Printf("SEED: %d vehicle x %d hari x interval %ds = ±%d baris (batch %d, workers %d)",
		*vehiclesN, *days, *intervalS, total, *batchSize, *workers)

	jobs := make(chan seedJob)
	var wg sync.WaitGroup
	var inserted int64
	start := time.Now()

	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				n := insertVehicle(db, job, totalPerVehicle)
				cur := atomic.AddInt64(&inserted, n)
				if cur%1_000_000 < n {
					rate := float64(cur) / time.Since(start).Seconds()
					log.Printf("  progress: %d baris (%.0f rows/s)", cur, rate)
				}
			}
		}()
	}

	for v := 1; v <= *vehiclesN; v++ {
		jobs <- seedJob{vehicleID: int64(v), imei: fmt.Sprintf("86420199%07d", v)}
	}
	close(jobs)
	wg.Wait()

	el := time.Since(start)
	n := atomic.LoadInt64(&inserted)
	log.Printf("SEED SELESAI: %d baris dalam %v (%.0f rows/s)", n, el.Round(time.Millisecond), float64(n)/el.Seconds())
}

// insertVehicle memasukkan totalPerVehicle titik untuk satu vehicle.
// Multi-value INSERT dibangun per chunk dengan placeholder PERSIS sebesar
// jumlah baris chunk (chunk terakhir bisa lebih kecil) — menghindari error
// "expected N arguments" dari prepared statement berukuran tetap.
func insertVehicle(db *sql.DB, job seedJob, totalPerVehicle int64) int64 {
	rng := rand.New(rand.NewSource(job.vehicleID))
	end := time.Now().Truncate(time.Second)
	startTs := end.Add(-time.Duration(totalPerVehicle) * time.Duration(*intervalS) * time.Second)

	const cols = `(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	var inserted int64
	lat, lon := -6.200000, 106.816666 // base Jakarta
	nowStr := time.Now().Format("2006-01-02 15:04:05")
	args := make([]interface{}, 0, *batchSize*12)
	rowsPh := make([]string, 0, *batchSize)

	flush := func() {
		if len(args) == 0 {
			return
		}
		q := "INSERT INTO telemetry_logs " +
			"(vehicle_id, imei, company_code, latitude, longitude, speed, heading, altitude, acc_status, battery_level, timestamp, created_at) VALUES " +
			strings.Join(rowsPh, ",")
		mustExec(nil, execArgs(db, q, args))
		inserted += int64(len(args) / 12)
		args = args[:0]
		rowsPh = rowsPh[:0]
	}

	for i := int64(0); i < totalPerVehicle; i++ {
		ts := startTs.Add(time.Duration(i) * time.Duration(*intervalS) * time.Second)
		lat += (rng.Float64() - 0.5) * 0.0004
		lon += (rng.Float64() - 0.5) * 0.0004
		args = append(args,
			job.vehicleID, job.imei, companyCode,
			lat, lon, rng.Float64()*80, rng.Float64()*360, 10.0+rng.Float64()*30,
			rng.Intn(2), 60+rng.Intn(40),
			ts.Format("2006-01-02 15:04:05"), nowStr)
		rowsPh = append(rowsPh, cols)
		if len(rowsPh) >= *batchSize {
			flush()
		}
	}
	flush()
	return inserted
}

func execArgs(db *sql.DB, q string, args []interface{}) error {
	_, err := db.Exec(q, args...)
	return err
}

func mustExec(_ sql.Result, err error) {
	if err != nil {
		log.Fatal("insert:", err)
	}
}

func exec(db *sql.DB, q string) error {
	_, err := db.Exec(q)
	return err
}

// ---------------------------------------------------------------------
// BENCH
// ---------------------------------------------------------------------

type result struct {
	name    string
	timesMs []float64
}

type scenario struct {
	name  string
	query string
	win   time.Duration
}

func benchScenarios() []scenario {
	// Skenario select memakai bentuk BARU handler (ORDER BY timestamp DESC,
	// searah indeks; ASC dikembalikan di aplikasi) — B4 perf fix.
	const sel = `SELECT timestamp, latitude, longitude, speed, heading FROM telemetry_logs WHERE imei = ? AND timestamp BETWEEN ? AND ? ORDER BY timestamp DESC LIMIT 5000`
	const cnt = `SELECT COUNT(*) FROM telemetry_logs WHERE imei = ? AND timestamp BETWEEN ? AND ?`
	return []scenario{
		{"history_24h_count", cnt, 24 * time.Hour},
		{"history_24h_select", sel, 24 * time.Hour},
		{"history_30d_count", cnt, 30 * 24 * time.Hour},
		{"history_30d_select", sel, 30 * 24 * time.Hour},
	}
}

func benchMode(db *sql.DB) {
	if *imei == "" {
		row := db.QueryRow(`SELECT imei FROM telemetry_logs GROUP BY imei ORDER BY COUNT(*) DESC LIMIT 1`)
		if err := row.Scan(imei); err != nil {
			log.Fatalf("imei tidak diset & gagal auto-detect: %v", err)
		}
		log.Printf("auto-pilih IMEI dgn baris terbanyak: %s", *imei)
	}

	var totalRows int64
	must(db.QueryRow(`SELECT COUNT(*) FROM telemetry_logs`).Scan(&totalRows))
	log.Printf("BENCH telemetry_logs: %d baris total, imei=%s, reps=%d", totalRows, *imei, *reps)

	scs := benchScenarios()

	// Warm-up buffer pool: 1 iterasi per skenario (tidak dicatat).
	for _, sc := range scs {
		runOnce(db, sc.query, warmArgs(sc)...)
	}

	var results []result
	for _, sc := range scs {
		r := result{name: sc.name}
		for i := 0; i < *reps; i++ {
			from := time.Now().Add(-sc.win - time.Duration(i)*time.Hour)
			ms, _ := timedQuery(db, sc.query, *imei, from, from.Add(sc.win))
			r.timesMs = append(r.timesMs, ms)
		}
		results = append(results, r)
	}
	results = append(results, benchGeofence(db)...)

	printReport(results, totalRows)

	if *explain {
		printExplains(db, scs)
	}
}

func warmArgs(sc scenario) []interface{} {
	from := time.Now().Add(-sc.win - 24*time.Hour)
	return []interface{}{*imei, from, from.Add(sc.win)}
}

func benchGeofence(db *sql.DB) []result {
	var out []result
	// Path worker-alert persis: ActiveGeofences(vehicleID) — store_geofence.go.
	vid := 1
	r := result{name: "geofence_active_by_vehicle"}
	for i := 0; i < *reps; i++ {
		ms, _ := timedQuery(db,
			`SELECT g.id, g.name, g.area_type, g.coordinates, COALESCE(g.radius_meters,0), COALESCE(g.boundary_points, JSON_ARRAY())
			 FROM geofences g
			 JOIN geofence_vehicles gv ON gv.geofence_id = g.id AND gv.is_enabled = TRUE
			 WHERE g.is_active = TRUE AND gv.vehicle_id = ?`, vid)
		r.timesMs = append(r.timesMs, ms)
	}
	out = append(out, r)

	// Path API admin: ringkasan geofence + jumlah assignment.
	r2 := result{name: "geofence_with_assignments"}
	for i := 0; i < *reps; i++ {
		ms, _ := timedQuery(db,
			`SELECT g.id, g.name, COUNT(gv.vehicle_id) AS assigned
			 FROM geofences g LEFT JOIN geofence_vehicles gv ON gv.geofence_id = g.id
			 WHERE g.is_active = TRUE GROUP BY g.id, g.name ORDER BY g.id`)
		r2.timesMs = append(r2.timesMs, ms)
	}
	out = append(out, r2)
	return out
}

func timedQuery(db *sql.DB, q string, args ...interface{}) (ms float64, rowsRead int) {
	t0 := time.Now()
	rows, err := db.Query(q, args...)
	if err != nil {
		log.Fatalf("query gagal (%s): %v", short(q), err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("rows err: %v", err)
	}
	return float64(time.Since(t0).Microseconds()) / 1000.0, n
}

func runOnce(db *sql.DB, q string, args ...interface{}) {
	_, _ = timedQuery(db, q, args...)
}

func printReport(rs []result, totalRows int64) {
	fmt.Println("\n================ HASIL BENCHMARK ================")
	fmt.Printf("tabel: telemetry_logs (%d baris) | reps=%d\n\n", totalRows, *reps)
	fmt.Printf("%-28s %10s %10s %10s %14s\n", "skenario", "min(ms)", "avg(ms)", "p95(ms)", "SLA")
	for _, r := range rs {
		sort.Float64s(r.timesMs)
		mn, avg, p95 := stats(r.timesMs)
		sla := "-"
		switch {
		case strings.Contains(r.name, "history"):
			if p95 < 1500 {
				sla = "PASS (<1.5s)"
			} else {
				sla = "FAIL (>1.5s)"
			}
		case strings.Contains(r.name, "geofence"):
			if p95 < 500 {
				sla = "PASS (<500ms)"
			} else {
				sla = "FAIL (>500ms)"
			}
		}
		fmt.Printf("%-28s %10.1f %10.1f %10.1f %14s\n", r.name, mn, avg, p95, sla)
	}
	fmt.Println("=================================================")
}

func printExplains(db *sql.DB, scs []scenario) {
	fmt.Println("\n===== EXPLAIN ANALYZE =====")
	for _, sc := range scs {
		rows, err := db.Query("EXPLAIN ANALYZE "+sc.query, warmArgs(sc)...)
		if err != nil {
			log.Printf("EXPLAIN %s gagal: %v", sc.name, err)
			continue
		}
		fmt.Printf("\n--- %s ---\n", sc.name)
		cols, _ := rows.Columns()
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			must(rows.Scan(ptrs...))
			parts := make([]string, len(cols))
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					parts[i] = string(b)
				} else {
					parts[i] = fmt.Sprint(v)
				}
			}
			fmt.Println(strings.Join(parts, " | "))
		}
		rows.Close()
	}
}

func stats(xs []float64) (min, avg, p95 float64) {
	if len(xs) == 0 {
		return 0, 0, 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	idx := int(float64(len(xs)-1) * 0.95)
	return xs[0], sum / float64(len(xs)), xs[idx]
}

func short(q string) string {
	s := strings.Join(strings.Fields(q), " ")
	if len(s) > 60 {
		s = s[:60] + "..."
	}
	return s
}
