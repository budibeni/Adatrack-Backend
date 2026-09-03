package controllers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// authLoginHandler handles POST /api/v1/auth/login. Master DB = otoritas
// autentikasi global (email → company_code + password verify); role & vehicle
// scope di-resolve dari company DB (PRD §6.1) — pola identik service-websocket.
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
		db, err := appTenant.DB(u.CompanyCode)
		if err != nil || db == nil {
			slog.Warn("login rejected: company db unavailable", "email", req.Email, "company", u.CompanyCode, "error", err)
			writeError(c, http.StatusForbidden, "FORBIDDEN", "akses ke company tidak tersedia")
			return
		}
		tenantDB = db

		_, _, roleOverride, accessActive, lerr := loadCompanyAccess(db, u.ID)
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

// auditLogin records login events (sukses/gagal) — GAP #10/#12 audit:
// structured log SELALU + baris master.audit_logs (async, best-effort).
func auditLogin(userID uint64, email, ip, userAgent string, success bool) {
	if success {
		slog.Info("login success", "user_id", userID, "email", email, "ip", ip)
	} else {
		slog.Warn("login failure", "email", email, "ip", ip)
	}
	eventType := "LOGIN_FAILURE"
	if success {
		eventType = "LOGIN_SUCCESS"
	}
	internal.LogAudit(auditDB(), internal.AuditEntry{
		UserID:    userID,
		EventType: eventType,
		Action:    "login",
		Entity:    "user",
		EntityID:  email,
		IP:        ip,
		UserAgent: userAgent,
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
