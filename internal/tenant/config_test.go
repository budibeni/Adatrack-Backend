package tenant

import (
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnvCacheDefaultsAndPool(t *testing.T) {
	for _, k := range []string{"TENANT_CACHE_KEY_PREFIX", "TENANT_CACHE_TTL_SEC",
		"MYSQL_POOL_MIN", "MYSQL_POOL_MAX", "MYSQL_CONN_MAX_LIFETIME_MIN"} {
		t.Setenv(k, "")
	}
	cfg := NewConfigFromEnv()
	if cfg.CachePrefix != defaultCachePrefix {
		t.Errorf("cache prefix default = %q", cfg.CachePrefix)
	}
	if cfg.CacheTTL != 900*time.Second {
		t.Errorf("cache TTL default = %v", cfg.CacheTTL)
	}
	if cfg.PoolMin != 10 || cfg.PoolMax != 50 {
		t.Errorf("pool default = %d/%d", cfg.PoolMin, cfg.PoolMax)
	}
	if cfg.ConnMaxLifetime != 30*time.Minute {
		t.Errorf("conn lifetime default = %v", cfg.ConnMaxLifetime)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "mysql") // jalur mysql eksplisit
	t.Setenv("MASTER_DB_HOST", "db.internal")
	t.Setenv("MASTER_DB_PORT", "3307")
	t.Setenv("MYSQL_POOL_MIN", "20")
	t.Setenv("MYSQL_POOL_MAX", "50")
	cfg := NewConfigFromEnv()
	if cfg.MasterHost != "db.internal" || cfg.MasterPort != "3307" {
		t.Errorf("override env tidak terbaca: %+v", cfg)
	}
	if cfg.PoolMin != 20 || cfg.PoolMax != 50 {
		t.Errorf("pool override salah: %d/%d", cfg.PoolMin, cfg.PoolMax)
	}
}

// Default proyek sejak 2026-08-25: DATABASE_PROVIDER tak diset ⇒ postgres.
func TestConfigFromEnvPostgresDefault(t *testing.T) {
	for _, k := range []string{"DATABASE_PROVIDER", "DATABASE_URL",
		"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER"} {
		t.Setenv(k, "")
	}
	cfg := NewConfigFromEnv()
	if cfg.Provider != "postgres" {
		t.Fatalf("provider default = %q, want postgres", cfg.Provider)
	}
	if cfg.PostgresHost != "127.0.0.1" || cfg.PostgresPort != "5432" {
		t.Errorf("postgres host/port default salah: %s:%s", cfg.PostgresHost, cfg.PostgresPort)
	}
	dsn := cfg.MasterDSN()
	if !strings.HasPrefix(dsn, "postgres://") || !strings.Contains(dsn, "search_path=") {
		t.Errorf("master DSN bukan postgres search_path: %s", dsn)
	}
}

func TestCompanyDBNameTrimsAndLowers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Logi002 ", "adatrack_gps_logi002"}, // trim + lowercase
		{"X", "adatrack_gps_x"},
		{"DEV001", "adatrack_gps_dev001"},
	}
	for _, c := range cases {
		if got := CompanyDBName("adatrack_gps_", c.in); got != c.want {
			t.Errorf("CompanyDBName(%q) = %q, ingin %q", c.in, got, c.want)
		}
	}
	cfg := Config{CompanyDBPrefix: "adatrack_gps_"}
	if got := cfg.CompanyDBName("DEF001"); got != "adatrack_gps_def001" {
		t.Errorf("cfg.CompanyDBName = %q", got)
	}
}

func TestDSNIncludesTimeouts(t *testing.T) {
	cfg := Config{
		MasterHost: "127.0.0.1", MasterPort: "3307",
		MasterUser: "root", MasterPassword: "secret",
		MasterName:     "adatrack_gps_master",
		ConnectTimeout: 5 * time.Second,
		ReadTimeout:    3 * time.Second,
		WriteTimeout:   4 * time.Second,
	}
	dsn := cfg.MasterDSN()
	for _, want := range []string{"timeout=5s", "readTimeout=3s", "writeTimeout=4s"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN tidak memuat %q: %s", want, dsn)
		}
	}
	dbDsn := cfg.DBDSN("adatrack_gps_def001")
	if !strings.Contains(dbDsn, "/adatrack_gps_def001?") {
		t.Errorf("DBDSN salah: %s", dbDsn)
	}
}

func TestValidatePoolAndPrefixErrors(t *testing.T) {
	valid := Config{
		MasterHost: "h", MasterPort: "3306", MasterName: "m",
		CompanyDBPrefix: "adatrack_gps_", PoolMin: 10, PoolMax: 50,
	}
	badPrefix := valid
	badPrefix.CompanyDBPrefix = "adatrackgps" // tanpa trailing '_'
	if err := badPrefix.Validate(); err == nil || !strings.Contains(err.Error(), "COMPANY_DB_PREFIX") {
		t.Errorf("prefix tanpa '_' harus error, dapat %v", err)
	}

	badPool := valid
	badPool.PoolMin, badPool.PoolMax = 60, 50 // min > max
	if err := badPool.Validate(); err == nil || !strings.Contains(err.Error(), "pool sizing") {
		t.Errorf("min>max harus error pool sizing, dapat %v", err)
	}

	negPool := valid
	negPool.PoolMin = -1
	if err := negPool.Validate(); err == nil {
		t.Error("pool min negatif harus error")
	}

	zeroMax := valid
	zeroMax.PoolMax = 0
	if err := zeroMax.Validate(); err == nil {
		t.Error("pool max 0 harus error")
	}
}
