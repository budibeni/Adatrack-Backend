package controllers

// alert_fuel_test.go — the B5a fuel-sensor detector (PRD Module 7, FR-7.6):
// config resolution, the ACC gate, the sliding-window drop/refuel arithmetic
// and the per-vehicle override precedence.

import (
	"context"
	"testing"
	"time"

	"adatrack_gps/worker-alert/models"
)

func f64p(v float64) *float64 { return &v }

// TestEvaluateRunsDetectors: evaluate() routes a fuel-bearing message into the
// fuel detector. The sliding window excludes the CURRENT reading, so the swing
// fires on the reading AFTER the pair is stored.
func TestEvaluateRunsDetectors(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	store.inserted = false
	store.fuelCfgs = []models.FuelConfig{{ID: 1, VehicleID: 0, DropThresholdPct: 10, WindowSeconds: 60, Enabled: true}}
	now := time.Now()

	t1 := telem(7)
	t1.FuelLevel = f64p(80)
	t1.Timestamp = now.Unix()
	w.evaluate(context.Background(), t1)

	t2 := telem(7)
	t2.FuelLevel = f64p(50)
	t2.Timestamp = now.Add(time.Second).Unix()
	w.evaluate(context.Background(), t2)

	// The stored window now holds 80→50; the next evaluation fires.
	t3 := telem(7)
	t3.FuelLevel = f64p(50)
	t3.Timestamp = now.Add(2 * time.Second).Unix()
	w.evaluate(context.Background(), t3)

	if len(store.alerts) == 0 {
		t.Fatal("a >10% fuel drop inside the window must raise")
	}
	if store.alerts[0].Type != models.AlertFuelDrop {
		t.Errorf("raised = %s, want fuel_drop", store.alerts[0].Type)
	}
	if store.alerts[0].DedupKey != "fuel:drop:7" {
		t.Errorf("dedup key = %s, want fuel:drop:7", store.alerts[0].DedupKey)
	}
}

// TestFuelDetectorMatrix: config errors, disabled/absent configs, the ACC gate,
// the refuel branch and the per-vehicle override.
func TestFuelDetectorMatrix(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// Config load error → silent skip.
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	store.fuelErr = context.DeadlineExceeded
	msg := telem(7)
	msg.FuelLevel = f64p(50)
	msg.Timestamp = now.Unix()
	w.detFuel(ctx, msg, now)
	if len(store.alerts) != 0 {
		t.Fatalf("config error must skip: %+v", store.alerts)
	}

	// No config for the vehicle → skip.
	store2 := newFakeAlertStore()
	w2, _, _ := newMiniredisWorker(t, store2)
	w2.detFuel(ctx, msg, now)
	if len(store2.alerts) != 0 {
		t.Fatalf("no config must skip: %+v", store2.alerts)
	}

	// Disabled config → skip.
	store3 := newFakeAlertStore()
	w3, _, _ := newMiniredisWorker(t, store3)
	store3.fuelCfgs = []models.FuelConfig{{ID: 1, VehicleID: 0, Enabled: false}}
	w3.detFuel(ctx, msg, now)
	if len(store3.alerts) != 0 {
		t.Fatalf("disabled config must skip: %+v", store3.alerts)
	}

	// ACC gate: RequireACC + ACC off → the reading is shelved, no alert.
	store4 := newFakeAlertStore()
	w4, _, cfg := newMiniredisWorker(t, store4)
	cfg.Fuel.RequireACC = true
	cfg.Fuel.ACCStaleSeconds = 60 // a 0 value would trim the ring to empty
	store4.fuelCfgs = []models.FuelConfig{{ID: 1, VehicleID: 0, DropThresholdPct: 10, WindowSeconds: 60, Enabled: true, RequireACC: true}}
	accOff := telem(7)
	accOff.FuelLevel = f64p(80)
	accOff.ACC = BoolPtr(false)
	accOff.Timestamp = now.Unix()
	w4.detFuel(ctx, accOff, now)
	if len(store4.alerts) != 0 {
		t.Fatalf("ACC gate must shelve the reading: %+v", store4.alerts)
	}
	if len(w4.fuelStash["DEV001:it-imei-7"].accHistory) != 1 {
		t.Error("the shelved reading must be recorded in the ACC history")
	}

	// B6: a frame WITHOUT ACC information must be treated like ACC off by the
	// strict gate (no evidence of a running engine) — never as an inferred ON.
	store4b := newFakeAlertStore()
	w4b, _, cfg4b := newMiniredisWorker(t, store4b)
	cfg4b.Fuel.RequireACC = true
	cfg4b.Fuel.ACCStaleSeconds = 60
	store4b.fuelCfgs = store4.fuelCfgs
	accUnknown := telem(7)
	accUnknown.FuelLevel = f64p(80)
	accUnknown.Timestamp = now.Unix()
	w4b.detFuel(ctx, accUnknown, now)
	if len(store4b.alerts) != 0 {
		t.Fatalf("unreported ACC must be shelved by the strict gate: %+v", store4b.alerts)
	}
	if AccOn(accUnknown.ACC) {
		t.Error("AccOn(nil) must be false")
	}

	// Refuel branch: a sharp rise with a non-zero baseline (the current reading
	// is excluded from the window, so the fire lands on the third reading).
	store5 := newFakeAlertStore()
	w5, _, _ := newMiniredisWorker(t, store5)
	store5.fuelCfgs = []models.FuelConfig{{ID: 1, VehicleID: 0, DropThresholdPct: 0, RefuelThresholdPct: 20, WindowSeconds: 600, Enabled: true}}
	base := telem(7)
	base.FuelLevel = f64p(50)
	base.Timestamp = now.Add(-time.Second).Unix()
	w5.detFuel(ctx, base, now)
	fill := telem(7)
	fill.FuelLevel = f64p(80)
	fill.Timestamp = now.Unix()
	w5.detFuel(ctx, fill, now)
	settle := telem(7)
	settle.FuelLevel = f64p(80)
	settle.Timestamp = now.Add(time.Second).Unix()
	w5.detFuel(ctx, settle, now)
	if len(store5.alerts) != 1 || store5.alerts[0].Type != models.AlertRefuel {
		t.Fatalf("refuel = %+v, want one refuel alert", store5.alerts)
	}
	if store5.alerts[0].DedupKey != "fuel:refuel:7" {
		t.Errorf("refuel dedup = %s, want fuel:refuel:7", store5.alerts[0].DedupKey)
	}

	// Per-vehicle override wins over the tenant default.
	store6 := newFakeAlertStore()
	w6, _, _ := newMiniredisWorker(t, store6)
	store6.fuelCfgs = []models.FuelConfig{
		{ID: 1, VehicleID: 0, DropThresholdPct: 50, WindowSeconds: 600, Enabled: true},
		{ID: 2, VehicleID: 7, DropThresholdPct: 10, WindowSeconds: 600, Enabled: true},
	}
	d1 := telem(7)
	d1.FuelLevel = f64p(80)
	d1.Timestamp = now.Add(-time.Second).Unix()
	w6.detFuel(ctx, d1, now)
	d2 := telem(7)
	d2.FuelLevel = f64p(50)
	d2.Timestamp = now.Unix()
	w6.detFuel(ctx, d2, now)
	d3 := telem(7)
	d3.FuelLevel = f64p(50) // stored window holds 80→50: 37.5% breaches only the per-vehicle 10%
	d3.Timestamp = now.Add(time.Second).Unix()
	w6.detFuel(ctx, d3, now)
	if len(store6.alerts) != 1 || store6.alerts[0].Type != models.AlertFuelDrop {
		t.Fatalf("per-vehicle override = %+v, want one fuel_drop", store6.alerts)
	}
}
