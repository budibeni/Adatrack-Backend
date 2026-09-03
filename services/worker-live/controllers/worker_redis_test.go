package controllers

import (
	"encoding/json"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/worker-live/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
)

// newTestRedis membuat miniredis (in-memory) + internal.RedisClient yang
// menunjuk ke-nya, lalu meng-Configure global redCli (natsCli nil).
func newTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	red, err := internal.NewRedisClient(&internal.Config{
		Redis: struct {
			Addr      string
			Password  string
			DB        int
			PoolSize  int
			KeyPrefix string
		}{Addr: mr.Addr(), PoolSize: 2},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient(miniredis): %v", err)
	}
	Configure(red, nil)
	t.Cleanup(func() {
		if cancel != nil {
			cancel()
		}
	})
	return mr
}

// ---------------------------------------------------------------------
// RegisterMetrics — memastikan collector worker-live terdaftar.
// ---------------------------------------------------------------------
func TestRegisterMetricsRegistersCollectors(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterMetrics(reg) // tidak boleh double-register / panic
	mf, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(mf) == 0 {
		t.Fatal("registry harus berisi minimal 1 metric")
	}
}

// ---------------------------------------------------------------------
// mergeFuelState — B5a partial fuel merge (FR-7.6 konteks ACC dipertahankan).
// ---------------------------------------------------------------------
func TestMergeFuelStatePreservesPositionAndStatus(t *testing.T) {
	mr := newTestRedis(t)

	key := "adatrack_gps:dev001:vehicle:state:123456789012345"
	acc := true
	existing := models.LiveState{
		IMEI: "123456789012345", CompanyCode: "dev001",
		Lat: -6.2, Lon: 106.8, Speed: 33, Heading: 90,
		Status: "ONLINE", LastSeen: time.Now().Unix(), ACC: &acc,
	}
	b, _ := json.Marshal(existing)
	mr.Set(key, string(b))

	fuel := 47.5
	st := mergeFuelState(key, models.TelemetryMessage{
		IMEI: "123456789012345", CompanyCode: "dev001", FuelLevel: &fuel,
	})

	if st.Lat != -6.2 || st.Speed != 33 {
		t.Errorf("posisi tergantikan: %+v", st)
	}
	if st.FuelLevel == nil || *st.FuelLevel != 47.5 {
		t.Errorf("fuel harus 47.5, dapat %+v", st.FuelLevel)
	}
	if st.Status != "ONLINE" {
		t.Errorf("status existing harus dipertahankan, dapat %q", st.Status)
	}
	if st.ACC == nil || *st.ACC != true {
		t.Errorf("ACC existing harus dipertahankan, dapat %+v", st.ACC)
	}
}

func TestMergeFuelStateFreshStateWhenNoExisting(t *testing.T) {
	newTestRedis(t)

	fuel := 70.0
	st := mergeFuelState("adatrack_gps:c:vehicle:state:999", models.TelemetryMessage{
		IMEI: "999", CompanyCode: "C", FuelLevel: &fuel,
	})
	if st.FuelLevel == nil || *st.FuelLevel != 70 {
		t.Errorf("fuel baru tidak ke-set: %+v", st.FuelLevel)
	}
	if st.Status != "IDLE" {
		t.Errorf("fresh state speed 0 harus IDLE, dapat %q", st.Status)
	}
}

func TestMergeFuelStateInvalidExistingResets(t *testing.T) {
	mr := newTestRedis(t)

	key := "adatrack_gps:d:vehicle:state:5"
	mr.Set(key, "{{{bukan-json")
	fuel := 33.3
	st := mergeFuelState(key, models.TelemetryMessage{
		IMEI: "5", CompanyCode: "D", FuelLevel: &fuel,
	})
	if st.FuelLevel == nil || *st.FuelLevel != 33.3 {
		t.Errorf("fuel harus tetap set walau existing rusak: %+v", st.FuelLevel)
	}
}