package tenant

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "mysql") // jalur mysql eksplisit
	t.Setenv("MASTER_DB_HOST", "db.internal")
	t.Setenv("MASTER_DB_PORT", "3306")
	t.Setenv("MASTER_DB_USER", "adatrack_app")
	t.Setenv("MASTER_DB_PASSWORD", "sekret")
	t.Setenv("MASTER_DB_NAME", "adatrack_gps_master")
	t.Setenv("COMPANY_DB_PREFIX", "adatrack_gps_")
	t.Setenv("MYSQL_POOL_MIN", "20")
	t.Setenv("MYSQL_POOL_MAX", "50")

	cfg := NewConfigFromEnv()
	if cfg.Provider != "mysql" {
		t.Fatalf("provider = %s, want mysql (eksplisit)", cfg.Provider)
	}
	if cfg.MasterHost != "db.internal" || cfg.MasterName != "adatrack_gps_master" {
		t.Fatalf("unexpected master config: %+v", cfg)
	}
	if cfg.PoolMin != 20 || cfg.PoolMax != 50 {
		t.Fatalf("unexpected pool sizing: min=%d max=%d", cfg.PoolMin, cfg.PoolMax)
	}
	if cfg.CompanyDBPrefix != "adatrack_gps_" {
		t.Fatalf("unexpected prefix: %s", cfg.CompanyDBPrefix)
	}
	if !strings.Contains(cfg.MasterDSN(), "@tcp(db.internal:3306)/adatrack_gps_master?") {
		t.Fatalf("unexpected master DSN: %s", cfg.MasterDSN())
	}
}

func TestCompanyDBNameLowercaseConvention(t *testing.T) {
	cfg := NewConfigFromEnv()
	cases := map[string]string{
		"DEV001":  "adatrack_gps_dev001",
		"ABLE01":  "adatrack_gps_able01",
		"dev001":  "adatrack_gps_dev001",
		"  ab   ": "adatrack_gps_ab",
	}
	for code, want := range cases {
		if got := cfg.CompanyDBName(code); got != want {
			t.Errorf("CompanyDBName(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	cfg := NewConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}

	bad := cfg
	bad.CompanyDBPrefix = "adatrackgps" // tidak diakhiri '_'
	if err := bad.Validate(); err == nil {
		t.Error("expected error for prefix without trailing underscore")
	}

	bad = cfg
	bad.PoolMin, bad.PoolMax = 50, 10
	if err := bad.Validate(); err == nil {
		t.Error("expected error for inverted pool sizing")
	}

	bad = cfg
	bad.Provider = "mysql"
	bad.MasterName = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty MASTER_DB_NAME (provider mysql)")
	}

	bad = cfg
	bad.PostgresUser = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty POSTGRES_USER (provider postgres)")
	}
}

// TestDeviceInfoJSONRoundTrip ensures the Redis cache value (JSON) is stable.
func TestDeviceInfoJSONRoundTrip(t *testing.T) {
	di := DeviceInfo{CompanyCode: "DEV001", VehicleID: 7}
	data, err := json.Marshal(di)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got DeviceInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != di {
		t.Fatalf("round trip = %+v, want %+v", got, di)
	}
}

// TestDSNContainsRequiredParams guards against losing parseTime / timeouts.
func TestDSNContainsRequiredParams(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "mysql") // DSN go-sql-driver hanya utk jalur mysql
	cfg := NewConfigFromEnv()
	dsn := cfg.MasterDSN()
	for _, part := range []string{"parseTime=true", "timeout=", "readTimeout=", "writeTimeout=", "charset=utf8mb4"} {
		if !strings.Contains(dsn, part) {
			t.Errorf("DSN missing %q: %s", part, dsn)
		}
	}
}
