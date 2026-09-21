package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// fi returns a float64 pointer for the DTO literals.
func fi(v float64) *float64 { return &v }

// ii returns an int pointer.
func ii(v int) *int { return &v }

// i64 returns an int64 pointer.
func i64(v int64) *int64 { return &v }

// ---------------------------------------------------------------------------
// geofences (PRD §5.9.1)
// ---------------------------------------------------------------------------

// TestGeofenceCreateCircleRequiresGeometry: a circle without center/radius is a
// 400 VALIDATION_ERROR naming the missing geometry (DB CHECK re-asserted).
func TestGeofenceCreateCircleRequiresGeometry(t *testing.T) {
	svc := newTestService(newFakeStore())
	body := `{"name":"Yard","area_type":"circle","center_lat":-6.2}`
	c, rec := testContext(http.MethodPost, "/api/v1/geofences", body, adminIdentity())

	svc.handleCreateGeofence(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for an incomplete circle", rec.Code)
	}
	var env models.ErrorEnvelope
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.ErrorCode != CodeValidationError {
		t.Errorf("error_code = %s, want %s", env.ErrorCode, CodeValidationError)
	}
}

// TestGeofenceCreateDefaultsAndNameConflict covers the §5.9.1 defaults
// (severity medium, entry+exit on, active) and the duplicate-name guard.
func TestGeofenceCreateDefaultsAndNameConflict(t *testing.T) {
	store := newFakeStore()
	store.seedGeofence(&models.Geofence{ID: 1, Name: "Yard", AreaType: "circle",
		CenterLat: fi(-6.2), CenterLon: fi(106.8), RadiusM: ii(500)})
	svc := newTestService(store)

	// Duplicate name → 409 CONFLICT.
	c, rec := testContext(http.MethodPost, "/api/v1/geofences",
		`{"name":"Yard","area_type":"circle","center_lat":-6.2,"center_lon":106.8,"radius_meters":500}`,
		adminIdentity())
	svc.handleCreateGeofence(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name got %d, want 409", rec.Code)
	}

	// Fresh polygon name → 201 with the documented defaults.
	store2 := newFakeStore()
	svc2 := newTestService(store2)
	c2, rec2 := testContext(http.MethodPost, "/api/v1/geofences",
		`{"name":"Depot","area_type":"polygon","boundary_points":[[-6.1,106.7],[-6.2,106.8],[-6.3,106.9]]}`,
		adminIdentity())
	svc2.handleCreateGeofence(c2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("polygon create got %d, want 201 (%s)", rec2.Code, rec2.Body.String())
	}
	if !store2.createdGeofence {
		t.Fatal("store.CreateGeofence was not called")
	}
	created := store2.geofences[0]
	if created.Severity != "medium" || !created.OnEntry || !created.OnExit || !created.Active {
		t.Errorf("defaults not applied: %+v", created)
	}
}

// TestGeofenceUpdateMergesAndValidatesGeometry covers the PATCH merge: optional
// fields are overlaid on the stored row and the MERGED geometry is re-validated.
func TestGeofenceUpdateMergesAndValidatesGeometry(t *testing.T) {
	store := newFakeStore()
	store.seedGeofence(&models.Geofence{ID: 1, Name: "Yard", AreaType: "circle",
		CenterLat: fi(-6.2), CenterLon: fi(106.8), RadiusM: ii(500), Severity: "medium"})
	svc := newTestService(store)

	c, rec := testContext(http.MethodPatch, "/api/v1/geofences/1",
		`{"name":"Yard","area_type":"circle","radius_meters":750}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleUpdateGeofence(c)
	if rec.Code != http.StatusOK || !store.updatedGeofence {
		t.Fatalf("update got %d (updated=%v), want 200", rec.Code, store.updatedGeofence)
	}
	if got := *store.geofences[0].RadiusM; got != 750 {
		t.Errorf("radius = %d, want 750", got)
	}

	// Switching to a polygon with 2 points must fail on the MERGED row.
	c2, rec2 := testContext(http.MethodPatch, "/api/v1/geofences/1",
		`{"name":"Yard","area_type":"polygon","boundary_points":[[-6.1,106.7],[-6.2,106.8]]}`,
		adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleUpdateGeofence(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("invalid merged geometry got %d, want 400", rec2.Code)
	}

	// Renaming onto an existing zone is a CONFLICT.
	store.seedGeofence(&models.Geofence{ID: 2, Name: "Depot", AreaType: "circle",
		CenterLat: fi(-6.2), CenterLon: fi(106.8), RadiusM: ii(100)})
	c3, rec3 := testContext(http.MethodPatch, "/api/v1/geofences/1",
		`{"name":"Depot","area_type":"circle"}`, adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleUpdateGeofence(c3)
	if rec3.Code != http.StatusConflict {
		t.Fatalf("rename onto an existing name got %d, want 409", rec3.Code)
	}

	// An unknown zone is a 404.
	c4, rec4 := testContext(http.MethodPatch, "/api/v1/geofences/77",
		`{"name":"Ghost","area_type":"circle","center_lat":-6.2,"center_lon":106.8,"radius_meters":50}`,
		adminIdentity())
	c4.Params = gin.Params{{Key: "id", Value: "77"}}
	svc.handleUpdateGeofence(c4)
	if rec4.Code != http.StatusNotFound {
		t.Fatalf("unknown zone got %d, want 404", rec4.Code)
	}
}

// TestGeofenceDeleteAndRestoreLifecycle covers the soft-delete veil: restore is
// rejected while the row is live and accepted once deleted.
func TestGeofenceDeleteAndRestoreLifecycle(t *testing.T) {
	store := newFakeStore()
	store.seedGeofence(&models.Geofence{ID: 1, Name: "Yard", AreaType: "circle",
		CenterLat: fi(-6.2), CenterLon: fi(106.8), RadiusM: ii(500)})
	svc := newTestService(store)

	c, rec := testContext(http.MethodDelete, "/api/v1/geofences/1", `{"reason":"test"}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleDeleteGeofence(c)
	if rec.Code != http.StatusOK || !store.deletedGeofence {
		t.Fatalf("delete got %d (deleted=%v), want 200", rec.Code, store.deletedGeofence)
	}

	// Restore of a live row is a CONFLICT.
	store.seedGeofence(&models.Geofence{ID: 2, Name: "Live", AreaType: "circle",
		CenterLat: fi(-6.2), CenterLon: fi(106.8), RadiusM: ii(100)})
	c2, rec2 := testContext(http.MethodPost, "/api/v1/geofences/2/restore", "", adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "2"}}
	svc.handleRestoreGeofence(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("restoring a live zone got %d, want 400 CONFLICT", rec2.Code)
	}

	// Restore of the deleted row succeeds and clears deleted_at.
	c3, rec3 := testContext(http.MethodPost, "/api/v1/geofences/1/restore", "", adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleRestoreGeofence(c3)
	if rec3.Code != http.StatusOK || !store.restoredGeofence {
		t.Fatalf("restore got %d (restored=%v), want 200", rec3.Code, store.restoredGeofence)
	}
	if store.geofences[0].DeletedAt != nil {
		t.Error("deleted_at not cleared by the restore")
	}

	// An unknown zone cannot be restored (404).
	c4, rec4 := testContext(http.MethodPost, "/api/v1/geofences/99/restore", "", adminIdentity())
	c4.Params = gin.Params{{Key: "id", Value: "99"}}
	svc.handleRestoreGeofence(c4)
	if rec4.Code != http.StatusNotFound {
		t.Fatalf("restore of an unknown zone got %d, want 404", rec4.Code)
	}
}

// TestGeofenceDetailNotFoundAndBadID covers the read guards.
func TestGeofenceDetailNotFoundAndBadID(t *testing.T) {
	svc := newTestService(newFakeStore())

	c, rec := testContext(http.MethodGet, "/api/v1/geofences/404", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "404"}}
	svc.handleGeofenceDetail(c)
	var env models.ErrorEnvelope
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if rec.Code != http.StatusNotFound || env.ErrorCode != CodeGeofenceNotFound {
		t.Errorf("got %d %s, want 404 GEOFENCE_NOT_FOUND", rec.Code, env.ErrorCode)
	}

	c2, rec2 := testContext(http.MethodGet, "/api/v1/geofences/abc", "", adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "abc"}}
	svc.handleGeofenceDetail(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id got %d, want 400", rec2.Code)
	}
}

// TestGeofenceListRowLevelAndPagination covers the list handler: an operator
// cannot look behind the soft-delete veil and an oversized limit is a 400.
func TestGeofenceListRowLevelAndPagination(t *testing.T) {
	store := newFakeStore()
	store.seedGeofence(&models.Geofence{ID: 1, Name: "Yard", AreaType: "circle"})
	svc := newTestService(store)

	c, rec := testContext(http.MethodGet, "/api/v1/geofences", "", operatorIdentity(2))
	svc.handleListGeofences(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator list got %d, want 200", rec.Code)
	}

	c2, rec2 := testContext(http.MethodGet, "/api/v1/geofences?include_deleted=true", "", operatorIdentity(2))
	svc.handleListGeofences(c2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("include_deleted by an operator got %d, want 403", rec2.Code)
	}

	c3, rec3 := testContext(http.MethodGet, "/api/v1/geofences?limit=99999", "", adminIdentity())
	svc.handleListGeofences(c3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("oversized limit got %d, want 400", rec3.Code)
	}
}

// ---------------------------------------------------------------------------
// routes (PRD §5.9.2)
// ---------------------------------------------------------------------------

// routeBody is a valid route payload with two waypoints.
const routeBody = `{"name":"R1","waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]}`

// TestRouteCreateNormalizesWaypointsAndConflicts: sequences are assigned
// server-side and a duplicate name is a CONFLICT.
func TestRouteCreateNormalizesWaypointsAndConflicts(t *testing.T) {
	store := newFakeStore()
	store.seedRoute(&models.Route{ID: 1, Name: "R1"})
	svc := newTestService(store)

	c, rec := testContext(http.MethodPost, "/api/v1/routes", routeBody, adminIdentity())
	svc.handleCreateRoute(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate route name got %d, want 409", rec.Code)
	}

	store2 := newFakeStore()
	svc2 := newTestService(store2)
	c2, rec2 := testContext(http.MethodPost, "/api/v1/routes",
		`{"name":"R2","estimated_duration_min":45,"waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9},{"lat":-6.4,"lon":107.0}]}`,
		adminIdentity())
	svc2.handleCreateRoute(c2)
	if rec2.Code != http.StatusCreated || !store2.createdRoute {
		t.Fatalf("valid route got %d (created=%v), want 201", rec2.Code, store2.createdRoute)
	}
	created := store2.routes[0]
	if len(created.Waypoints) != 3 {
		t.Fatalf("waypoints = %d, want 3", len(created.Waypoints))
	}
	for i, w := range created.Waypoints {
		if w.Seq != i+1 {
			t.Errorf("waypoint %d seq = %d, want %d (server-side normalisation)", i, w.Seq, i+1)
		}
	}
	if created.EstMinutes == nil || *created.EstMinutes != 45 {
		t.Errorf("estimated duration lost: %+v", created.EstMinutes)
	}
}

// TestRouteUpdateConflictsAndNotFound covers the PATCH guards.
func TestRouteUpdateConflictsAndNotFound(t *testing.T) {
	store := newFakeStore()
	store.seedRoute(&models.Route{ID: 1, Name: "R1"})
	store.seedRoute(&models.Route{ID: 2, Name: "R2"})
	svc := newTestService(store)

	c, rec := testContext(http.MethodPatch, "/api/v1/routes/1", routeBody, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleUpdateRoute(c)
	if rec.Code != http.StatusOK || !store.updatedRoute {
		t.Fatalf("update got %d (updated=%v), want 200", rec.Code, store.updatedRoute)
	}

	c2, rec2 := testContext(http.MethodPatch, "/api/v1/routes/1",
		`{"name":"R2","waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]}`,
		adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleUpdateRoute(c2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("rename onto an existing route got %d, want 409", rec2.Code)
	}

	c3, rec3 := testContext(http.MethodPatch, "/api/v1/routes/99", routeBody, adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "99"}}
	svc.handleUpdateRoute(c3)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown route got %d, want 404", rec3.Code)
	}
}

// TestRouteDeleteRestoreAndDetail covers delete → restore life-cycle, the
// soft-delete veil and the detail guards.
func TestRouteDeleteRestoreAndDetail(t *testing.T) {
	store := newFakeStore()
	store.seedRoute(&models.Route{ID: 1, Name: "R1"})
	svc := newTestService(store)

	c, rec := testContext(http.MethodDelete, "/api/v1/routes/1", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleDeleteRoute(c)
	if rec.Code != http.StatusOK || !store.deletedRoute {
		t.Fatalf("delete got %d (deleted=%v), want 200", rec.Code, store.deletedRoute)
	}

	// The deleted route disappears from the default detail read.
	c2, rec2 := testContext(http.MethodGet, "/api/v1/routes/1", "", adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleRouteDetail(c2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("deleted route detail got %d, want 404", rec2.Code)
	}

	// Restore succeeds only once the row is deleted.
	c3, rec3 := testContext(http.MethodPost, "/api/v1/routes/1/restore", "", adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleRestoreRoute(c3)
	if rec3.Code != http.StatusOK || !store.restoredRoute {
		t.Fatalf("restore got %d (restored=%v), want 200", rec3.Code, store.restoredRoute)
	}

	c4, rec4 := testContext(http.MethodPost, "/api/v1/routes/1/restore", "", adminIdentity())
	c4.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleRestoreRoute(c4)
	if rec4.Code != http.StatusBadRequest {
		t.Fatalf("restoring a live route got %d, want 400", rec4.Code)
	}

	// Bad id and unknown route.
	c5, rec5 := testContext(http.MethodGet, "/api/v1/routes/abc", "", adminIdentity())
	c5.Params = gin.Params{{Key: "id", Value: "abc"}}
	svc.handleRouteDetail(c5)
	if rec5.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric route id got %d, want 400", rec5.Code)
	}

	// List works for an operator and rejects include_deleted.
	c6, rec6 := testContext(http.MethodGet, "/api/v1/routes", "", operatorIdentity(2))
	svc.handleListRoutes(c6)
	if rec6.Code != http.StatusOK {
		t.Fatalf("operator route list got %d, want 200", rec6.Code)
	}
	c7, rec7 := testContext(http.MethodGet, "/api/v1/routes?include_deleted=true", "", operatorIdentity(2))
	svc.handleListRoutes(c7)
	if rec7.Code != http.StatusForbidden {
		t.Fatalf("operator include_deleted got %d, want 403", rec7.Code)
	}
}

// ---------------------------------------------------------------------------
// route assignments (PRD §5.9.2)
// ---------------------------------------------------------------------------

// TestAssignmentCreateValidatesRouteAndVehicle documents that an assignment
// needs an existing route AND an existing vehicle (no orphan rows).
func TestAssignmentCreateValidatesRouteAndVehicle(t *testing.T) {
	store := newFakeStore()
	store.seedRoute(&models.Route{ID: 1, Name: "R1"})
	store.seedVehicle(&models.Vehicle{ID: 5, IMEI: "864201040512345"})
	svc := newTestService(store)

	// Unknown route.
	c, rec := testContext(http.MethodPost, "/api/v1/routes/9/assignments", `{"vehicle_id":5}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "9"}}
	svc.handleCreateAssignment(c)
	var env models.ErrorEnvelope
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if rec.Code != http.StatusNotFound || env.ErrorCode != CodeRouteNotFound {
		t.Fatalf("unknown route got %d %s, want 404 ROUTE_NOT_FOUND", rec.Code, env.ErrorCode)
	}

	// Unknown vehicle.
	c2, rec2 := testContext(http.MethodPost, "/api/v1/routes/1/assignments", `{"vehicle_id":42}`, adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleCreateAssignment(c2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("unknown vehicle got %d, want 404", rec2.Code)
	}

	// Valid assignment starts as not_started.
	c3, rec3 := testContext(http.MethodPost, "/api/v1/routes/1/assignments",
		`{"vehicle_id":5,"driver_user_id":3}`, adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleCreateAssignment(c3)
	if rec3.Code != http.StatusCreated || !store.createdAssignment {
		t.Fatalf("valid assignment got %d (created=%v), want 201", rec3.Code, store.createdAssignment)
	}
	if got := store.assignments[0].Status; got != models.AssignNotStarted {
		t.Errorf("initial status = %s, want %s", got, models.AssignNotStarted)
	}
}

// TestAssignmentPatchTransitions covers the PRD §5.9.2 state machine and the
// started_at/completed_at stamping.
func TestAssignmentPatchTransitions(t *testing.T) {
	store := newFakeStore()
	store.seedRoute(&models.Route{ID: 1, Name: "R1"})
	store.seedAssignment(&models.RouteAssignment{ID: 7, RouteID: 1, VehicleID: 5, Status: models.AssignNotStarted})
	svc := newTestService(store)

	patch := func(assignment, body string) *httptest.ResponseRecorder {
		c, rec := testContext(http.MethodPatch, "/api/v1/routes/1/assignments/"+assignment, body, adminIdentity())
		c.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "assignmentId", Value: assignment}}
		svc.handlePatchAssignment(c)
		return rec
	}

	if rec := patch("abc", `{"status":"in_progress"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric assignment id got %d, want 400", rec.Code)
	}
	if rec := patch("99", `{"status":"in_progress"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown assignment got %d, want 404", rec.Code)
	}
	if rec := patch("7", `{"status":"completed"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("not_started → completed got %d, want 400 INVALID_STATUS_TRANSITION", rec.Code)
	}

	if rec := patch("7", `{"status":"in_progress"}`); rec.Code != http.StatusOK {
		t.Fatalf("not_started → in_progress got %d, want 200", rec.Code)
	}
	if store.assignments[0].StartedAt == nil {
		t.Error("started_at must be stamped on the first in_progress")
	}
	if rec := patch("7", `{"status":"completed"}`); rec.Code != http.StatusOK {
		t.Fatalf("in_progress → completed got %d, want 200", rec.Code)
	}
	if store.assignments[0].CompletedAt == nil {
		t.Error("completed_at must be stamped on completion")
	}
	if !store.updatedAssignment {
		t.Error("store.UpdateAssignmentStatus was not called")
	}
}

// TestAssignmentListAndDelete covers the read + soft-delete paths.
func TestAssignmentListAndDelete(t *testing.T) {
	store := newFakeStore()
	store.seedRoute(&models.Route{ID: 1, Name: "R1"})
	store.seedAssignment(&models.RouteAssignment{ID: 7, RouteID: 1, VehicleID: 5, Status: models.AssignInProgress})
	svc := newTestService(store)

	c, rec := testContext(http.MethodGet, "/api/v1/routes/1/assignments", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleListAssignments(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("assignment list got %d, want 200", rec.Code)
	}

	c2, rec2 := testContext(http.MethodDelete, "/api/v1/routes/1/assignments/7", `{"reason":"test"}`, adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "assignmentId", Value: "7"}}
	svc.handleDeleteAssignment(c2)
	if rec2.Code != http.StatusOK || !store.deletedAssignment {
		t.Fatalf("assignment delete got %d (deleted=%v), want 200", rec2.Code, store.deletedAssignment)
	}
}

// ---------------------------------------------------------------------------
// speed configs (PRD §5.9.4)
// ---------------------------------------------------------------------------

// TestSpeedConfigCreateDefaultsAndVehicleGuard covers the tenant-wide default
// (vehicle_id absent → NULL) and the vehicle existence guard.
func TestSpeedConfigCreateDefaultsAndVehicleGuard(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 5, IMEI: "864201040512345"})
	svc := newTestService(store)

	// Unknown vehicle for a vehicle-scoped threshold.
	c, rec := testContext(http.MethodPost, "/api/v1/speed-configs",
		`{"vehicle_id":42,"max_speed_kmh":80}`, adminIdentity())
	svc.handleCreateSpeedConfig(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown vehicle got %d, want 404", rec.Code)
	}

	// Tenant-wide default: severity medium, enabled.
	c2, rec2 := testContext(http.MethodPost, "/api/v1/speed-configs", `{"max_speed_kmh":90}`, adminIdentity())
	svc.handleCreateSpeedConfig(c2)
	if rec2.Code != http.StatusCreated || !store.createdSpeed {
		t.Fatalf("global threshold got %d (created=%v), want 201", rec2.Code, store.createdSpeed)
	}
	sc := store.speedConfigs[0]
	if sc.VehicleID != nil {
		t.Errorf("vehicle_id = %v, want NULL for a tenant-wide default", sc.VehicleID)
	}
	if sc.Severity != "medium" || !sc.Enabled {
		t.Errorf("defaults not applied: %+v", sc)
	}

	// Vehicle-scoped threshold accepted for a known vehicle.
	c3, rec3 := testContext(http.MethodPost, "/api/v1/speed-configs",
		`{"vehicle_id":5,"max_speed_kmh":70,"grace_margin_percent":10,"alert_severity":"high","enabled":false}`,
		adminIdentity())
	svc.handleCreateSpeedConfig(c3)
	if rec3.Code != http.StatusCreated {
		t.Fatalf("vehicle threshold got %d, want 201 (%s)", rec3.Code, rec3.Body.String())
	}
	if sc2 := store.speedConfigs[1]; sc2.VehicleID == nil || *sc2.VehicleID != 5 || sc2.Enabled {
		t.Errorf("vehicle threshold not mapped: %+v", sc2)
	}
}

// TestSpeedConfigUpdateMergeAndVehicleGuard covers the PATCH merge + guards.
func TestSpeedConfigUpdateMergeAndVehicleGuard(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 5, IMEI: "864201040512345"})
	store.seedSpeedConfig(&models.SpeedConfig{ID: 3, MaxSpeedKMH: 80, GracePct: 5,
		Severity: "medium", Enabled: true})
	svc := newTestService(store)

	c, rec := testContext(http.MethodPatch, "/api/v1/speed-configs/3",
		`{"max_speed_kmh":95,"alert_severity":"critical"}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "3"}}
	svc.handleUpdateSpeedConfig(c)
	if rec.Code != http.StatusOK || !store.updatedSpeed {
		t.Fatalf("update got %d (updated=%v), want 200", rec.Code, store.updatedSpeed)
	}
	updated := store.speedConfigs[0]
	if updated.MaxSpeedKMH != 95 || updated.Severity != "critical" || updated.GracePct != 5 {
		t.Errorf("merge lost fields: %+v", updated)
	}

	c2, rec2 := testContext(http.MethodPatch, "/api/v1/speed-configs/3",
		`{"vehicle_id":42,"max_speed_kmh":95}`, adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "3"}}
	svc.handleUpdateSpeedConfig(c2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("unknown vehicle got %d, want 404", rec2.Code)
	}

	c3, rec3 := testContext(http.MethodPatch, "/api/v1/speed-configs/77",
		`{"max_speed_kmh":95}`, adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "77"}}
	svc.handleUpdateSpeedConfig(c3)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown threshold got %d, want 404", rec3.Code)
	}
}

// TestSpeedConfigDeleteRestoreAndDetail covers the life-cycle + read guards.
func TestSpeedConfigDeleteRestoreAndDetail(t *testing.T) {
	store := newFakeStore()
	store.seedSpeedConfig(&models.SpeedConfig{ID: 1, MaxSpeedKMH: 80, Severity: "medium", Enabled: true})
	svc := newTestService(store)

	c, rec := testContext(http.MethodGet, "/api/v1/speed-configs/1", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleSpeedConfigDetail(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail got %d, want 200", rec.Code)
	}

	c2, rec2 := testContext(http.MethodDelete, "/api/v1/speed-configs/1", "", adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleDeleteSpeedConfig(c2)
	if rec2.Code != http.StatusOK || !store.deletedSpeed {
		t.Fatalf("delete got %d (deleted=%v), want 200", rec2.Code, store.deletedSpeed)
	}

	c3, rec3 := testContext(http.MethodGet, "/api/v1/speed-configs/1", "", adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleSpeedConfigDetail(c3)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("deleted threshold detail got %d, want 404", rec3.Code)
	}

	c4, rec4 := testContext(http.MethodPost, "/api/v1/speed-configs/1/restore", "", adminIdentity())
	c4.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleRestoreSpeedConfig(c4)
	if rec4.Code != http.StatusOK || !store.restoredSpeed {
		t.Fatalf("restore got %d (restored=%v), want 200", rec4.Code, store.restoredSpeed)
	}

	c5, rec5 := testContext(http.MethodPost, "/api/v1/speed-configs/1/restore", "", adminIdentity())
	c5.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleRestoreSpeedConfig(c5)
	if rec5.Code != http.StatusBadRequest {
		t.Fatalf("restoring a live threshold got %d, want 400", rec5.Code)
	}

	c6, rec6 := testContext(http.MethodGet, "/api/v1/speed-configs", "", operatorIdentity(2))
	svc.handleListSpeedConfigs(c6)
	if rec6.Code != http.StatusOK {
		t.Fatalf("list got %d, want 200", rec6.Code)
	}
	c7, rec7 := testContext(http.MethodGet, "/api/v1/speed-configs?include_deleted=true", "", operatorIdentity(2))
	svc.handleListSpeedConfigs(c7)
	if rec7.Code != http.StatusForbidden {
		t.Fatalf("operator include_deleted got %d, want 403", rec7.Code)
	}
}

// newServiceWithStore wires a Service with an arbitrary Store implementation.
// It is the test-only twin of newTestService that accepts any Store so a test
// can swap in a wrapper simulating a store-level race (conditional UPDATE
// matching 0 rows).
func newServiceWithStore(store Store) *Service {
	return NewService(Deps{
		Settings: Settings{
			HTTPAddr:        ":0",
			JWTSecret:       strings.Repeat("t", 40),
			JWTIssuer:       "test",
			DefaultPageSize: 100,
			MaxPageSize:     1000,
		},
		Store: store,
	})
}

// listAlertsEnvelope is the decoded success payload of GET /api/v1/alerts.
type listAlertsEnvelope struct {
	Status     string             `json:"status"`
	Data       []models.Alert     `json:"data"`
	Pagination *models.Pagination `json:"pagination"`
}

// resolveZeroStore wraps a fakeStore and reports ResolveAlert as matching 0
// rows even when the in-memory alert is open — i.e. the lost-update race the
// `if rows == 0` guard exists to protect against.
type resolveZeroStore struct {
	*fakeStore
}

func (s resolveZeroStore) ResolveAlert(context.Context, string, int64, int64) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// alerts (PRD §8.1 list + §5.9.7 life-cycle)
// ---------------------------------------------------------------------------

// TestAlertsListFilterValidation exercises every rejection branch of the
// list handler before the store is ever queried, then the happy path.
func TestAlertsListFilterValidation(t *testing.T) {
	svc := newTestService(newFakeStore())

	bad := func(q string) *httptest.ResponseRecorder {
		c, rec := testContext(http.MethodGet, "/api/v1/alerts?"+q, "", adminIdentity())
		svc.handleListAlerts(c)
		return rec
	}
	if rec := bad("type=not_a_type"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad type got %d, want 400", rec.Code)
	}
	if rec := bad("severity=urgent"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad severity got %d, want 400", rec.Code)
	}
	if rec := bad("status=closed"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad status got %d, want 400", rec.Code)
	}
	if rec := bad("from=not-a-time"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad from got %d, want 400", rec.Code)
	}
	if rec := bad("to=not-a-time"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad to got %d, want 400", rec.Code)
	}

	// Happy path: a fully-valid query returns 200 with pagination metadata.
	store := newFakeStore()
	store.alertsList = []models.Alert{{ID: 1, Type: "overspeeding", Severity: "high", Status: "open"}}
	svc2 := newServiceWithStore(store)
	c, rec := testContext(http.MethodGet,
		"/api/v1/alerts?type=overspeeding&severity=high&status=open&from=2026-09-16T00:00:00Z&to=2026-09-16T23:00:00Z&limit=50",
		"", adminIdentity())
	svc2.handleListAlerts(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid query got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var env listAlertsEnvelope
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if env.Status != "success" || env.Pagination.Total != 1 || len(env.Data) != 1 {
		t.Errorf("list envelope = status %q total %d items %d, want success/1/1",
			env.Status, env.Pagination.Total, len(env.Data))
	}
}

// TestAcknowledgeAlertNotFoundAndBadID covers the pathID validation (non-numeric
// id → 400) and the AlertByID miss (unknown id → 404 ALERT_NOT_FOUND) — the
// two branches the flow test does not exercise.
func TestAcknowledgeAlertNotFoundAndBadID(t *testing.T) {
	svc := newTestService(newFakeStore())

	c, rec := testContext(http.MethodPost, "/api/v1/alerts/x/acknowledge", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "x"}}
	svc.handleAcknowledgeAlert(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric id got %d, want 400", rec.Code)
	}

	c2, rec2 := testContext(http.MethodPost, "/api/v1/alerts/99/acknowledge", "", adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "99"}}
	svc.handleAcknowledgeAlert(c2)
	var env models.ErrorEnvelope
	if err := parseJSON(rec2.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if rec2.Code != http.StatusNotFound || env.ErrorCode != CodeAlertNotFound {
		t.Fatalf("unknown alert got %d %s, want 404 ALERT_NOT_FOUND", rec2.Code, env.ErrorCode)
	}
}

// TestResolveAlertLifecycle covers open|acknowledged → resolved and the
// conflict on re-resolve.
func TestResolveAlertLifecycle(t *testing.T) {
	store := newFakeStore()
	store.seedAlert(&models.Alert{ID: 1, Type: "geofence_breach", Status: "acknowledged"})
	svc := newTestService(store)

	c, rec := testContext(http.MethodPost, "/api/v1/alerts/1/resolve", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleResolveAlert(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if store.alerts[1].Status != "resolved" {
		t.Errorf("status = %s, want resolved", store.alerts[1].Status)
	}

	// Second resolve on an already-resolved alert → 400 CONFLICT.
	c2, rec2 := testContext(http.MethodPost, "/api/v1/alerts/1/resolve", "", adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleResolveAlert(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("re-resolve got %d, want 400 CONFLICT", rec2.Code)
	}

	c3, rec3 := testContext(http.MethodPost, "/api/v1/alerts/99/resolve", "", adminIdentity())
	c3.Params = gin.Params{{Key: "id", Value: "99"}}
	svc.handleResolveAlert(c3)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown alert got %d, want 404", rec3.Code)
	}
}

// TestResolveAlertRowsZeroConflict simulates the conditional UPDATE matching 0
// rows even though the in-memory copy is still open — the race the
// `if rows == 0` guard protects against reports 400 CONFLICT rather than a
// misleading 200.
func TestResolveAlertRowsZeroConflict(t *testing.T) {
	store := newFakeStore()
	store.seedAlert(&models.Alert{ID: 1, Type: "offline", Status: "open"})
	// Wrap so ResolveAlert returns 0 rows (lost-update scenario).
	svc := newServiceWithStore(&resolveZeroStore{store})

	c, rec := testContext(http.MethodPost, "/api/v1/alerts/1/resolve", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleResolveAlert(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("lost-update resolve got %d, want 400 CONFLICT (rows==0 guard)", rec.Code)
	}
}
