package controllers

// Fleet + alert sections of the in-memory Store fake. The state fields live in
// fake_store_test.go so the whole fake stays ONE type (methods may be split
// across files of the same package).

import (
	"context"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// --- geofences -------------------------------------------------------------

// seedGeofence registers a zone as if it were already stored.
func (f *fakeStore) seedGeofence(g *models.Geofence) { f.geofences = append(f.geofences, g) }

// findGeofence returns the stored zone (nil when absent or filtered out).
func (f *fakeStore) findGeofence(id int64, includeDeleted bool) *models.Geofence {
	for _, g := range f.geofences {
		if g.ID != id {
			continue
		}
		if g.DeletedAt != nil && !includeDeleted {
			return nil
		}
		return g
	}
	return nil
}

func (f *fakeStore) ListGeofences(_ context.Context, _ GeofenceQuery) ([]models.Geofence, int64, error) {
	out := []models.Geofence{}
	for _, g := range f.geofences {
		out = append(out, *g)
	}
	return out, int64(len(out)), nil
}

func (f *fakeStore) GeofenceByID(_ context.Context, _ string, id int64, includeDeleted bool) (*models.Geofence, error) {
	return f.findGeofence(id, includeDeleted), nil
}

func (f *fakeStore) GeofenceNameExists(_ context.Context, _, name string, excludeID int64) (bool, error) {
	for _, g := range f.geofences {
		if g.ID != excludeID && g.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CreateGeofence(_ context.Context, _ string, g *models.Geofence, _ int64) (int64, error) {
	f.createdGeofence = true
	g.ID = int64(len(f.geofences) + 1)
	f.geofences = append(f.geofences, g)
	return g.ID, nil
}

func (f *fakeStore) UpdateGeofence(_ context.Context, _ string, g *models.Geofence, _ int64) error {
	f.updatedGeofence = true
	for i, cur := range f.geofences {
		if cur.ID == g.ID {
			f.geofences[i] = g
			return nil
		}
	}
	return nil
}

func (f *fakeStore) SoftDeleteGeofence(_ context.Context, _ string, id, _ int64, _ string) error {
	f.deletedGeofence = true
	if g := f.findGeofence(id, true); g != nil {
		at := time.Now().UTC().Format(time.RFC3339)
		g.DeletedAt = &at
	}
	return nil
}

func (f *fakeStore) RestoreGeofence(_ context.Context, _ string, id int64) error {
	f.restoredGeofence = true
	if g := f.findGeofence(id, true); g != nil {
		g.DeletedAt = nil
	}
	return nil
}

func (f *fakeStore) ReplaceGeofenceVehicles(_ context.Context, _ string, geofenceID int64, vehicleIDs []int64, _ int64) error {
	f.replacedGeofenceVehicles = true
	if g := f.findGeofence(geofenceID, true); g != nil {
		g.VehicleIDs = vehicleIDs
	}
	return nil
}

// --- routes ----------------------------------------------------------------

// seedRoute registers a route as if it were already stored.
func (f *fakeStore) seedRoute(r *models.Route) { f.routes = append(f.routes, r) }

// findRoute returns the stored route (nil when absent or filtered out).
func (f *fakeStore) findRoute(id int64, includeDeleted bool) *models.Route {
	for _, r := range f.routes {
		if r.ID != id {
			continue
		}
		if r.DeletedAt != nil && !includeDeleted {
			return nil
		}
		return r
	}
	return nil
}

func (f *fakeStore) ListRoutes(_ context.Context, _ RouteQuery) ([]models.Route, int64, error) {
	out := []models.Route{}
	for _, r := range f.routes {
		out = append(out, *r)
	}
	return out, int64(len(out)), nil
}

func (f *fakeStore) RouteByID(_ context.Context, _ string, id int64, includeDeleted bool) (*models.Route, error) {
	return f.findRoute(id, includeDeleted), nil
}

func (f *fakeStore) RouteNameExists(_ context.Context, _, name string, excludeID int64) (bool, error) {
	for _, r := range f.routes {
		if r.ID != excludeID && r.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CreateRoute(_ context.Context, _ string, r *models.Route, _ int64) (int64, error) {
	f.createdRoute = true
	r.ID = int64(len(f.routes) + 1)
	f.routes = append(f.routes, r)
	return r.ID, nil
}

func (f *fakeStore) UpdateRoute(_ context.Context, _ string, r *models.Route, _ int64) error {
	f.updatedRoute = true
	for i, cur := range f.routes {
		if cur.ID == r.ID {
			f.routes[i] = r
			return nil
		}
	}
	return nil
}

func (f *fakeStore) SoftDeleteRoute(_ context.Context, _ string, id, _ int64, _ string) error {
	f.deletedRoute = true
	if r := f.findRoute(id, true); r != nil {
		at := time.Now().UTC().Format(time.RFC3339)
		r.DeletedAt = &at
	}
	return nil
}

func (f *fakeStore) RestoreRoute(_ context.Context, _ string, id int64) error {
	f.restoredRoute = true
	if r := f.findRoute(id, true); r != nil {
		r.DeletedAt = nil
	}
	return nil
}

// --- route assignments ------------------------------------------------------

// seedAssignment registers an assignment as if it were already stored.
func (f *fakeStore) seedAssignment(a *models.RouteAssignment) {
	f.assignments = append(f.assignments, a)
}

// findAssignment returns the stored, non-deleted assignment of one route.
func (f *fakeStore) findAssignment(routeID, id int64) *models.RouteAssignment {
	for _, a := range f.assignments {
		if a.ID == id && a.RouteID == routeID && a.DeletedAt == nil {
			return a
		}
	}
	return nil
}

func (f *fakeStore) ListAssignments(_ context.Context, _ string, routeID int64) ([]models.RouteAssignment, error) {
	out := []models.RouteAssignment{}
	for _, a := range f.assignments {
		if a.RouteID == routeID && a.DeletedAt == nil {
			out = append(out, *a)
		}
	}
	return out, nil
}

func (f *fakeStore) AssignmentByID(_ context.Context, _ string, routeID, id int64) (*models.RouteAssignment, error) {
	return f.findAssignment(routeID, id), nil
}

func (f *fakeStore) CreateAssignment(_ context.Context, _ string, a *models.RouteAssignment, _ int64) (int64, error) {
	f.createdAssignment = true
	a.ID = int64(len(f.assignments) + 1)
	f.assignments = append(f.assignments, a)
	return a.ID, nil
}

func (f *fakeStore) UpdateAssignmentStatus(_ context.Context, _ string, a *models.RouteAssignment) error {
	f.updatedAssignment = true
	for i, cur := range f.assignments {
		if cur.ID == a.ID {
			f.assignments[i] = a
			return nil
		}
	}
	return nil
}

func (f *fakeStore) SoftDeleteAssignment(_ context.Context, _ string, routeID, id, _ int64, _ string) error {
	f.deletedAssignment = true
	if a := f.findAssignment(routeID, id); a != nil {
		at := time.Now().UTC().Format(time.RFC3339)
		a.DeletedAt = &at
	}
	return nil
}

// --- speed configs ----------------------------------------------------------

// seedSpeedConfig registers a threshold as if it were already stored.
func (f *fakeStore) seedSpeedConfig(sc *models.SpeedConfig) {
	f.speedConfigs = append(f.speedConfigs, sc)
}

// findSpeedConfig returns the stored threshold (nil when absent/filtered).
func (f *fakeStore) findSpeedConfig(id int64, includeDeleted bool) *models.SpeedConfig {
	for _, sc := range f.speedConfigs {
		if sc.ID != id {
			continue
		}
		if sc.DeletedAt != nil && !includeDeleted {
			return nil
		}
		return sc
	}
	return nil
}

func (f *fakeStore) ListSpeedConfigs(_ context.Context, _ string, includeDeleted bool) ([]models.SpeedConfig, error) {
	out := []models.SpeedConfig{}
	for _, sc := range f.speedConfigs {
		if sc.DeletedAt != nil && !includeDeleted {
			continue
		}
		out = append(out, *sc)
	}
	return out, nil
}

func (f *fakeStore) SpeedConfigByID(_ context.Context, _ string, id int64, includeDeleted bool) (*models.SpeedConfig, error) {
	return f.findSpeedConfig(id, includeDeleted), nil
}

func (f *fakeStore) CreateSpeedConfig(_ context.Context, _ string, sc *models.SpeedConfig, _ int64) (int64, error) {
	f.createdSpeed = true
	sc.ID = int64(len(f.speedConfigs) + 1)
	f.speedConfigs = append(f.speedConfigs, sc)
	return sc.ID, nil
}

func (f *fakeStore) UpdateSpeedConfig(_ context.Context, _ string, sc *models.SpeedConfig, _ int64) error {
	f.updatedSpeed = true
	for i, cur := range f.speedConfigs {
		if cur.ID == sc.ID {
			f.speedConfigs[i] = sc
			return nil
		}
	}
	return nil
}

func (f *fakeStore) SoftDeleteSpeedConfig(_ context.Context, _ string, id, _ int64, _ string) error {
	f.deletedSpeed = true
	if sc := f.findSpeedConfig(id, true); sc != nil {
		at := time.Now().UTC().Format(time.RFC3339)
		sc.DeletedAt = &at
	}
	return nil
}

func (f *fakeStore) RestoreSpeedConfig(_ context.Context, _ string, id int64) error {
	f.restoredSpeed = true
	if sc := f.findSpeedConfig(id, true); sc != nil {
		sc.DeletedAt = nil
	}
	return nil
}

// --- alert list -------------------------------------------------------------

func (f *fakeStore) ListAlerts(_ context.Context, _ AlertQuery) ([]models.Alert, int64, error) {
	out := f.alertsList
	if out == nil {
		out = []models.Alert{}
	}
	return out, int64(len(out)), nil
}
