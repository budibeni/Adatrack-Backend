import re

with open('services/service-websocket/internal/api/handlers.go', 'r') as f:
    content = f.read()

new_func = """func (h *Handler) CreateCompany(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		CountryCode string `json:"country_code"`
		Timezone    string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	
	// Create company in master
	_, err := dbclient.Pool.Exec(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_companies (code, name, country_code, timezone, business_type)
		VALUES ($1, $2, $3, $4, 'b2b')
	`, req.Code, req.Name, req.CountryCode, req.Timezone)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create company")
		return
	}
	
	// Create user in master
	adminEmail := fmt.Sprintf("admin@%s.local", req.Code)
	hash, _ := bcrypt.GenerateFromPassword([]byte("Admin@123"), 12)
	
	var newUserID int64
	err = dbclient.Pool.QueryRow(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_users (email, password_hash, global_role, must_change_password)
		VALUES ($1, $2, 'Admin', true) RETURNING id
	`, adminEmail, string(hash)).Scan(&newUserID)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create admin user")
		return
	}
	
	schema := fmt.Sprintf("adatrack_gps_%s", req.Code)
	_, err = dbclient.Pool.Exec(r.Context(), fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema))
	if err != nil {
		logger.Log.Error("Failed to create schema", "err", err)
	} else {
	    // Apply migrations
	    files, _ := filepath.Glob("../../database/migrations/company_pg/*.up.sql")
	    for _, file := range files {
	        sqlBytes, err := os.ReadFile(file)
	        if err == nil {
	            // Set search path for this execution
	            execSQL := fmt.Sprintf("SET search_path TO %s; %s", schema, string(sqlBytes))
	            _, err := dbclient.Pool.Exec(r.Context(), execSQL)
	            if err != nil {
	                logger.Log.Error("Migration failed", "file", file, "err", err)
	            }
	        }
	    }
	    
	    // Give admin user access
	    dbclient.Pool.Exec(r.Context(), fmt.Sprintf("INSERT INTO %s.tm_user_company_access (user_id, role_override) VALUES ($1, 'Admin')", schema), newUserID)
	}
	
	h.auditLog(r.Context(), req.Code, "COMPANY_CREATED", "success", claims.UserID, claims.Email, claims.Role, "Company "+req.Code+" created")
	h.auditLog(r.Context(), req.Code, "TENANT_PROVISIONED", "success", claims.UserID, claims.Email, claims.Role, "Tenant provisioned")
	h.auditLog(r.Context(), req.Code, "ADMIN_USER_AUTOCREATED", "success", claims.UserID, claims.Email, claims.Role, "Admin user created")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"code": req.Code,
			"admin_user": map[string]interface{}{
				"email": adminEmail,
				"must_change_password": true,
			},
		},
	})
}"""

content = re.sub(r'func \(h \*Handler\) CreateCompany.*?\}\n\}', new_func, content, flags=re.DOTALL)
if '"path/filepath"' not in content:
    content = content.replace('"net/http"', '"net/http"\n\t"path/filepath"\n\t"os"')

with open('services/service-websocket/internal/api/handlers.go', 'w') as f:
    f.write(content)

