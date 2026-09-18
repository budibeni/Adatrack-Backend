package controllers

import (
	"context"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// fakeStore satisfies Store; every unimplemented method panics via the embedded
// interface, so a test only stubs what its handler actually touches.
type fakeStore struct {
	Store

	vehicles map[int64]*models.Vehicle
	alerts   map[int64]*models.Alert

	createdVehicle bool
	updatedVehicle bool
	deletedVehicle bool
	ackedRows      int64
	imeiExists     bool

	// --- B5a fuel -----------------------------------------------------------
	// fuelConfigs keeps insertion order so list assertions stay deterministic.
	fuelConfigs  []*models.FuelConfig
	fuelLogs     []models.FuelLog
	fuelHistoryN int64
	fuelFrom     time.Time
	fuelTo       time.Time
	fuelPage     int
	fuelLimit    int
	createdFuel  bool
	updatedFuel  bool
	deletedFuel  bool
	restoredFuel bool

	// listVehicles is what ListVehicles returns (nil when unset).
	listVehicles []models.Vehicle
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		vehicles: map[int64]*models.Vehicle{},
		alerts:   map[int64]*models.Alert{},
	}
}

func (f *fakeStore) seedVehicle(v *models.Vehicle) { f.vehicles[v.ID] = v }

func (f *fakeStore) seedAlert(a *models.Alert) { f.alerts[a.ID] = a }

// --- vehicles -------------------------------------------------------------

func (f *fakeStore) VehicleByID(_ context.Context, _ string, id int64, includeDeleted bool) (*models.Vehicle, error) {
	v, ok := f.vehicles[id]
	if !ok {
		return nil, nil
	}
	if v.DeletedAt != nil && !includeDeleted {
		return nil, nil
	}
	return v, nil
}

func (f *fakeStore) IMEIExists(_ context.Context, _, _ string, _ int64) (bool, error) {
	return f.imeiExists, nil
}

func (f *fakeStore) CreateVehicle(_ context.Context, _ string, v *models.Vehicle, _ int64) (int64, error) {
	f.createdVehicle = true
	v.ID = int64(len(f.vehicles) + 1)
	f.vehicles[v.ID] = v
	return v.ID, nil
}

func (f *fakeStore) UpdateVehicle(_ context.Context, _ string, v *models.Vehicle, _ int64) error {
	f.updatedVehicle = true
	f.vehicles[v.ID] = v
	return nil
}

func (f *fakeStore) SoftDeleteVehicle(_ context.Context, _ string, id, _ int64, _ string) error {
	f.deletedVehicle = true
	if v, ok := f.vehicles[id]; ok {
		deleted := "2026-09-16T00:00:00Z"
		v.DeletedAt = &deleted
	}
	return nil
}

func (f *fakeStore) SyncIMEIMap(_ context.Context, _, _ string, _ int64) error { return nil }

func (f *fakeStore) SoftDeleteIMEIMap(_ context.Context, _, _ string) error { return nil }

// --- alerts ---------------------------------------------------------------

func (f *fakeStore) AlertByID(_ context.Context, _ string, id int64) (*models.Alert, error) {
	a, ok := f.alerts[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}

func (f *fakeStore) AcknowledgeAlert(_ context.Context, _ string, id, _ int64) (int64, error) {
	if a, ok := f.alerts[id]; ok && a.Status == "open" {
		a.Status = "acknowledged"
		return 1, nil
	}
	return f.ackedRows, nil
}

func (f *fakeStore) ResolveAlert(_ context.Context, _ string, id, _ int64) (int64, error) {
	if a, ok := f.alerts[id]; ok && a.Status != "resolved" {
		a.Status = "resolved"
		return 1, nil
	}
	return 0, nil
}

// --- vehicles (list, B5a live overlay) ------------------------------------

func (f *fakeStore) ListVehicles(_ context.Context, _ VehicleQuery) ([]models.Vehicle, int64, error) {
	out := f.listVehicles
	if out == nil {
		out = []models.Vehicle{}
	}
	return out, int64(len(out)), nil
}

func (f *fakeStore) seedListedVehicles(vs ...models.Vehicle) { f.listVehicles = vs }

// --- fuel configs + history (B5a) ----------------------------------------

func (f *fakeStore) seedFuelConfig(fc *models.FuelConfig) {
	f.fuelConfigs = append(f.fuelConfigs, fc)
}

func (f *fakeStore) ListFuelConfigs(_ context.Context, _ string, includeDeleted bool) ([]models.FuelConfig, error) {
	out := []models.FuelConfig{}
	for _, fc := range f.fuelConfigs {
		if fc.DeletedAt != nil && !includeDeleted {
			continue
		}
		out = append(out, *fc)
	}
	return out, nil
}

func (f *fakeStore) FuelConfigByID(_ context.Context, _ string, id int64, includeDeleted bool) (*models.FuelConfig, error) {
	for _, fc := range f.fuelConfigs {
		if fc.ID != id {
			continue
		}
		if fc.DeletedAt != nil && !includeDeleted {
			return nil, nil
		}
		return fc, nil
	}
	return nil, nil
}

func (f *fakeStore) CreateFuelConfig(_ context.Context, _ string, fc *models.FuelConfig, _ int64) (int64, error) {
	f.createdFuel = true
	fc.ID = int64(len(f.fuelConfigs) + 1)
	f.fuelConfigs = append(f.fuelConfigs, fc)
	return fc.ID, nil
}

func (f *fakeStore) UpdateFuelConfig(_ context.Context, _ string, fc *models.FuelConfig, _ int64) error {
	f.updatedFuel = true
	for i, cur := range f.fuelConfigs {
		if cur.ID == fc.ID {
			f.fuelConfigs[i] = fc
			return nil
		}
	}
	return nil
}

func (f *fakeStore) SoftDeleteFuelConfig(_ context.Context, _ string, id, _ int64, _ string) error {
	f.deletedFuel = true
	for _, fc := range f.fuelConfigs {
		if fc.ID == id {
			at := "2026-09-16T00:00:00Z"
			fc.DeletedAt = &at
		}
	}
	return nil
}

func (f *fakeStore) RestoreFuelConfig(_ context.Context, _ string, id int64) error {
	f.restoredFuel = true
	for _, fc := range f.fuelConfigs {
		if fc.ID == id {
			fc.DeletedAt = nil
		}
	}
	return nil
}

func (f *fakeStore) ListFuelHistory(_ context.Context, _ string, _ int64, from, to time.Time, page, limit int) ([]models.FuelLog, int64, error) {
	f.fuelFrom, f.fuelTo, f.fuelPage, f.fuelLimit = from, to, page, limit
	out := f.fuelLogs
	if len(out) > limit {
		out = out[:limit]
	}
	return out, f.fuelHistoryN, nil
}
