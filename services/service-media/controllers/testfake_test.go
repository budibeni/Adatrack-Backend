package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/storage"
	"adatrack_gps/service-media/models"
)

// fakeStore is the in-memory Store used by the unit tests (no DB required).
type fakeStore struct {
	mu sync.Mutex

	users     map[int64]*UserRecord
	access    map[string]map[int64]string // company -> user -> role override
	assigned  map[string]map[int64][]int64
	companies []MediaConfig
	devices   map[string]*VehicleRef
	vehicles  map[string]map[int64]*Vehicle

	media      map[string]map[int64]*models.MediaEvent
	nextID     int64
	expiredIDs map[string][]int64
	audit      []AuditRow

	auditErr  error
	configErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:      map[int64]*UserRecord{},
		access:     map[string]map[int64]string{},
		assigned:   map[string]map[int64][]int64{},
		devices:    map[string]*VehicleRef{},
		vehicles:   map[string]map[int64]*Vehicle{},
		media:      map[string]map[int64]*models.MediaEvent{},
		expiredIDs: map[string][]int64{},
	}
}

func (f *fakeStore) UserByID(_ context.Context, id int64) (*UserRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}

func (f *fakeStore) TenantAccess(_ context.Context, company string, userID int64) (string, bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.access[company][userID]
	if !ok {
		return "", false, false, nil
	}
	return role, true, true, nil
}

func (f *fakeStore) AssignedVehicleIDs(_ context.Context, company string, userID int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.assigned[company][userID]...), nil
}

func (f *fakeStore) MediaCompanies(_ context.Context) ([]MediaConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.configErr != nil {
		return nil, f.configErr
	}
	return append([]MediaConfig(nil), f.companies...), nil
}

func (f *fakeStore) ResolveVehicleByIMEI(_ context.Context, imei string) (*VehicleRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := f.devices[imei]
	if !ok {
		return nil, nil
	}
	cp := *ref
	return &cp, nil
}

func (f *fakeStore) WriteAudit(_ context.Context, rows []AuditRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auditErr != nil {
		return f.auditErr
	}
	f.audit = append(f.audit, rows...)
	return nil
}

func (f *fakeStore) VehicleByID(_ context.Context, company string, id int64, _ bool) (*Vehicle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vehicles[company][id]
	if !ok {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}

func (f *fakeStore) CreateMediaEvent(_ context.Context, company string, m *models.MediaEvent) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	cp := *m
	cp.ID = f.nextID
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
		cp.UpdatedAt = cp.CreatedAt
	}
	if f.media[company] == nil {
		f.media[company] = map[int64]*models.MediaEvent{}
	}
	f.media[company][cp.ID] = &cp
	return cp.ID, nil
}

func (f *fakeStore) MediaEventByID(_ context.Context, company string, id int64, _ bool) (*models.MediaEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.media[company][id]
	if !ok {
		return nil, nil
	}
	cp := *m
	return &cp, nil
}

func (f *fakeStore) ListMediaEvents(_ context.Context, q MediaQuery) ([]models.MediaEvent, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []models.MediaEvent{}
	for _, m := range f.media[q.CompanyCode] {
		if !q.IncludeDel && m.DeletedAt != nil {
			continue
		}
		if !q.AllVehicles && !containsID(q.AssignedIDs, m.VehicleID) {
			continue
		}
		if q.VehicleID > 0 && m.VehicleID != q.VehicleID {
			continue
		}
		out = append(out, *m)
	}
	return out, int64(len(out)), nil
}

func (f *fakeStore) CompleteMediaEvent(_ context.Context, company string, m *models.MediaEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.media[company][m.ID]
	if !ok || cur.Status != models.StatusPending {
		return errConflict(CodeInvalidTransition, "not pending")
	}
	cp := *m
	f.media[company][m.ID] = &cp
	return nil
}

func (f *fakeStore) MarkMediaExpired(_ context.Context, company string, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expiredIDs[company] = append(f.expiredIDs[company], ids...)
	for _, id := range ids {
		if m, ok := f.media[company][id]; ok {
			m.Status = models.StatusExpired
			m.UpdatedAt = time.Now().UTC()
		}
	}
	return nil
}

func (f *fakeStore) SoftDeleteMediaEvent(_ context.Context, company string, id, by int64, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.media[company][id]
	if !ok || m.DeletedAt != nil {
		return errConflict(CodeConflict, "already deleted")
	}
	now := time.Now().UTC()
	m.Status = models.StatusDeleted
	m.DeletedAt = &now
	m.DeleteReason = reason
	return nil
}

func (f *fakeStore) RestoreMediaEvent(_ context.Context, company string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.media[company][id]
	if !ok || m.DeletedAt == nil {
		return errConflict(CodeConflict, "not deleted")
	}
	m.DeletedAt = nil
	m.DeleteReason = ""
	m.Status = models.StatusComplete
	return nil
}

func (f *fakeStore) ExpiredMediaEvents(_ context.Context, company string, now, _ time.Time, _ int) ([]models.MediaEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []models.MediaEvent{}
	for _, m := range f.media[company] {
		if m.Status == models.StatusExpired {
			continue
		}
		if m.ExpiresAt != nil && !m.ExpiresAt.After(now) {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeStore) CountStoredObjects(_ context.Context, company string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, m := range f.media[company] {
		if m.Status == models.StatusComplete || m.Status == models.StatusDeleted {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) Master() *internal.DBPool { return &internal.DBPool{} }

func (f *fakeStore) TenantHealth(context.Context) error { return nil }

// auditRows returns a copy of the written audit rows.
func (f *fakeStore) auditRows() []AuditRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]AuditRow(nil), f.audit...)
}

// rows returns the catalog of one company.
func (f *fakeStore) rows(company string) []models.MediaEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []models.MediaEvent{}
	for _, m := range f.media[company] {
		out = append(out, *m)
	}
	return out
}

// containsID reports membership.
func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// newTestService wires a Service with a fake store + in-memory object storage.
func newTestService(t *testing.T, store *fakeStore, opts ...func(*Settings)) (*Service, *storage.Mem) {
	t.Helper()
	settings := Settings{
		HTTPAddr:         ":0",
		Bucket:           "adatrack-media",
		MaxFileMB:        1,
		PresignTTL:       2 * time.Minute,
		HMACSecret:       "test-secret-at-least-16-chars",
		HMACMaxSkew:      5 * time.Minute,
		RetentionDays:    30,
		CleanupCron:      "0 3 * * *",
		PendingTTLHours:  24,
		ConfigCacheTTL:   time.Minute,
		JWTSecret:        strings.Repeat("k", 32),
		JWTIssuer:        "adatrack",
		JWTClockSkew:     time.Minute,
		DefaultPageSize:  100,
		MaxPageSize:      1000,
		MaxBodyBytes:     4 << 20,
		AllowEmptyOrigin: true,
		AuditEnabled:     true,
	}
	for _, opt := range opts {
		opt(&settings)
	}
	mem := storage.NewMem(settings.Bucket)
	svc := NewService(Deps{Settings: settings, Store: store, Storage: mem})
	return svc, mem
}

// seedTenant registers one company with a device/vehicle mapping.
func seedTenant(store *fakeStore, company, imei string, vehicleID int64) {
	store.companies = append(store.companies, MediaConfig{
		CompanyCode: company, Bucket: "adatrack-media", RetentionDays: 30,
		MaxFileMB: 1, HMACSecret: "tenant-hmac-secret-123456",
	})
	store.devices[imei] = &VehicleRef{CompanyCode: company, VehicleID: vehicleID, IsActive: true}
	if store.vehicles[company] == nil {
		store.vehicles[company] = map[int64]*Vehicle{}
	}
	store.vehicles[company][vehicleID] = &Vehicle{ID: vehicleID, IMEI: imei, PlateNumber: "B 1 TST"}
}

// seedUser registers one authenticated identity with a tenant role.
func seedUser(store *fakeStore, id int64, company, email, role string, assigned []int64) {
	store.users[id] = &UserRecord{ID: id, CompanyCode: company, Email: email, GlobalRole: role, IsActive: true}
	if store.access[company] == nil {
		store.access[company] = map[int64]string{}
	}
	store.access[company][id] = role
	if assigned != nil {
		if store.assigned[company] == nil {
			store.assigned[company] = map[int64][]int64{}
		}
		store.assigned[company][id] = assigned
	}
}

// errInjected is the deliberate failure used by the negative tests.
var errInjected = errors.New("injected failure")

// describe renders a value for assertion messages (keeps the import honest).
func describe(v any) string { return fmt.Sprintf("%+v", v) }

func (f *fakeStore) MediaEventByObjectKey(_ context.Context, company, key string) (*models.MediaEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.media[company] {
		if m.ObjectKey == key {
			cp := *m
			return &cp, nil
		}
	}
	return nil, nil
}
