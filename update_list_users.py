import re

with open("/home/arfian107/Projects/adatrack/backend/services/service-websocket/internal/api/admin_handlers.go", "r") as f:
    content = f.read()

# 1. Add Tenants to UserInfo
content = re.sub(
    r'type UserInfo struct \{\n\tID.*?GlobalRole string `json:"global_role"`\n\}',
    'type UserInfo struct {\n\tID         int      `json:"id"`\n\tEmail      string   `json:"email"`\n\tIsActive   bool     `json:"is_active"`\n\tCreatedAt  string   `json:"created_at"`\n\tGlobalRole string   `json:"global_role"`\n\tTenants    []string `json:"tenants"`\n}',
    content,
    flags=re.DOTALL
)

# 2. Modify ListUsers to fetch tenants
replacement_logic = """	users := []UserInfo{}
	var userIDs []int
	userMap := make(map[int]*UserInfo)

	for rows.Next() {
		var u UserInfo
		var t time.Time
		if err := rows.Scan(&u.ID, &u.Email, &u.IsActive, &t, &u.GlobalRole); err == nil {
			u.CreatedAt = t.Format(time.RFC3339)
			u.Tenants = []string{}
			users = append(users, u)
		}
	}
	
	// Map users for O(1) access
	for i := range users {
		userIDs = append(userIDs, users[i].ID)
		userMap[users[i].ID] = &users[i]
	}

	if len(userIDs) > 0 {
		// Fetch all company codes
		compRows, err := dbclient.Pool.Query(ctx, "SELECT code FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL AND business_type = 'b2b'")
		if err == nil {
			var codes []string
			for compRows.Next() {
				var code string
				if err := compRows.Scan(&code); err == nil {
					codes = append(codes, code)
				}
			}
			compRows.Close()

			if len(codes) > 0 {
				var queryParts []string
				for _, code := range codes {
					schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(code))
					queryParts = append(queryParts, fmt.Sprintf("SELECT user_id, '%s' as company_code FROM %s.tm_user_company_access WHERE user_id = ANY($1) AND deleted_at IS NULL AND is_active = true", code, schema))
				}
				
				query := strings.Join(queryParts, " UNION ALL ")
				accessRows, err := dbclient.Pool.Query(ctx, query, userIDs)
				if err == nil {
					for accessRows.Next() {
						var uid int
						var ccode string
						if err := accessRows.Scan(&uid, &ccode); err == nil {
							if user, ok := userMap[uid]; ok {
								user.Tenants = append(user.Tenants, ccode)
							}
						}
					}
					accessRows.Close()
				}
			}
		}
	}"""

content = re.sub(
    r'\tusers := \[\]UserInfo\{\}\n\tfor rows\.Next\(\) \{\n\t\tvar u UserInfo\n\t\tvar t time\.Time\n\t\tif err := rows\.Scan\(&u\.ID, &u\.Email, &u\.IsActive, &t, &u\.GlobalRole\); err == nil \{\n\t\t\tu\.CreatedAt = t\.Format\(time\.RFC3339\)\n\t\t\tusers = append\(users, u\)\n\t\t\}\n\t\}',
    replacement_logic,
    content,
    flags=re.DOTALL
)

with open("/home/arfian107/Projects/adatrack/backend/services/service-websocket/internal/api/admin_handlers.go", "w") as f:
    f.write(content)

print("SUCCESS")
