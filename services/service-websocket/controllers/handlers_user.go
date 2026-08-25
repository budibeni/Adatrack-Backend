package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// PLATFORM-only user provisioning (onboarding tenant, PRD §6.1 governance).
//
// Melengkapi Platform Tier: setelah SuperAdmin mendaftarkan company
// (POST /companies), ia juga yang membuat AKUN PERTAMA tenant tersebut di sini.
// Admin tenant tidak boleh membuat user lintas-konteks — sama seperti tidak
// boleh membuat company baru. Baris yang ditulis:
//
//	master.users                          (otoritas auth global)
//	adatrack_gps_{code}.user_company_access  (wajib agar login lolos RBAC)
//	adatrack_gps_{code}.user_vehicles        (opsional: scope Operator/Driver)
// ---------------------------------------------------------------------------

// userCreateRequest is the POST /api/v1/users body.
type userCreateRequest struct {
	CompanyCode string  `json:"company_code"` // tenant tujuan (WAJIB, bukan 'default')
	Email       string  `json:"email"`        // login identity (WAJIB, unik global)
	Password    string  `json:"password"`     // plaintext; min 8 char (WAJIB)
	FullName    string  `json:"full_name"`    // WAJIB (NOT NULL di skema)
	Role        string  `json:"role"`         // Admin|Manager|Operator|Driver (default Admin)
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty"`
}

// userCreateResponse is returned on 201.
type userCreateResponse struct {
	ID               uint64 `json:"id"`
	Email            string `json:"email"`
	FullName         string `json:"full_name"`
	Role             string `json:"role"`
	CompanyCode      string `json:"company_code"`
	VehiclesAssigned int    `json:"vehicles_assigned,omitempty"`
}

// tenantRoles maps accepted input (case-insensitive) to the canonical value
// stored in master.users.role. Role platform SENGAJA tidak ada di sini:
// identitas platform hanya dibuat lewat migrasi/seed, bukan API.
var tenantRoles = map[string]string{
	"admin":    "Admin",
	"manager":  "Manager",
	"operator": "Operator",
	"driver":   "Driver",
}

// userCreateHandler handles POST /api/v1/users (PLATFORM-only).
func userCreateHandler(c *gin.Context) {
	if !isPlatformAdmin(c) {
		recordRBACDenial(c, "user.create", "platform_only")
		writeError(c, http.StatusForbidden, "PLATFORM_ONLY", "user provisioning is a platform-level operation")
		return
	}

	var req userCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "request body must include company_code, email, password and full_name")
		return
	}
	req.CompanyCode = strings.ToUpper(strings.TrimSpace(req.CompanyCode))
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	roleInput := strings.ToLower(strings.TrimSpace(req.Role))
	if roleInput == "" {
		roleInput = "admin" // default: admin tenant pertama
	}
	// Role platform dilindungi eksplisit SEBELUM map lookup agar pesan error
	// jelas (bukan sekadar "invalid role").
	if tenant.IsPlatformRole(roleInput) {
		writeError(c, http.StatusForbidden, "PLATFORM_ROLE_RESERVED", "the platform role cannot be assigned via this endpoint")
		return
	}

	// --- Validasi murni tanpa sentuh DB (unit-testable) ---
	if req.CompanyCode == "" || req.Email == "" || req.Password == "" || req.FullName == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "company_code, email, password and full_name are required")
		return
	}
	if !strings.Contains(req.Email, "@") || !strings.Contains(strings.SplitN(req.Email, "@", 2)[1], ".") {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "email format is invalid")
		return
	}
	if len(req.Password) < 8 {
		writeError(c, http.StatusBadRequest, "WEAK_PASSWORD", "password must be at least 8 characters")
		return
	}
	role, ok := tenantRoles[roleInput]
	if !ok {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "role must be one of: Admin, Manager, Operator, Driver")
		return
	}
	if tenant.IsPlatformRole(role) {
		writeError(c, http.StatusForbidden, "PLATFORM_ROLE_RESERVED", "the platform role cannot be assigned via this endpoint")
		return
	}
	if tenant.IsPlatformCompany(req.CompanyCode) {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "platform context ('default') hosts no tenant users")
		return
	}

	// --- Infra gate ---
	if appTenant == nil {
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "tenant manager not initialized")
		return
	}
	master := masterDB()

	// 1) Tenant harus terdaftar & aktif (dibuat via POST /companies).
	var companyID int64
	err := master.QueryRowContext(c.Request.Context(),
		`SELECT id FROM companies WHERE code = ? AND is_active = TRUE AND deleted_at IS NULL`,
		req.CompanyCode).Scan(&companyID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "COMPANY_NOT_FOUND", "no active company with that code")
		return
	} else if err != nil {
		slog.Error("user.create: query company failed", "error", err, "company", req.CompanyCode)
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
		return
	}

	// 2) Email unik GLOBAL (constraint skema) — pre-check ramah-user.
	var existing uint64
	err = master.QueryRowContext(c.Request.Context(),
		`SELECT id FROM users WHERE email = ? AND deleted_at IS NULL`, req.Email).Scan(&existing)
	if err == nil {
		writeError(c, http.StatusConflict, "USER_EXISTS", "a user with this email already exists")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		slog.Error("user.create: duplicate check failed", "error", err, "email", req.Email)
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
		return
	}

	// 3) Hash bcrypt cost 12 (PRD §4.2).
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		slog.Error("user.create: bcrypt failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	res, err := master.ExecContext(c.Request.Context(),
		`INSERT INTO users (company_id, company_code, email, password_hash, full_name,
		                    email_verified, locale, role, status)
		 VALUES (?, ?, ?, ?, ?, TRUE, 'id', ?, 'active')`,
		companyID, req.CompanyCode, req.Email, string(hash), req.FullName, role)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate entry") {
			writeError(c, http.StatusConflict, "USER_EXISTS", "a user with this email already exists")
			return
		}
		slog.Error("user.create: insert master user failed", "error", err, "email", req.Email)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	userID, _ := res.LastInsertId()

	// 4) Registry akses di company DB (WAJIB agar login tenant lolos RBAC).
	targetDB, err := appTenant.DB(req.CompanyCode)
	if err != nil || targetDB == nil {
		// No silent drop: baris master sudah ada — log keras agar bisa dipulihkan.
		slog.Error("user.create: company db unavailable AFTER master insert (partial state)",
			"user_id", userID, "company", req.CompanyCode, "error", err)
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "user created in master but company db is unavailable")
		return
	}

	ctx := c.Request.Context()
	tx, err := targetDB.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("user.create: begin tx failed", "error", err, "company", req.CompanyCode)
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
		return
	}
	defer func() { _ = tx.Rollback() }() // no-op setelah Commit

	// role_override NULL → selalu mengikuti role global di master.users.
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO user_company_access (user_id, role_override, is_active)
		 VALUES (?, NULL, TRUE)
		 ON DUPLICATE KEY UPDATE is_active = TRUE`, userID); err != nil {
		slog.Error("user.create: upsert user_company_access failed", "error", err, "user_id", userID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to grant company access")
		return
	}

	assigned := 0
	if len(req.VehicleIDs) > 0 {
		// Validasi kepemilikan vehicle oleh tenant SEBELUM insert apa pun.
		placeholders := strings.TrimRight(strings.Repeat("?,", len(req.VehicleIDs)), ",")
		args := make([]interface{}, len(req.VehicleIDs))
		for i, id := range req.VehicleIDs {
			args[i] = id
		}
		var owned int
		if err = targetDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM vehicles WHERE id IN (`+placeholders+`)`, args...).Scan(&owned); err != nil {
			slog.Error("user.create: vehicle ownership check failed", "error", err, "company", req.CompanyCode)
			writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
			return
		}
		if owned != len(req.VehicleIDs) {
			writeError(c, http.StatusBadRequest, "VEHICLE_NOT_FOUND",
				fmt.Sprintf("some vehicles do not belong to company %s (%d/%d found)", req.CompanyCode, owned, len(req.VehicleIDs)))
			return
		}
		for _, id := range req.VehicleIDs {
			if _, err = tx.ExecContext(ctx,
				`INSERT IGNORE INTO user_vehicles (user_id, vehicle_id) VALUES (?, ?)`, userID, id); err != nil {
				slog.Error("user.create: insert user_vehicles failed", "error", err, "user_id", userID, "vehicle_id", id)
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to assign vehicles")
				return
			}
			assigned++
		}
	}

	if err = tx.Commit(); err != nil {
		slog.Error("user.create: commit failed", "error", err, "user_id", userID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to persist company access")
		return
	}

	internal.LogAudit(auditDB(), internal.AuditEntry{
		UserID:      uint64(userID),
		CompanyCode: req.CompanyCode,
		EventType:   "USER_CREATED",
		Action:      "user.create",
		Entity:      "user",
		EntityID:    req.Email,
		IP:          c.ClientIP(),
		UserAgent:   c.Request.UserAgent(),
	})
	slog.Info("platform provisioned tenant user", "user_id", userID, "email", req.Email,
		"company", req.CompanyCode, "role", role, "vehicles", assigned)

	writeSuccess(c, http.StatusCreated, userCreateResponse{
		ID:               uint64(userID),
		Email:            req.Email,
		FullName:         req.FullName,
		Role:             role,
		CompanyCode:      req.CompanyCode,
		VehiclesAssigned: assigned,
	})
}
