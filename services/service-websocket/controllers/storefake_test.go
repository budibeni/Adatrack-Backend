package controllers

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"adatrack_gps/internal/tenant"
	"adatrack_gps/service-websocket/models"
)

// accessRow is one `tm_user_company_access` row in the fake store.
type accessRow struct {
	roleOverride string
	isActive     bool
}

// fakeStore is an in-memory Store used by the unit tests so handlers, middleware
// and RBAC can be exercised without PostgreSQL (live infrastructure is covered by
// the tools/e2ews harness).
type fakeStore struct {
	mu sync.Mutex

	users        map[string]*UserRecord // key: lower(email)
	access       map[string]map[int64]accessRow
	assigned     map[string]map[int64][]int64
	vehicles     map[string][]models.Vehicle
	positions    map[string][]models.Position
	companies    map[string]bool
	audits       []AuditRow
	auditErr     error
	readinessErr error
	createdUsers []UserRecord
	provisions   []tenant.ProvisionOptions
	provisionErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:     map[string]*UserRecord{},
		access:    map[string]map[int64]accessRow{},
		assigned:  map[string]map[int64][]int64{},
		vehicles:  map[string][]models.Vehicle{},
		positions: map[string][]models.Position{},
		companies: map[string]bool{},
	}
}

// addUser registers a user with a bcrypt hash of `password`.
func (f *fakeStore) addUser(id int64, company, email, role, password string, mustChange bool) *UserRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := &UserRecord{
		ID:                 id,
		CompanyCode:        strings.ToUpper(company),
		Email:              email,
		FullName:           "User " + itoa(id),
		PasswordHash:       bcryptHashForTest(password),
		GlobalRole:         role,
		IsActive:           true,
		MustChangePassword: mustChange,
	}
	f.users[strings.ToLower(email)] = rec
	return rec
}

// addAccess registers a `tm_user_company_access` row.
func (f *fakeStore) addAccess(company string, userID int64, role string, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := strings.ToUpper(company)
	if f.access[code] == nil {
		f.access[code] = map[int64]accessRow{}
	}
	f.access[code][userID] = accessRow{roleOverride: role, isActive: active}
}

// addAssignment grants a user access to vehicles.
func (f *fakeStore) addAssignment(company string, userID int64, vehicleIDs ...int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := strings.ToUpper(company)
	if f.assigned[code] == nil {
		f.assigned[code] = map[int64][]int64{}
	}
	f.assigned[code][userID] = append(f.assigned[code][userID], vehicleIDs...)
}

// addVehicle registers a vehicle row.
func (f *fakeStore) addVehicle(company string, v models.Vehicle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := strings.ToUpper(company)
	f.vehicles[code] = append(f.vehicles[code], v)
}

// auditsSnapshot copies the recorded audit rows.
func (f *fakeStore) auditsSnapshot() []AuditRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]AuditRow, len(f.audits))
	copy(out, f.audits)
	return out
}

// UserByEmail implements Store.
func (f *fakeStore) UserByEmail(_ context.Context, email string) (*UserRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.users[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return nil, nil
	}
	clone := *rec
	return &clone, nil
}

// UserByID implements Store.
func (f *fakeStore) UserByID(_ context.Context, id int64) (*UserRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.users {
		if rec.ID == id {
			clone := *rec
			return &clone, nil
		}
	}
	return nil, nil
}

// UserEmailExists implements Store.
func (f *fakeStore) UserEmailExists(ctx context.Context, email string) (bool, error) {
	rec, err := f.UserByEmail(ctx, email)
	return rec != nil, err
}

// CreateUser implements Store. Like the SQL path
// (`is_active = TRUE, email_verified = FALSE`) the fake applies the same
// defaults so the login behaviour of a freshly created account matches
// production.
func (f *fakeStore) CreateUser(_ context.Context, u UserRecord) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.users[strings.ToLower(u.Email)]; exists {
		return 0, errors.New("duplicate email")
	}
	next := int64(1)
	for _, rec := range f.users {
		if rec.ID >= next {
			next = rec.ID + 1
		}
	}
	u.ID = next
	u.IsActive = true
	if u.FullName == "" {
		u.FullName = "User " + itoa(next)
	}
	f.users[strings.ToLower(u.Email)] = &u
	f.createdUsers = append(f.createdUsers, u)
	return next, nil
}

// RecordLoginSuccess implements Store.
func (f *fakeStore) RecordLoginSuccess(_ context.Context, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.users {
		if rec.ID == userID {
			rec.FailedAttempts = 0
			rec.LockedUntil = nil
		}
	}
	return nil
}

// RecordLoginFailure implements Store.
func (f *fakeStore) RecordLoginFailure(_ context.Context, userID int64, attempts int, lockedUntil *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.users {
		if rec.ID == userID {
			rec.FailedAttempts = attempts
			rec.LockedUntil = lockedUntil
		}
	}
	return nil
}

// CompanyExists implements Store.
func (f *fakeStore) CompanyExists(_ context.Context, code string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.companies[strings.ToUpper(code)], nil
}

// WriteAudit implements Store.
func (f *fakeStore) WriteAudit(_ context.Context, rows []AuditRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auditErr != nil {
		return f.auditErr
	}
	f.audits = append(f.audits, rows...)
	return nil
}

// sortedIDs is a small helper for deterministic assertions.
func sortedIDs(ids []int64) []int64 {
	out := append([]int64{}, ids...)
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}
