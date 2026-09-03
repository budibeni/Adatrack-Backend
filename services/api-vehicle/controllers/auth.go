package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ajb_gps/api-vehicle/models"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// tokenClaims is the JWT payload — IDENTICAL ke service-websocket (B2) agar
// satu token dipakai lintas kedua API (JWT_SECRET sama, PRD §7).
type tokenClaims struct {
	UserID      uint64  `json:"user_id"`
	CompanyCode string  `json:"company_code"`
	CompanyID   int64   `json:"company_id,omitempty"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty"`
	jwt.RegisteredClaims
}

// loginRequest is the POST /api/v1/auth/login body (PRD §6.1: email based).
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// signTokenClaims creates an HS256 JWT carrying the GAP #2 payload + tenant.
func signTokenClaims(mu models.MasterUser, role string, vehicleIDs []int64) (string, int64, error) {
	now := time.Now()
	exp := now.Add(appCfg.JWT.Expiry)
	claims := &tokenClaims{
		UserID:      mu.ID,
		CompanyCode: mu.CompanyCode,
		CompanyID:   mu.CompanyID,
		Email:       mu.Email,
		Role:        role,
		VehicleIDs:  vehicleIDs,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", mu.ID),
			ID:        newJTI(), // jti untuk revocation (B4 hardening)
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(appCfg.JWT.Secret))
	if err != nil {
		return "", 0, fmt.Errorf("sign token: %w", err)
	}
	return signed, int64(exp.Sub(now).Seconds()), nil
}

// parseToken validates a JWT and returns its claims.
func parseToken(tokenStr string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(appCfg.JWT.Secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.CompanyCode == "" {
		return nil, errors.New("token missing company_code")
	}
	return claims, nil
}

// extractToken reads a Bearer token from the Authorization header or query.
func extractToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return c.Query("token")
}

// loadMasterUserByEmail finds a user in master.users (GLOBAL auth authority).
// Soft-deleted users (deleted_at IS NOT NULL) are excluded — they cannot log in.
// Enterprise-standard fields (email_verified, mfa_enabled, locked_until, etc.)
// are loaded so the login handler can enforce account lockout and MFA gating.
func loadMasterUserByEmail(db *sql.DB, email string) (*models.MasterUser, error) {
	u := &models.MasterUser{}
	var (
		passwordChangedAt, lockedUntil, deletedAt, lastLogin sql.NullTime
		createdBy, updatedBy                                 sql.NullInt64
	)
	err := db.QueryRow(`SELECT id, company_id, company_code, email, password_hash,
                        COALESCE(full_name, ''),
                        COALESCE(username, ''), COALESCE(first_name, ''), COALESCE(last_name, ''),
                        COALESCE(phone_number, ''), email_verified, phone_verified, mfa_enabled,
                        COALESCE(locale, 'id'), COALESCE(avatar_url, ''),
                        COALESCE(failed_login_attempts, 0),
                        password_changed_at, locked_until, deleted_at, last_login,
                        created_by, updated_by,
                        role, status
                       FROM users
                       WHERE email = ? AND deleted_at IS NULL
                       LIMIT 1`, strings.TrimSpace(email)).
		Scan(&u.ID, &u.CompanyID, &u.CompanyCode, &u.Email, &u.PasswordHash,
			&u.FullName,
			&u.Username, &u.FirstName, &u.LastName,
			&u.PhoneNumber, &u.EmailVerified, &u.PhoneVerified, &u.MFAEnabled,
			&u.Locale, &u.AvatarURL,
			&u.FailedLoginAttempts,
			&passwordChangedAt, &lockedUntil, &deletedAt, &lastLogin,
			&createdBy, &updatedBy,
			&u.Role, &u.Status)
	if err != nil {
		return nil, err
	}
	if passwordChangedAt.Valid {
		u.PasswordChangedAt = &passwordChangedAt.Time
	}
	if lockedUntil.Valid {
		u.LockedUntil = &lockedUntil.Time
	}
	if deletedAt.Valid {
		u.DeletedAt = &deletedAt.Time
	}
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.Time
	}
	if createdBy.Valid {
		cb := uint64(createdBy.Int64)
		u.CreatedBy = &cb
	}
	if updatedBy.Valid {
		ub := uint64(updatedBy.Int64)
		u.UpdatedBy = &ub
	}
	return u, nil
}

// loadCompanyAccess loads the per-company registry (user_company_access):
// local id, user_id (kanonik utk FK created_by/acknowledged_by), role override,
// is_active.
func loadCompanyAccess(db *sql.DB, masterUserID uint64) (ucaID, companyUserID uint64, roleOverride string, isActive bool, err error) {
	var role sql.NullString
	err = db.QueryRow(`SELECT id, user_id, role_override, is_active
FROM user_company_access WHERE user_id = ? LIMIT 1`, masterUserID).
		Scan(&ucaID, &companyUserID, &role, &isActive)
	if err != nil {
		return 0, 0, "", false, err
	}
	if role.Valid {
		roleOverride = role.String
	}
	return ucaID, companyUserID, roleOverride, isActive, nil
}

// effectiveRole derives the per-company role: role_override bila ada.
func effectiveRole(globalRole, roleOverride string) string {
	if strings.TrimSpace(roleOverride) != "" {
		return roleOverride
	}
	return globalRole
}

// userVehicleIDs returns the vehicle IDs a user can access (user_vehicles).
func userVehicleIDs(db *sql.DB, userID uint64) ([]int64, error) {
	set := map[int64]struct{}{}
	rows, err := db.Query(`SELECT vehicle_id FROM user_vehicles WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		set[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// Login rate limiting (GAP #12: 5 attempts / 15 minutes per IP)
// ---------------------------------------------------------------------------

type rateEntry struct {
	count int
	reset time.Time
}

type failureRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateEntry
	max     int
	window  time.Duration
}

func newFailureRateLimiter(max int, window time.Duration) *failureRateLimiter {
	return &failureRateLimiter{entries: make(map[string]*rateEntry), max: max, window: window}
}

func (l *failureRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok || time.Now().After(e.reset) {
		l.entries[key] = &rateEntry{count: 0, reset: time.Now().Add(l.window)}
		return true
	}
	return e.count < l.max
}

func (l *failureRateLimiter) recordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[key]; ok && time.Now().Before(e.reset) {
		e.count++
		return
	}
	l.entries[key] = &rateEntry{count: 1, reset: time.Now().Add(l.window)}
}

func (l *failureRateLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

var loginLimiter *failureRateLimiter
