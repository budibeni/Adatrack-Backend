package controllers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"ajb_gps/service-websocket/models"
)

// Claims is the JWT payload (PRD §9.1): the SAME claims must work for
// service-websocket and api-vehicle (B3 interop).
type Claims struct {
	UserID      int64   `json:"user_id"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	CompanyCode string  `json:"company_code"`
	GlobalRole  string  `json:"global_role"`
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty"`
	jwt.RegisteredClaims
}

// IsPlatform reports the platform (governance) identity (PRD §3.1).
func (c *Claims) IsPlatform() bool {
	return c.GlobalRole == models.RoleSuperAdmin && c.CompanyCode == models.PlatformCompanyCode
}

// refreshRecord is the Redis value behind an opaque refresh token: only the
// SHA-256 hash is used as the key, the raw token is never stored (PRD §9.1).
type refreshRecord struct {
	UserID      int64  `json:"user_id"`
	Email       string `json:"email"`
	CompanyCode string `json:"company_code"`
	JTI         string `json:"jti"`
	IssuedAt    int64  `json:"iat"`
}

// tenantIdentity is the resolved (role + row-level grants) identity of one user
// inside their tenant (PRD §3.1/§9.2).
type tenantIdentity struct {
	user     models.User
	assigned []int64
	// allVehicles is true for Admin/Manager (tenant-wide read) and platform admins.
	allVehicles bool
}

// resolveIdentity resolves the effective role from `tm_user_company_access`
// (role_override wins) and the row-level vehicle grants from `tm_user_vehicles`.
func (a *AuthService) resolveIdentity(ctx context.Context, u *UserRecord) (*tenantIdentity, error) {
	identity := &tenantIdentity{user: models.User{
		ID:                 u.ID,
		Email:              u.Email,
		FullName:           u.FullName,
		GlobalRole:         u.GlobalRole,
		Role:               u.GlobalRole,
		CompanyCode:        strings.ToUpper(strings.TrimSpace(u.CompanyCode)),
		MustChangePassword: u.MustChangePassword,
		IsActive:           u.IsActive,
	}}

	// Platform tier (PRD §3.1): context `default` + SuperAdmin.
	if identity.user.GlobalRole == models.RoleSuperAdmin && identity.user.CompanyCode == models.PlatformCompanyCode {
		identity.allVehicles = true
		return identity, nil
	}

	roleOverride, active, found, err := a.store.TenantAccess(ctx, identity.user.CompanyCode, u.ID)
	if err != nil {
		return nil, errUnavailable("authorization backend unavailable")
	}
	if found {
		if !active {
			return nil, NewAPIError(401, CodeAccountInactive, "tenant access revoked")
		}
		if roleOverride != "" {
			identity.user.Role = roleOverride
		}
	} else if identity.user.GlobalRole != models.RoleSuperAdmin {
		// No membership row and not a platform admin → no tenant access at all.
		rbacDenied.WithLabelValues("not_a_member").Inc()
		return nil, errForbidden(CodeForbidden, "user has no access to this tenant")
	}

	switch identity.user.Role {
	case models.RoleAdmin, models.RoleManager:
		identity.allVehicles = true
	default:
		ids, ierr := a.store.AssignedVehicleIDs(ctx, identity.user.CompanyCode, u.ID)
		if ierr != nil {
			return nil, errUnavailable("authorization backend unavailable")
		}
		identity.assigned = ids
	}
	return identity, nil
}

// issueTokens mints the access JWT + the opaque refresh token (PRD §9.1/FR-5.7).
func (a *AuthService) issueTokens(ctx context.Context, identity *tenantIdentity, vehicleIDs []int64) (models.TokenPair, error) {
	now := a.now()
	jti, err := randomToken(16)
	if err != nil {
		return models.TokenPair{}, errInternal("could not generate token identifier")
	}
	claims := Claims{
		UserID:      identity.user.ID,
		Email:       identity.user.Email,
		Role:        identity.user.Role,
		CompanyCode: identity.user.CompanyCode,
		GlobalRole:  identity.user.GlobalRole,
		VehicleIDs:  vehicleIDs,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    a.cfg.JWTIssuer,
			Subject:   itoa(identity.user.ID),
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-a.cfg.ClockSkew)),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.cfg.AccessExpiry)),
		},
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.cfg.JWTSecret))
	if err != nil {
		return models.TokenPair{}, errInternal("could not sign access token")
	}

	rawRefresh, err := randomToken(32) // 256-bit opaque refresh token
	if err != nil {
		return models.TokenPair{}, errInternal("could not generate refresh token")
	}
	record, err := json.Marshal(refreshRecord{
		UserID:      identity.user.ID,
		Email:       identity.user.Email,
		CompanyCode: identity.user.CompanyCode,
		JTI:         jti,
		IssuedAt:    now.Unix(),
	})
	if err != nil {
		return models.TokenPair{}, errInternal("could not encode refresh record")
	}
	if serr := a.kv.Set(ctx, a.cfg.RefreshPrefix+hashToken(rawRefresh), string(record), a.cfg.RefreshExpiry); serr != nil {
		return models.TokenPair{}, errUnavailable("token store unavailable")
	}

	return models.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		TokenType:    "Bearer",
		ExpiresIn:    int64(a.cfg.AccessExpiry.Seconds()),
	}, nil
}

// ParseAccessToken verifies signature, issuer and expiry (with clock skew) and
// returns the claims. Revocation is a separate check (Revoked).
func (a *AuthService) ParseAccessToken(raw string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(strings.TrimSpace(raw), claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		return []byte(a.cfg.JWTSecret), nil
	}, jwt.WithIssuer(a.cfg.JWTIssuer), jwt.WithLeeway(a.cfg.ClockSkew), jwt.WithExpirationRequired())
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, NewAPIError(401, CodeTokenExpired, "access token expired")
		}
		return nil, NewAPIError(401, CodeTokenInvalid, "invalid access token")
	}
	if !token.Valid {
		return nil, NewAPIError(401, CodeTokenInvalid, "invalid access token")
	}
	if claims.UserID <= 0 || claims.CompanyCode == "" {
		return nil, NewAPIError(401, CodeTokenInvalid, "incomplete access token")
	}
	claims.CompanyCode = strings.ToUpper(strings.TrimSpace(claims.CompanyCode))
	return claims, nil
}

// Revoked reports whether a token identifier is denylisted (FR-5.7). An
// unverifiable revocation state is reported as an error so the caller can
// fail-closed instead of accepting a possibly revoked token.
func (a *AuthService) Revoked(ctx context.Context, claims *Claims) (bool, error) {
	if !a.cfg.RevocationEnabled || claims == nil || claims.ID == "" {
		return false, nil
	}
	val, err := a.kv.Get(ctx, a.cfg.DenylistPrefix+claims.ID)
	if err != nil {
		return false, err
	}
	return val != "", nil
}

// DenyToken denylists a token identifier for its remaining lifetime (logout).
func (a *AuthService) DenyToken(ctx context.Context, claims *Claims) error {
	if !a.cfg.RevocationEnabled || claims == nil || claims.ID == "" {
		return nil
	}
	ttl := a.cfg.AccessExpiry
	if claims.ExpiresAt != nil {
		if remaining := claims.ExpiresAt.Time.Sub(a.now()); remaining > 0 {
			ttl = remaining
		}
	}
	return a.kv.Set(ctx, a.cfg.DenylistPrefix+claims.ID, "revoked", ttl)
}

// RevokeRefresh drops the refresh record so the token cannot be rotated again.
func (a *AuthService) RevokeRefresh(ctx context.Context, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return a.kv.Del(ctx, a.cfg.RefreshPrefix+hashToken(raw))
}

// hashToken computes the SHA-256 hex digest used as the refresh-token key.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// randomToken returns a base64url-encoded cryptographically random token.
func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// dummyBcryptHash is a real cost-12 hash used to equalise the timing of a login
// attempt for an unknown account (prevents user enumeration).
const dummyBcryptHash = "$2a$12$68Y3c8vQndkODvLUKj52RuC02x8yLdpJorBYysKXAkLvV9mgf3YTK"

// AuthService issues and validates credentials (PRD §9.1, FR-5.7).
type AuthService struct {
	cfg     Settings
	store   Store
	kv      KVStore
	auditor *Auditor
	// now is a test seam (token expiry / lockout windows).
	now func() time.Time
}

// NewAuthService builds the auth service.
func NewAuthService(cfg Settings, store Store, kv KVStore, auditor *Auditor) *AuthService {
	return &AuthService{cfg: cfg, store: store, kv: kv, auditor: auditor, now: time.Now}
}

// loginRateLimit enforces PRD §8.4 (5 attempts / 15 min per IP+email). A Redis
// failure is fail-closed (503) rather than silently unlimited.
func (a *AuthService) loginRateLimit(ctx context.Context, ip, email string) error {
	if a.cfg.LoginRateLimit <= 0 || a.cfg.LoginRateWindow <= 0 {
		return nil
	}
	key := "adatrack_gps:auth:login:" + ip + ":" + hashToken(email)[:16]
	n, err := a.kv.Incr(ctx, key)
	if err != nil {
		return errUnavailable("rate limiter unavailable")
	}
	if n == 1 {
		if err := a.kv.Expire(ctx, key, a.cfg.LoginRateWindow); err != nil {
			return errUnavailable("rate limiter unavailable")
		}
	}
	if n > int64(a.cfg.LoginRateLimit) {
		return errRateLimited("too many login attempts, try again later")
	}
	return nil
}

// auditLogin writes one LOGIN_* row synchronously (fail-closed, PRD §9.4).
func (a *AuthService) auditLogin(ctx context.Context, action, outcome string, userID int64, email, role,
	ip, userAgent, requestID, reason string) error {
	if a.auditor == nil {
		return nil
	}
	return a.auditor.RecordSync(ctx, AuditRow{
		Action:         action,
		Outcome:        outcome,
		ActorUserID:    userID,
		ActorEmail:     email,
		ActorRole:      role,
		ActorIP:        ip,
		ActorUserAgent: userAgent,
		EntityType:     "user",
		EntityID:       itoa(userID),
		Reason:         reason,
		RequestID:      requestID,
	})
}

// Login authenticates an email/password pair (PRD §9.1) and returns the token
// pair plus the resolved identity.
func (a *AuthService) Login(ctx context.Context, req models.LoginRequest, ip, userAgent, requestID string) (*models.LoginResponse, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))

	if err := a.loginRateLimit(ctx, ip, email); err != nil {
		loginAttempts.WithLabelValues("rate_limited").Inc()
		_ = a.auditLogin(ctx, ActionLoginFailure, OutcomeDenied, 0, email, "", ip, userAgent, requestID,
			"login rate limit exceeded")
		return nil, err
	}

	user, err := a.store.UserByEmail(ctx, email)
	if err != nil {
		return nil, errUnavailable("authentication backend unavailable")
	}
	if user == nil {
		// Constant-cost compare so a missing account is indistinguishable from a
		// wrong password (no user-enumeration oracle).
		_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(req.Password))
		loginAttempts.WithLabelValues("failure").Inc()
		_ = a.auditLogin(ctx, ActionLoginFailure, OutcomeFailure, 0, email, "", ip, userAgent, requestID,
			"unknown account")
		return nil, invalidCredentials()
	}

	now := a.now()
	if user.LockedUntil != nil && now.Before(*user.LockedUntil) {
		loginAttempts.WithLabelValues("locked").Inc()
		_ = a.auditLogin(ctx, ActionLoginFailure, OutcomeDenied, user.ID, email, user.GlobalRole, ip, userAgent, requestID,
			"account locked until "+user.LockedUntil.UTC().Format(time.RFC3339))
		return nil, NewAPIError(401, CodeAccountLocked, "account locked after repeated failed logins")
	}
	if !user.IsActive {
		loginAttempts.WithLabelValues("failure").Inc()
		_ = a.auditLogin(ctx, ActionLoginFailure, OutcomeDenied, user.ID, email, user.GlobalRole, ip, userAgent, requestID,
			"inactive account")
		return nil, NewAPIError(401, CodeAccountInactive, "account is inactive")
	}

	if berr := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); berr != nil {
		attempts := user.FailedAttempts + 1
		var lockedUntil *time.Time
		if a.cfg.LoginLockoutThreshold > 0 && attempts >= a.cfg.LoginLockoutThreshold {
			until := now.Add(a.cfg.LoginLockoutDuration)
			lockedUntil = &until
		}
		if uerr := a.store.RecordLoginFailure(ctx, user.ID, attempts, lockedUntil); uerr != nil {
			slog.Warn("service-websocket: failed to record login failure", "user_id", user.ID, "error", uerr)
		}
		loginAttempts.WithLabelValues("failure").Inc()
		_ = a.auditLogin(ctx, ActionLoginFailure, OutcomeFailure, user.ID, email, user.GlobalRole, ip, userAgent, requestID,
			"invalid credentials")
		return nil, invalidCredentials()
	}

	identity, err := a.resolveIdentity(ctx, user)
	if err != nil {
		return nil, err
	}

	if uerr := a.store.RecordLoginSuccess(ctx, user.ID); uerr != nil {
		slog.Warn("service-websocket: failed to record login success", "user_id", user.ID, "error", uerr)
	}

	pair, err := a.issueTokens(ctx, identity, identity.assigned)
	if err != nil {
		return nil, err
	}

	// Fail-closed: a successful login MUST be audited (PRD §9.4).
	if aerr := a.auditLogin(ctx, ActionLoginSuccess, OutcomeSuccess, identity.user.ID, identity.user.Email,
		identity.user.Role, ip, userAgent, requestID, ""); aerr != nil {
		return nil, errUnavailable("audit trail unavailable")
	}
	loginAttempts.WithLabelValues("success").Inc()

	return &models.LoginResponse{TokenPair: pair, User: identity.user}, nil
}
