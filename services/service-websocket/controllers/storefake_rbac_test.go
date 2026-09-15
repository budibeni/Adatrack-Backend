package controllers

import (
	"context"
	"sort"
	"strings"

	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"
)

// TenantAccess implements Store.
func (f *fakeStore) TenantAccess(_ context.Context, companyCode string, userID int64) (string, bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows, ok := f.access[strings.ToUpper(companyCode)]
	if !ok {
		return "", false, false, nil
	}
	row, ok := rows[userID]
	if !ok {
		return "", false, false, nil
	}
	return row.roleOverride, row.isActive, true, nil
}

// UpsertTenantAccess implements Store.
func (f *fakeStore) UpsertTenantAccess(_ context.Context, companyCode string, userID int64, role string) error {
	f.addAccess(companyCode, userID, role, true)
	return nil
}

// AssignedVehicleIDs implements Store.
func (f *fakeStore) AssignedVehicleIDs(_ context.Context, companyCode string, userID int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.assigned[strings.ToUpper(companyCode)]
	if rows == nil {
		return nil, nil
	}
	return sortedIDs(rows[userID]), nil
}

// AssignVehicles implements Store.
func (f *fakeStore) AssignVehicles(_ context.Context, companyCode string, userID int64, vehicleIDs []int64) error {
	f.addAssignment(companyCode, userID, vehicleIDs...)
	return nil
}

// ExistingVehicleIDs implements Store.
func (f *fakeStore) ExistingVehicleIDs(_ context.Context, companyCode string, ids []int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	known := map[int64]bool{}
	for _, v := range f.vehicles[strings.ToUpper(companyCode)] {
		known[v.ID] = true
	}
	var out []int64
	for _, id := range ids {
		if known[id] {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out, nil
}

// ListVehicles implements Store (mirrors the row-level rules of the SQL path).
func (f *fakeStore) ListVehicles(_ context.Context, q VehicleQuery) ([]models.Vehicle, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.vehicles[strings.ToUpper(q.CompanyCode)]

	allowed := map[int64]bool{}
	for _, id := range q.AssignedIDs {
		allowed[id] = true
	}
	var filtered []models.Vehicle
	for _, v := range rows {
		if !q.AllVehicles && !allowed[v.ID] {
			continue
		}
		if q.Status != "" && v.Status != q.Status {
			continue
		}
		if q.Search != "" && !strings.Contains(strings.ToLower(v.PlateNumber), strings.ToLower(q.Search)) {
			continue
		}
		filtered = append(filtered, v)
	}
	total := int64(len(filtered))
	start := (q.Page - 1) * q.Limit
	if start >= len(filtered) {
		return []models.Vehicle{}, total, nil
	}
	end := start + q.Limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return append([]models.Vehicle{}, filtered[start:end]...), total, nil
}

// VehicleByID implements Store.
func (f *fakeStore) VehicleByID(_ context.Context, companyCode string, id int64, includeDeleted bool) (*models.Vehicle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.vehicles[strings.ToUpper(companyCode)] {
		if v.ID != id {
			continue
		}
		if v.DeletedAt != nil && !includeDeleted {
			return nil, nil
		}
		clone := v
		return &clone, nil
	}
	return nil, nil
}

// VehicleHistory implements Store.
func (f *fakeStore) VehicleHistory(_ context.Context, q HistoryQuery) ([]models.Position, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.positions[q.IMEI]
	total := int64(len(rows))
	start := (q.Page - 1) * q.Limit
	if start >= len(rows) {
		return []models.Position{}, total, nil
	}
	end := start + q.Limit
	if end > len(rows) {
		end = len(rows)
	}
	return append([]models.Position{}, rows[start:end]...), total, nil
}

// VehicleMeta implements Store.
func (f *fakeStore) VehicleMeta(ctx context.Context, companyCode string, id int64) (models.Vehicle, error) {
	v, err := f.VehicleByID(ctx, companyCode, id, false)
	if err != nil || v == nil {
		return models.Vehicle{}, err
	}
	return *v, nil
}

// ProvisionTenant implements Store.
func (f *fakeStore) ProvisionTenant(_ context.Context, opts tenant.ProvisionOptions) (*tenant.ProvisionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provisionErr != nil {
		return nil, f.provisionErr
	}
	f.provisions = append(f.provisions, opts)
	f.companies[strings.ToUpper(opts.Code)] = true
	return &tenant.ProvisionResult{
		CompanyCode:  strings.ToUpper(opts.Code),
		Schema:       "adatrack_gps_" + strings.ToLower(opts.Code),
		BusinessType: opts.BusinessType,
		Created:      true,
	}, nil
}

// PingTenant implements Store.
func (f *fakeStore) PingTenant(context.Context, string) error { return nil }

// Readiness implements ReadinessStore (healthz).
func (f *fakeStore) Readiness(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readinessErr
}

// newTestStore builds a store with the B2 fixture: a DEV001 tenant with an admin,
// an operator (vehicles 1-2) and a driver (vehicle 3), plus a platform SuperAdmin.
func newTestStore() *fakeStore {
	f := newFakeStore()
	f.addUser(1, "DEV001", "admin@dev001.io", models.RoleAdmin, "Admin@123", false)
	f.addUser(3, "DEV001", "operator@dev001.io", models.RoleOperator, "Admin@123", false)
	f.addUser(4, "DEV001", "driver@dev001.io", models.RoleDriver, "Admin@123", false)
	f.addUser(9, models.PlatformCompanyCode, "platform@adatrackgps.local", models.RoleSuperAdmin, "Platform@123", false)
	f.addUser(10, "DEV001", "newadmin@dev001.io", models.RoleAdmin, "Admin@123", true)

	f.addAccess("DEV001", 1, models.RoleAdmin, true)
	f.addAccess("DEV001", 3, models.RoleOperator, true)
	f.addAccess("DEV001", 4, models.RoleDriver, true)
	f.addAccess("DEV001", 10, models.RoleAdmin, true)

	f.addAssignment("DEV001", 3, 1, 2)
	f.addAssignment("DEV001", 4, 3)

	f.addVehicle("DEV001", models.Vehicle{ID: 1, IMEI: "864201040512345", PlateNumber: "B 1234 XYZ", Status: "active"})
	f.addVehicle("DEV001", models.Vehicle{ID: 2, IMEI: "864201040512346", PlateNumber: "D 5678 ABC", Status: "active"})
	f.addVehicle("DEV001", models.Vehicle{ID: 3, IMEI: "864201040512347", PlateNumber: "E 9012 RST", Status: "maintenance"})

	f.companies["DEV001"] = true
	f.companies[models.PlatformCompanyCode] = true
	return f
}
