package controllers

import (
	"context"

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
