// =====================================================================
// partition-maintain — Phase B4, GAP #4 (retention & archival).
//
// Mengelola partisi telemetry_logs tiap company DB:
//   1. ENSURE : buat partisi bulanan ke depan (-ahead) dengan memecah
//               p_future (REORGANIZE PARTITION) — aman saat online.
//   2. DROP   : hapus partisi lebih tua dari -retain-months (GAP #4),
//               opsional meng-archive dulu via mysqldump -archive-dir.
//
// Mode default DRY-RUN (hanya mencetak SQL); tambahkan -apply utk eksekusi.
//
// Contoh (cron mingguan):
//   ./partition-maintain -apply -ahead 2 -retain-months 6
// =====================================================================
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var (
	apply        = flag.Bool("apply", false, "eksekusi ALTER (default dry-run)")
	ahead        = flag.Int("ahead", 2, "partisi bulanan ke depan yang dijamin ada")
	retainMonths = flag.Int("retain-months", 6, "simpan data N bulan terakhir")
	archiveDir   = flag.String("archive-dir", "", "direktori archive dump sebelum DROP (opsional)")
	table        = flag.String("table", "telemetry_logs", "tabel ber-partisi")
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func rootDSN() string {
	// Operasi partisi butuh privilege admin → selalu pakai root.
	return fmt.Sprintf("root:%s@tcp(%s:%s)/?parseTime=true",
		os.Getenv("MYSQL_ROOT_PASSWORD"),
		envOr("MYSQL_HOST", "127.0.0.1"), envOr("MYSQL_PORT", "3307"))
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)

	db, err := sql.Open("mysql", rootDSN())
	must(err)
	defer db.Close()

	dbs := companyDBs(db)
	if len(dbs) == 0 {
		log.Fatal("tidak ada company DB adatrack_gps_* ditemukan")
	}
	log.Printf("company DB: %v", dbs)

	for _, name := range dbs {
		processDB(db, name)
	}
	log.Printf("SELESAI (apply=%v)", *apply)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// companyDBs lists adatrack_gps_% databases excluding master.
func companyDBs(db *sql.DB) []string {
	rows, err := db.Query(`SHOW DATABASES LIKE 'adatrack_gps_%'`)
	must(err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		must(rows.Scan(&n))
		if strings.EqualFold(n, "adatrack_gps_master") {
			continue
		}
		out = append(out, n)
	}
	return out
}

func processDB(db *sql.DB, name string) {
	log.Printf("[%s] mulai", name)

	existing := partitions(db, name)
	now := time.Now().UTC()

	// ---- 1. ENSURE partisi bulanan ke depan ------------------------------
	// Batas partisi baru HARUS > semua batas eksisting (MySQL Error 1493).
	// Bila partisi kuartalan lama berbatas sampai 2027-01-01, partisi
	// bulanan pertama = bulan setelah batas tsb.
	lastEnd := time.Time{}
	for pname := range existing {
		if e := partitionEnd(pname); e.After(lastEnd) {
			lastEnd = e
		}
	}
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if lastEnd.After(start) {
		start = lastEnd // lanjutkan setelah partisi terakhir yang ada
	}
	for i := 0; i <= *ahead+12; i++ {
		first := start.AddDate(0, i, 0)
		if !first.Before(time.Date(now.Year(), now.Month()+time.Month(*ahead)+1, 1, 0, 0, 0, 0, time.UTC)) {
			break
		}
		pname := "p_" + first.Format("2006_01")
		if _, ok := existing[pname]; ok {
			continue
		}
		bound := first.AddDate(0, 1, 0).Format("2006-01-02")
		q := fmt.Sprintf("ALTER TABLE `%s`.`%s` REORGANIZE PARTITION p_future INTO "+
			"(PARTITION `%s` VALUES LESS THAN (TO_DAYS('%s')), PARTITION p_future VALUES LESS THAN MAXVALUE)",
			name, *table, pname, bound)
		run(db, q)
		existing[pname] = true
	}

	// ---- 2. DROP partisi di luar retensi ---------------------------------
	cutoff := now.AddDate(0, -*retainMonths, 0)
	for pname := range existing {
		if !strings.HasPrefix(pname, "p_") || pname == "p_future" {
			continue
		}
		end := partitionEnd(pname)
		if end.IsZero() || end.After(cutoff) {
			continue // masih dalam window retensi
		}
		if *archiveDir != "" {
			archivePartition(name, pname, end)
		}
		run(db, fmt.Sprintf("ALTER TABLE `%s`.`%s` DROP PARTITION `%s`", name, *table, pname))
		log.Printf("[%s] DROP %s (data < %s)", name, pname, cutoff.Format("2006-01"))
	}
}

// partitions returns current partition names of the table.
func partitions(db *sql.DB, schema string) map[string]bool {
	q := `SELECT PARTITION_NAME FROM INFORMATION_SCHEMA.PARTITIONS
	      WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`
	rows, err := db.Query(q, schema, *table)
	must(err)
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n sql.NullString
		must(rows.Scan(&n))
		if n.Valid {
			out[n.String] = true
		}
	}
	return out
}

// run executes or prints SQL depending on -apply.
func run(db *sql.DB, q string) {
	if !*apply {
		log.Printf("  [dry-run] %s", short(q))
		return
	}
	if _, err := db.Exec(q); err != nil {
		log.Printf("  GAGAL: %s → %v", short(q), err)
		return
	}
	log.Printf("  OK: %s", short(q))
}

func short(q string) string {
	if len(q) > 100 {
		return q[:100] + "..."
	}
	return q
}

// partitionEnd parses p_YYYYMM / p_YYYY_Qn into the exclusive end date.
func partitionEnd(name string) time.Time {
	s := strings.TrimPrefix(name, "p_")
	var y int
	if n, _ := fmt.Sscanf(s, "%d_%d", &y, new(int)); n >= 1 {
		var m int
		if _, err := fmt.Sscanf(s, fmt.Sprintf("%d_", y)+"%d", &m); err == nil && m >= 1 && m <= 12 {
			return time.Date(y, time.Month(m)+1, 1, 0, 0, 0, 0, time.UTC)
		}
	}
	if strings.HasSuffix(s, "_Q1") || strings.HasSuffix(s, "_Q2") ||
		strings.HasSuffix(s, "_Q3") || strings.HasSuffix(s, "_Q4") {
		fmt.Sscanf(s, "%d", &y)
		qm := map[string]int{"_Q1": 4, "_Q2": 7, "_Q3": 10, "_Q4": 13}
		for suf, month := range qm {
			if strings.HasSuffix(s, suf) {
				yy := y
				if month == 13 {
					yy++
					month = 1
				}
				return time.Date(yy, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
			}
		}
	}
	return time.Time{}
}

// archivePartition dumps one partition's rows before dropping it.
func archivePartition(schema, pname string, end time.Time) {
	start := strings.TrimPrefix(pname, "p_")
	startTS := start[:4] + "-" + start[5:7] + "-01"
	where := fmt.Sprintf("timestamp >= '%s' AND timestamp < '%s'",
		startTS, end.Format("2006-01-02"))
	out := fmt.Sprintf("%s/%s_%s.sql.gz", *archiveDir, schema, pname)
	inner := fmt.Sprintf(
		`mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --no-create-info --where="%s" %s %s`,
		where, schema, *table)
	script := fmt.Sprintf(`docker exec mysql sh -c %q | gzip > %s`, inner, out)
	cmd := exec.Command("sh", "-c", script)
	if b, err := cmd.CombinedOutput(); err != nil {
		log.Printf("  ARCHIVE gagal (%s): %v — partisi TIDAK di-drop (no silent loss)", out, err)
		log.Printf("  output: %s", string(b))
		return
	}
	log.Printf("  archived → %s", out)
}