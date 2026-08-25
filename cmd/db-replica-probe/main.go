// Command db-replica-probe membuktikan read/write split APP-LEVEL (B4 HA)
// secara LIVE terhadap infrastruktur nyata, memakai kode TenantManager yang
// sama dengan service produksi:
//
//	jalur READ  (Manager.ReadPool / ReadRouter) → harus melayani dari REPLICA
//	jalur WRITE (Manager.DB / ReadRouter.Exec)  → selalu PRIMARY
//
// Bukti peran server sadar-dialek: PostgreSQL memakai pg_is_in_recovery(),
// MySQL memakai @@read_only (replika compose-ha di-start --read-only=ON).
//
// Metrik routing db_read_queries_total{route} di-dump sebagai bukti tambahan.
//
// Pemakaian (CWD di dalam tree backend agar .env ditemukan loader):
//
//	cd backend/cmd/db-replica-probe && go run .
//	db-replica-probe -company DEV001   # paksa tenant tertentu
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	want := flag.String("company", "", "company_code target (default: tenant aktif pertama)")
	flag.Parse()

	internal.ConfigureLogging()
	internal.LoadProjectEnv() // OS env > backend/.env

	cfg := tenant.NewConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "❌ config invalid:", err)
		os.Exit(2)
	}
	if !cfg.ReplicaEnabled {
		fmt.Fprintln(os.Stderr, "❌ DB_REPLICA_ENABLED=false — set true untuk menguji routing replica")
		os.Exit(2)
	}

	reg := prometheus.NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tm, err := tenant.New(ctx, cfg, nil, reg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ tenant manager:", err)
		os.Exit(1)
	}
	defer tm.Close()

	code := strings.ToUpper(strings.TrimSpace(*want))
	if code == "" {
		for _, c := range tm.Companies() {
			if c.IsActive && !tenant.IsPlatformCompany(c.Code) {
				code = strings.ToUpper(c.Code)
				break
			}
		}
	}
	if code == "" {
		fmt.Fprintln(os.Stderr, "❌ tidak ada tenant aktif untuk diuji")
		os.Exit(1)
	}
	fmt.Printf("Tenant uji      : %s\n", code)

	// Cek peran server SADAR-DIALEK: kedua engine menjawab string
	// 'REPLICA' | 'PRIMARY' sehingga satu jalur kode memuat dua provider.
	//   postgres : pg_is_in_recovery()
	//   mysql    : @@read_only (compose-ha men-set --read-only=ON di replika)
	roleQuery := `SELECT CASE WHEN pg_is_in_recovery() THEN 'REPLICA' ELSE 'PRIMARY' END`
	if cfg.Provider != "postgres" {
		roleQuery = `SELECT IF(@@read_only, 'REPLICA', 'PRIMARY')`
	}

	repHost, repPort := cfg.ReplicaEndpoint()
	priHost := cfg.MasterHost
	if cfg.Provider == "postgres" {
		priHost = cfg.PostgresHost
	}
	fmt.Printf("Engine          : %s\n", cfg.Provider)
	fmt.Printf("PRIMARY endpoint : %s:%s\n", priHost, primaryPort(cfg))
	fmt.Printf("REPLICA endpoint : %s:%s\n\n", repHost, repPort)

	fail := 0

	// ---- 1) Jalur READ via Manager.ReadPool → harus REPLICA ----
	readDB, err := tm.ReadPool(code)
	if err != nil {
		fmt.Println("❌ READ  ReadPool:", err)
		fail++
	} else {
		var role string
		if perr := readDB.QueryRowContext(ctx, roleQuery).Scan(&role); perr != nil {
			fmt.Println("❌ READ  cek peran server:", perr)
			fail++
		} else if role == "REPLICA" {
			fmt.Println("✅ READ  Manager.ReadPool        → REPLICA")
		} else {
			fmt.Println("⚠️  READ  Manager.ReadPool        → PRIMARY (replika tidak terpakai — cek warm-up/breaker)")
			fail++
		}
	}

	// ---- 2) Jalur READ via ReadRouter.QueryRow → harus REPLICA ----
	router, rerr := tm.ReadRouter(code)
	if rerr != nil {
		fmt.Println("❌ READ  ReadRouter:", rerr)
		fail++
	} else {
		var role string
		if qerr := router.QueryRowContext(ctx, roleQuery).Scan(&role); qerr != nil {
			fmt.Println("❌ READ  router query:", qerr)
			fail++
		} else if role == "REPLICA" {
			fmt.Println("✅ READ  ReadRouter.QueryRow     → REPLICA")
		} else {
			fmt.Println("⚠️  READ  ReadRouter.QueryRow     → PRIMARY (fallback aktif?)")
			fail++
		}

		// ---- 3) Jalur WRITE (router.Primary()/Exec) → selalu PRIMARY ----
		wdb := router.Primary()
		switch {
		case wdb == nil:
			fmt.Println("❌ WRITE Primary(): nil")
			fail++
		default:
			var wrole string
			if werr := wdb.QueryRowContext(ctx, roleQuery).Scan(&wrole); werr != nil {
				fmt.Println("❌ WRITE cek peran server:", werr)
				fail++
			} else if wrole != "PRIMARY" {
				fmt.Printf("❌ WRITE jalur tulis mendarat di %s — kontrak dilanggar!\n", wrole)
				fail++
			} else {
				fmt.Println("✅ WRITE router.Primary()/Exec    → PRIMARY")
			}
		}
	}

	// ---- 4) Dump metrik routing ----
	time.Sleep(200 * time.Millisecond) // beri waktu counter tersinkron
	mfs, _ := reg.Gather()
	routeCounts := map[string]float64{}
	for _, mf := range mfs {
		if mf.GetName() != "db_read_queries_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			route := ""
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "route" {
					route = lp.GetValue()
				}
			}
			routeCounts[route] += m.GetCounter().GetValue()
		}
	}
	fmt.Printf("\nMetrik db_read_queries_total: replica=%.0f primary=%.0f primary_fallback=%.0f\n",
		routeCounts["replica"], routeCounts["primary"], routeCounts["primary_fallback"])
	if routeCounts["replica"] < 1 {
		fmt.Println("⚠️  tidak ada query yang tercatat lewat REPLICA")
	}

	if fail > 0 {
		fmt.Printf("\n❌ PROBE GAGAL (%d check)\n", fail)
		os.Exit(1)
	}
	fmt.Println("\n✅ PROBE LOLOS — READ→REPLICA, WRITE→PRIMARY terverifikasi live")
}

func primaryPort(cfg tenant.Config) string {
	if cfg.Provider == "postgres" {
		return cfg.PostgresPort
	}
	return cfg.MasterPort
}