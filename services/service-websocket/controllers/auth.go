package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// tokenClaims is the JWT payload (GAP #2 + FR-5.1):
// user_id, company_code, company_id, email, role, vehicle_ids, exp, iat.
// company_code wajib agar setiap request di-rute ke adatrack_gps_{company_code}.
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

// signToken creates an HS256 JWT carrying the GAP #2 payload + tenant context.
func signToken(cfg *internal.Config, mu models.MasterUser, role string, vehicleIDs []int64) (string, int64, error) {
	now := time.Now()
	exp := now.Add(cfg.JWT.Expiry)
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
	signed, err := token.SignedString([]byte(cfg.JWT.Secret))
	if err != nil {
		return "", 0, fmt.Errorf("sign token: %w", err)
	}
	return signed, int64(exp.Sub(now).Seconds()), nil
}

// parseToken validates a JWT and returns its claims.
func parseToken(cfg *internal.Config, tokenStr string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(cfg.JWT.Secret), nil
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

// loadCompanyAccess loads the per-company registry (user_company_access) from
// the company DB: local id, role_override (nil → pakai global role), is_active.
func loadCompanyAccess(db *sql.DB, masterUserID uint64) (companyUserID uint64, roleOverride string, isActive bool, err error) {
	var role sql.NullString
	err = db.QueryRow(`SELECT id, role_override, is_active
FROM user_company_access WHERE user_id = ? LIMIT 1`, masterUserID).
		Scan(&companyUserID, &role, &isActive)
	if err != nil {
		return 0, "", false, err
	}
	if role.Valid {
		roleOverride = role.String
	}
	return companyUserID, roleOverride, isActive, nil
}

// effectiveRole derives the per-company role: role_override bila ada, else the
// global master role (PRD §6.1 / FR-5.1).
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

type failureKey struct {
	firstFail time.Time
	count     int
}

type failureRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*failureKey
	max     int
	window  time.Duration
}

func newFailureRateLimiter(max int, window time.Duration) *failureRateLimiter {
	return &failureRateLimiter{entries: make(map[string]*failureKey), max: max, window: window}
}

func (l *failureRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return true
	}
	if time.Since(e.firstFail) > l.window {
		delete(l.entries, key)
		return true
	}
	return e.count < l.max
}

func (l *failureRateLimiter) recordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[key]; ok {
		if time.Since(e.firstFail) > l.window {
			l.entries[key] = &failureKey{firstFail: time.Now(), count: 1}
			return
		}
		e.count++
		return
	}
	l.entries[key] = &failureKey{firstFail: time.Now(), count: 1}
}

func (l *failureRateLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// loginLimiter dibangun di setupRouter (RATE_LIMIT_LOGIN_ATTEMPTS≈5/15 mnt).
var loginLimiter *failureRateLimiter

// ---------------------------------------------------------------------------
// Login handler
// ---------------------------------------------------------------------------

// authLoginHandler handles POST /api/v1/auth/login.
// Master DB = otoritas autentikasi global (email → company_code + password
// verify); role & vehicle scope di-resolve dari company DB (PRD §6.1).
func authLoginHandler(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "request body must include email and password")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "email and password are required")
		return
	}

	clientIP := c.ClientIP()
	if !loginLimiter.allow(clientIP) {
		slog.Warn("login rate limited", "client_ip", clientIP, "email", req.Email)
		writeError(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "too many login attempts, retry later")
		return
	}

	u, err := loadMasterUserByEmail(masterDB(), req.Email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			loginLimiter.recordFailure(clientIP)
			auditLogin(0, req.Email, clientIP, c.Request.UserAgent(), false)
			slog.Warn("login failed: unknown user", "email", req.Email, "client_ip", clientIP)
			writeError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "email or password is incorrect")
			return
		}
		slog.Error("login: query master user failed", "error", err, "email", req.Email)
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
		return
	}

	// Platform tier vs tenant resolution (PRD §6.1 governance):
	// konteks 'default' = platform primer DI LUAR semua tenant — tidak ada
	// company DB & user_company_access yang perlu di-resolve; hanya SuperAdmin.
	var (
		role        string
		accessValid bool
		tenantDB    *sql.DB
	)
	if tenant.IsPlatformCompany(u.CompanyCode) {
		if !tenant.IsPlatformRole(u.Role) {
			slog.Warn("login rejected: platform context requires super admin", "email", req.Email, "role", u.Role)
			writeError(c, http.StatusForbidden, "FORBIDDEN", "platform context is reserved for super admins")
			return
		}
		role = u.Role
		accessValid = true
	} else {
		// Tenant resolution: user harus punya DB company yang aktif.
		db, err := companyDBByCode(u.CompanyCode)
		if err != nil || db == nil {
			slog.Warn("login rejected: company db unavailable", "email", req.Email, "company", u.CompanyCode, "error", err)
			writeError(c, http.StatusForbidden, "FORBIDDEN", "akses ke company tidak tersedia")
			return
		}
		tenantDB = db

		// Per-company active check via user_company_access.
		_, roleOverride, accessActive, lerr := loadCompanyAccess(db, u.ID)
		if lerr != nil {
			if errors.Is(lerr, sql.ErrNoRows) {
				slog.Warn("login rejected: no company access", "email", req.Email, "company", u.CompanyCode)
				writeError(c, http.StatusForbidden, "FORBIDDEN", "no access to this company")
				return
			}
			slog.Error("login: company access query failed", "error", lerr, "user_id", u.ID)
			writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
			return
		}

		role = effectiveRole(u.Role, roleOverride)
		accessValid = accessActive
	}

	if u.Status != "active" || !accessValid {
		slog.Warn("login rejected: account not active", "email", req.Email, "status", u.Status, "company_active", accessValid)
		writeError(c, http.StatusForbidden, "ACCOUNT_INACTIVE", "account is not active")
		return
	}

	// Enterprise security: reject login if account is locked (failed-attempt lockout).
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		slog.Warn("login rejected: account locked", "email", req.Email, "locked_until", u.LockedUntil.Format(time.RFC3339))
		writeError(c, http.StatusForbidden, "ACCOUNT_LOCKED", "account is locked due to too many failed login attempts")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		loginLimiter.recordFailure(clientIP)
		auditLogin(u.ID, req.Email, clientIP, c.Request.UserAgent(), false)
		// Enterprise security: increment failed login attempts + lock if threshold exceeded.
		recordFailedLogin(masterDB(), u.ID, appCfg.RateLimit.LoginLockoutThreshold, appCfg.RateLimit.LoginLockoutWindow)
		slog.Warn("login failed: bad password", "email", req.Email, "client_ip", clientIP, "failed_attempts", u.FailedLoginAttempts+1)
		writeError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "email or password is incorrect")
		return
	}

	var vehicleIDs []int64
	if !isAdminRole(role) && tenantDB != nil { // platform tier tidak punya vehicle scope
		vehicleIDs, err = userVehicleIDs(tenantDB, u.ID)
		if err != nil {
			slog.Error("login: load vehicle ids failed", "error", err, "user_id", u.ID)
			writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
			return
		}
	}

	token, expSec, refreshToken, err := issueTokenPair(c.Request.Context(), *u, role, vehicleIDs)
	if err != nil {
		slog.Error("login: sign token failed", "error", err, "user_id", u.ID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	loginLimiter.reset(clientIP)
	auditLogin(u.ID, req.Email, clientIP, c.Request.UserAgent(), true)

	// Reset failed-login counter + clear lockout on successful authentication.
	if _, err := masterDB().Exec(`UPDATE users SET last_login = NOW(),
		failed_login_attempts = 0, locked_until = NULL
		WHERE id = ?`, u.ID); err != nil {
		slog.Warn("login: update last_login failed", "error", err, "user_id", u.ID)
	}

	writeSuccess(c, http.StatusOK, models.LoginResponse{
		Token:            token,
		TokenType:        "Bearer",
		ExpiresIn:        expSec,
		RefreshToken:     refreshToken,
		RefreshExpiresIn: int64(appCfg.JWT.RefreshExpiry.Seconds()),
		User: models.AuthUserPayload{
			ID:          u.ID,
			CompanyCode: u.CompanyCode,
			Email:       u.Email,
			Role:        role,
		},
	})
}

// recordFailedLogin increments master.users.failed_login_attempts and, when
// the threshold is reached, locks the account via locked_until (enterprise
// security — GAP #12 account lockout). Errors are logged but never block the
// login response (defense-in-depth on top of the IP rate limiter).
// Only unlocked accounts accumulate attempts; once locked_until is set the
// counter stays frozen until the lock expires or a successful login resets it.
func recordFailedLogin(db *sql.DB, userID uint64, threshold int, lockout time.Duration) {
	if db == nil || threshold <= 0 {
		return
	}
	if _, err := db.Exec(
		`UPDATE users SET failed_login_attempts = failed_login_attempts + 1,
		 locked_until = IF(failed_login_attempts + 1 >= ?, DATE_ADD(NOW(), INTERVAL ? SECOND), locked_until)
		 WHERE id = ? AND (locked_until IS NULL OR locked_until < NOW())`,
		threshold, int(lockout.Seconds()), userID); err != nil {
		slog.Warn("login: record failed attempt failed", "user_id", userID, "error", err)
	}
}
