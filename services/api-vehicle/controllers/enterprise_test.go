package controllers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// testEnterpriseStore is a *fakeStore that ALSO implements the full B12
// EnterpriseStore via the embedded interface (unused methods panic — the tests
// only stub what the handler under test touches).
type testEnterpriseStore struct {
	*fakeStore
	EnterpriseStore

	items      map[string]map[int64]map[string]any
	nextID     int64
	menus      []models.MenuItem
	menuRoles  []string
	shareLinks []models.ShareLink
	licences   []models.ModuleLicense
	shareViews int
}

func newTestEnterpriseStore() *testEnterpriseStore {
	return &testEnterpriseStore{
		fakeStore: newFakeStore(),
		items:     map[string]map[int64]map[string]any{},
		nextID:    0,
	}
}

func newEnterpriseService(store *testEnterpriseStore) *Service {
	return NewService(Deps{
		Settings: authSettings(),
		Store:    store,
		Auditor:  NewAuditor(store, nil, true),
	})
}

func (f *testEnterpriseStore) bucket(spec resourceSpec) map[int64]map[string]any {
	if f.items[spec.Name] == nil {
		f.items[spec.Name] = map[int64]map[string]any{}
	}
	return f.items[spec.Name]
}

func (f *testEnterpriseStore) ListEnterprise(_ context.Context, spec resourceSpec, _ EnterpriseQuery) ([]map[string]any, int64, error) {
	bucket := f.bucket(spec)
	out := []map[string]any{}
	for _, row := range bucket {
		if row["deleted_at"] != nil {
			continue
		}
		out = append(out, row)
	}
	return out, int64(len(out)), nil
}

func (f *testEnterpriseStore) GetEnterprise(_ context.Context, _ string, spec resourceSpec, id int64, _ bool) (map[string]any, error) {
	row, ok := f.bucket(spec)[id]
	if !ok {
		return nil, nil
	}
	return row, nil
}

func (f *testEnterpriseStore) CreateEnterprise(_ context.Context, _ string, spec resourceSpec, data map[string]any, _ int64) (int64, error) {
	f.nextID++
	row := map[string]any{"id": f.nextID}
	for k, v := range data {
		row[k] = v
	}
	f.bucket(spec)[f.nextID] = row
	return f.nextID, nil
}

func (f *testEnterpriseStore) UpdateEnterprise(_ context.Context, _ string, spec resourceSpec, id int64, data map[string]any, _ int64) (int64, error) {
	row, ok := f.bucket(spec)[id]
	if !ok || row["deleted_at"] != nil {
		return 0, nil
	}
	for k, v := range data {
		row[k] = v
	}
	return 1, nil
}

func (f *testEnterpriseStore) SoftDeleteEnterprise(_ context.Context, _ string, spec resourceSpec, id, _ int64, _ string) (int64, error) {
	row, ok := f.bucket(spec)[id]
	if !ok || row["deleted_at"] != nil {
		return 0, nil
	}
	row["deleted_at"] = time.Now().UTC().Format(time.RFC3339)
	return 1, nil
}

func (f *testEnterpriseStore) RestoreEnterprise(_ context.Context, _ string, spec resourceSpec, id int64) (int64, error) {
	row, ok := f.bucket(spec)[id]
	if !ok || row["deleted_at"] == nil {
		return 0, nil
	}
	row["deleted_at"] = nil
	return 1, nil
}

func (f *testEnterpriseStore) MenuItemsForRole(_ context.Context, _ string, role string) ([]models.MenuItem, error) {
	f.menuRoles = append(f.menuRoles, role)
	return f.menus, nil
}

func (f *testEnterpriseStore) CreateShareLink(_ context.Context, _ string, link *models.ShareLink, expiresAt time.Time, _ int64) (int64, error) {
	link.ID = int64(len(f.shareLinks) + 1)
	link.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	f.shareLinks = append(f.shareLinks, *link)
	return link.ID, nil
}

func (f *testEnterpriseStore) ListShareLinks(_ context.Context, _ string) ([]models.ShareLink, error) {
	return f.shareLinks, nil
}

func (f *testEnterpriseStore) ResolveShareLink(_ context.Context, token string) (*models.ShareLink, error) {
	for i := range f.shareLinks {
		if f.shareLinks[i].Token == token && f.shareLinks[i].RevokedAt == nil {
			f.shareViews++
			return &f.shareLinks[i], nil
		}
	}
	return nil, nil
}

func (f *testEnterpriseStore) SharedVehicles(_ context.Context, _ string, ids []int64) ([]models.SharedVehicle, error) {
	out := []models.SharedVehicle{}
	for _, id := range ids {
		out = append(out, models.SharedVehicle{VehicleID: id, PlateNumber: "SHARED-" + itoa(int(id))})
	}
	return out, nil
}

func (f *testEnterpriseStore) ModuleLicenses(_ context.Context, _ string) ([]models.ModuleLicense, error) {
	return f.licences, nil
}

// TestEnterpriseNormalize pins the table-driven validation (B12): required
// fields, enum membership, kind coercion and unknown-key rejection.
func TestEnterpriseNormalize(t *testing.T) {
	drivers, _ := lookupResource("drivers")

	if _, err := drivers.normalize(map[string]any{"name": "Budi", "status": "active"}, false); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if _, err := drivers.normalize(map[string]any{"status": "active"}, false); err == nil {
		t.Error("a missing required field must be rejected")
	}
	if _, err := drivers.normalize(map[string]any{"name": "Budi", "status": "flying"}, false); err == nil {
		t.Error("an out-of-enum value must be rejected")
	}
	if _, err := drivers.normalize(map[string]any{"name": "Budi", "hacked": "1); DROP"}, false); err == nil {
		t.Error("an unknown field must be rejected (no injection surface)")
	}
	if _, err := drivers.normalize(map[string]any{"name": "Budi", "user_id": 12.5}, false); err == nil {
		t.Error("a non-integer value for an integer field must be rejected")
	}
	if _, err := drivers.normalize(map[string]any{"name": "Budi", "license_expiry": "2027-01-31"}, false); err != nil {
		t.Errorf("a YYYY-MM-DD date must be accepted: %v", err)
	}
	partial, err := drivers.normalize(map[string]any{"phone": "08123"}, true)
	if err != nil {
		t.Fatalf("partial patch rejected: %v", err)
	}
	if len(partial) != 1 {
		t.Errorf("partial patch = %v, want only the provided field", partial)
	}
	if _, err := drivers.normalize(map[string]any{}, true); err == nil {
		t.Error("an empty PATCH body must be rejected")
	}

	accessLogs, _ := lookupResource("access-logs")
	if !accessLogs.Immutable {
		t.Error("the access log must be declared immutable (append-only)")
	}
	if _, err := accessLogs.normalize(map[string]any{"direction": "sideways"}, false); err == nil {
		t.Error("an invalid access direction must be rejected")
	}
	if _, err := accessLogs.normalize(map[string]any{"direction": "in"}, false); err != nil {
		t.Errorf("a minimal access log must be accepted: %v", err)
	}
}

// TestEnterpriseCRUDLifecycle drives the generic handlers end-to-end (create →
// list → patch → delete → restore) including soft delete semantics.
func TestEnterpriseCRUDLifecycle(t *testing.T) {
	store := newTestEnterpriseStore()
	svc := newEnterpriseService(store)
	spec, _ := lookupResource("drivers")
	gin.SetMode(gin.TestMode)

	// create
	c, rec := testContext(http.MethodPost, "/api/v1/drivers",
		`{"name":"Budi Santoso","employee_code":"DRV-001","status":"active"}`, adminIdentity())
	svc.handleCreateEnterprise(spec)(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var createdEnvelope struct {
		Data map[string]any `json:"data"`
	}
	if err := parseJSON(rec.Body.Bytes(), &createdEnvelope); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createdEnvelope.Data["name"] != "Budi Santoso" {
		t.Errorf("created = %v, want the requested name", createdEnvelope.Data)
	}

	// create with a bad payload → 400
	c, rec = testContext(http.MethodPost, "/api/v1/drivers", `{"status":"active"}`, adminIdentity())
	svc.handleCreateEnterprise(spec)(c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid create = %d, want 400", rec.Code)
	}

	// list
	c, rec = testContext(http.MethodGet, "/api/v1/drivers", "", adminIdentity())
	svc.handleListEnterprise(spec)(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", rec.Code)
	}

	// patch
	c, rec = testContext(http.MethodPatch, "/api/v1/drivers/1", `{"phone":"0812"}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleUpdateEnterprise(spec)(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d, want 200", rec.Code)
	}

	// delete (soft)
	c, rec = testContext(http.MethodDelete, "/api/v1/drivers/1", `{"reason":"resigned"}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleDeleteEnterprise(spec)(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d, want 200", rec.Code)
	}

	// restore
	c, rec = testContext(http.MethodPost, "/api/v1/drivers/1/restore", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleRestoreEnterprise(spec)(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// patching an unknown id is a 404 with the resource-specific code
	c, rec = testContext(http.MethodPatch, "/api/v1/drivers/99", `{"phone":"0812"}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "99"}}
	svc.handleUpdateEnterprise(spec)(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown patch = %d, want 404", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != "DRIVER_NOT_FOUND" {
		t.Errorf("error_code = %s, want DRIVER_NOT_FOUND", code)
	}
}

// TestComplianceGuardBlocksUnknownFields proves a mutation with an unknown field
// never reaches the store (the table-driven CRUD cannot be widened by a client).
func TestComplianceGuardBlocksUnknownFields(t *testing.T) {
	store := newTestEnterpriseStore()
	svc := newEnterpriseService(store)
	spec, _ := lookupResource("assets")

	c, rec := testContext(http.MethodPost, "/api/v1/assets",
		`{"name":"Genset","hacked":true}`, adminIdentity())
	svc.handleCreateEnterprise(spec)(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", rec.Code)
	}
	if _, err := store.GetEnterprise(context.Background(), "DEV001", spec, 1, false); err != nil {
		t.Fatalf("store lookup: %v", err)
	}
	if row, _ := store.GetEnterprise(context.Background(), "DEV001", spec, 1, false); row != nil {
		t.Error("a rejected payload must never be persisted")
	}
}

// TestAccessMenuUsesCallerRole implements the B12 acceptance criterion: the
// navigation is rendered from GET /api/v1/access/menu for the CALLER's role.
func TestAccessMenuUsesCallerRole(t *testing.T) {
	store := newTestEnterpriseStore()
	store.menus = []models.MenuItem{
		{MenuID: 1, ModuleCode: "main", Code: "business.main.home", Name: "Beranda", Path: "/", CanView: true},
	}
	svc := newEnterpriseService(store)

	c, rec := testContext(http.MethodGet, "/api/v1/access/menu", "", operatorIdentity(1))
	svc.handleAccessMenu(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("access menu = %d, want 200", rec.Code)
	}
	if len(store.menuRoles) != 1 || store.menuRoles[0] != models.RoleOperator {
		t.Fatalf("menuRoles = %v, want the caller's role", store.menuRoles)
	}
}

// TestPublicShareLifecycle covers FR-9.3 end-to-end: create a link (Admin), read
// it WITHOUT a token, and confirm an unknown token is a 404.
func TestPublicShareLifecycle(t *testing.T) {
	store := newTestEnterpriseStore()
	svc := newEnterpriseService(store)

	c, rec := testContext(http.MethodPost, "/api/v1/share-links",
		`{"label":"Live trip","vehicle_ids":[1,2],"ttl_minutes":60}`, adminIdentity())
	svc.handleCreateShareLink(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if len(store.shareLinks) != 1 {
		t.Fatalf("share links = %d, want 1", len(store.shareLinks))
	}
	token := store.shareLinks[0].Token
	if len(token) < 16 {
		t.Fatalf("token = %q, want a long random token", token)
	}

	// public read (no identity attached)
	c, rec = testContext(http.MethodGet, "/api/v1/share/"+token, "", nil)
	c.Params = gin.Params{{Key: "token", Value: token}}
	svc.handlePublicShare(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("public share = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if store.shareViews != 1 {
		t.Errorf("view count increments = %d, want 1", store.shareViews)
	}
	var envelope struct {
		Data struct {
			Vehicles []models.SharedVehicle `json:"vehicles"`
		} `json:"data"`
	}
	if err := parseJSON(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode public share: %v", err)
	}
	if len(envelope.Data.Vehicles) != 2 {
		t.Errorf("vehicles = %d, want the 2 requested vehicles", len(envelope.Data.Vehicles))
	}

	// unknown token → 404 SHARE_LINK_NOT_FOUND
	c, rec = testContext(http.MethodGet, "/api/v1/share/unknowntoken1234567890", "", nil)
	c.Params = gin.Params{{Key: "token", Value: "unknowntoken1234567890"}}
	svc.handlePublicShare(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token = %d, want 404", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != "SHARE_LINK_NOT_FOUND" {
		t.Errorf("error_code = %s, want SHARE_LINK_NOT_FOUND", code)
	}
}
