package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// ReadinessStore reports the persistence readiness of the backing store so
// /healthz works through the Store abstraction (unit tests use a fake).
type ReadinessStore interface {
	Readiness(ctx context.Context) error
}

// PostgresStore implements Store on top of the shared tenant Manager (master
// pool + one pre-warmed pool per company schema).
type PostgresStore struct {
	tenants *tenant.Manager
}

// NewPostgresStore wraps the tenant manager.
func NewPostgresStore(tenants *tenant.Manager) *PostgresStore {
	return &PostgresStore{tenants: tenants}
}

// Master exposes the master pool (health checks).
func (s *PostgresStore) Master() *internal.DBPool { return s.tenants.Master() }

// TenantHealth aggregates every tenant pool's readiness (healthz).
func (s *PostgresStore) TenantHealth(ctx context.Context) error { return s.tenants.Health(ctx) }

// Readiness implements ReadinessStore: the master schema plus every pre-warmed
// tenant pool must answer (PRD §10.2).
func (s *PostgresStore) Readiness(ctx context.Context) error {
	if err := s.tenants.Master().Ping(ctx); err != nil {
		return fmt.Errorf("postgres_master: %w", err)
	}
	if err := s.tenants.Health(ctx); err != nil {
		return fmt.Errorf("tenant_pools: %w", err)
	}
	return nil
}

// tenantPool resolves a company code to its pool. The code is sanitised by
// tenant.NormalizeCode before it ever reaches this point, so nothing
// user-controlled is interpolated into SQL.
func (s *PostgresStore) tenantPool(companyCode string) (*internal.DBPool, error) {
	code := strings.ToUpper(strings.TrimSpace(companyCode))
	start := time.Now()
	pool, err := s.tenants.DB(code)
	observeTenantRoute(start)
	if err != nil {
		return nil, err
	}
	st := pool.DB.Stats()
	tenantDBConnectionsActive.WithLabelValues(code).Set(float64(st.InUse))
	return pool, nil
}

// PingTenant verifies one tenant pool answers (readiness probe).
func (s *PostgresStore) PingTenant(ctx context.Context, companyCode string) error {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return err
	}
	return pool.Ping(ctx)
}

// ProvisionTenant creates the tenant schema + applies every company migration
// (FR-5.5 step 1, PRD §14.5 step 4).
func (s *PostgresStore) ProvisionTenant(ctx context.Context, opts tenant.ProvisionOptions) (*tenant.ProvisionResult, error) {
	return s.tenants.ProvisionCompany(ctx, opts)
}

// UserByEmail loads the auth authority row for an email (PRD §9.1).
func (s *PostgresStore) UserByEmail(ctx context.Context, email string) (*UserRecord, error) {
	return scanUser(s.tenants.Master().DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(company_code, ''), email, full_name, password_hash, global_role,
		       is_active, must_change_password, failed_login_attempts, locked_until
		FROM tm_users
		WHERE lower(email) = lower($1) AND deleted_at IS NULL`, strings.TrimSpace(email)))
}

// UserByID loads a user for token validation on every authenticated request.
func (s *PostgresStore) UserByID(ctx context.Context, id int64) (*UserRecord, error) {
	return scanUser(s.tenants.Master().DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(company_code, ''), email, full_name, password_hash, global_role,
		       is_active, must_change_password, failed_login_attempts, locked_until
		FROM tm_users
		WHERE id = $1 AND deleted_at IS NULL`, id))
}

// scanUser maps one row; sql.ErrNoRows becomes (nil, nil) so callers can decide
// between 401 (unknown credential) and 404 (unknown resource).
func scanUser(row *sql.Row) (*UserRecord, error) {
	var (
		u           UserRecord
		lockedUntil sql.NullTime
	)
	if err := row.Scan(&u.ID, &u.CompanyCode, &u.Email, &u.FullName, &u.PasswordHash,
		&u.GlobalRole, &u.IsActive, &u.MustChangePassword, &u.FailedAttempts, &lockedUntil); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: user lookup: %w", err)
	}
	if lockedUntil.Valid {
		t := lockedUntil.Time
		u.LockedUntil = &t
	}
	return &u, nil
}

// UserEmailExists checks uniqueness before inserting (FR-5.6 → 409).
func (s *PostgresStore) UserEmailExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := s.tenants.Master().DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tm_users WHERE lower(email) = lower($1) AND deleted_at IS NULL)`,
		strings.TrimSpace(email)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: email exists: %w", err)
	}
	return exists, nil
}

// CompanyExists reports whether a tenant is registered (FR-5.5 idempotency).
func (s *PostgresStore) CompanyExists(ctx context.Context, code string) (bool, error) {
	var exists bool
	err := s.tenants.Master().DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tm_companies WHERE code = $1 AND deleted_at IS NULL)`,
		strings.ToUpper(strings.TrimSpace(code))).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: company exists: %w", err)
	}
	return exists, nil
}

// CreateUser inserts the master auth row (bcrypt hash supplied by the caller) and
// returns its id (FR-5.6).
func (s *PostgresStore) CreateUser(ctx context.Context, u UserRecord) (int64, error) {
	var id int64
	err := s.tenants.Master().DB.QueryRowContext(ctx, `
		INSERT INTO tm_users (company_id, company_code, email, password_hash, full_name,
		                      global_role, is_active, must_change_password, email_verified)
		VALUES ((SELECT id FROM tm_companies WHERE code = $1), $1, $2, $3, $4, $5, TRUE, $6, FALSE)
		RETURNING id`,
		strings.ToUpper(strings.TrimSpace(u.CompanyCode)), u.Email, u.PasswordHash,
		u.FullName, u.GlobalRole, u.MustChangePassword).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create user: %w", err)
	}
	return id, nil
}

// RecordLoginSuccess clears the failure counter + lockout and stamps last_login_at.
func (s *PostgresStore) RecordLoginSuccess(ctx context.Context, userID int64) error {
	_, err := s.tenants.Master().DB.ExecContext(ctx, `
		UPDATE tm_users
		SET failed_login_attempts = 0, locked_until = NULL, last_login_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("store: record login success: %w", err)
	}
	return nil
}

// RecordLoginFailure increments the failure counter and (optionally) locks the
// account (PRD §9.1 login lockout).
func (s *PostgresStore) RecordLoginFailure(ctx context.Context, userID int64, attempts int, lockedUntil *time.Time) error {
	_, err := s.tenants.Master().DB.ExecContext(ctx, `
		UPDATE tm_users
		SET failed_login_attempts = $2, locked_until = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, userID, attempts, lockedUntil)
	if err != nil {
		return fmt.Errorf("store: record login failure: %w", err)
	}
	return nil
}

// WriteAudit appends audit rows to master `tm_audit_logs` in one batch (append
// only; the table itself rejects UPDATE/DELETE — PRD §9.4).
func (s *PostgresStore) WriteAudit(ctx context.Context, rows []AuditRow) error {
	if len(rows) == 0 {
		return nil
	}
	columns := []string{"action", "outcome", "actor_user_id", "actor_email", "actor_role",
		"actor_ip", "actor_user_agent", "company_code", "entity_type", "entity_id",
		"before_state", "after_state", "reason", "request_id"}
	values := make([][]any, 0, len(rows))
	for _, r := range rows {
		values = append(values, []any{
			r.Action, r.Outcome, nullableInt(r.ActorUserID), nullableString(r.ActorEmail),
			nullableString(r.ActorRole), nullableString(r.ActorIP), nullableString(r.ActorUserAgent),
			nullableString(r.CompanyCode), nullableString(r.EntityType), nullableString(r.EntityID),
			jsonOrNil(r.BeforeState), jsonOrNil(r.AfterState), nullableString(r.Reason),
			nullableString(r.RequestID),
		})
	}
	_, err := internal.BatchInsert(ctx, s.tenants.Master(), "tm_audit_logs", columns, values)
	return err
}

// nullableString maps "" to SQL NULL (audit columns are nullable by design).
func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// nullableInt maps 0 to SQL NULL.
func nullableInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// jsonOrNil marshals a JSONB audit payload ("before"/"after" state); a value
// that cannot be encoded is dropped rather than failing the audit write.
func jsonOrNil(v any) any {
	if v == nil {
		return nil
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(body)
}
