package models

// --- B12 enterprise module DTOs (PRD §5.10) ---------------------------------

// MenuItem is one navigable menu (master `tm_menus`) joined with the caller's role
// access (tenant `tm_role_menu_access`). The dashboard renders navigation from
// GET /api/v1/access/menu — no menu is ever hardcoded in the frontend.
type MenuItem struct {
	MenuID     int64  `json:"menu_id"`
	ModuleCode string `json:"module_code"`
	ModuleName string `json:"module_name"`
	App        string `json:"app"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	ParentID   *int64 `json:"parent_id,omitempty"`
	Icon       string `json:"icon,omitempty"`
	SortOrder  int    `json:"sort_order"`
	CanView    bool   `json:"can_view"`
	CanCreate  bool   `json:"can_create"`
	CanEdit    bool   `json:"can_edit"`
	CanDelete  bool   `json:"can_delete"`
}

// RoleMenuAccess is one row of the role→menu matrix (admin CRUD).
type RoleMenuAccess struct {
	MenuID    int64  `json:"menu_id"`
	MenuCode  string `json:"menu_code"`
	MenuName  string `json:"menu_name"`
	CanView   bool   `json:"can_view"`
	CanCreate bool   `json:"can_create"`
	CanEdit   bool   `json:"can_edit"`
	CanDelete bool   `json:"can_delete"`
	Enabled   bool   `json:"enabled"`
}

// MenuAccessEntry is one requested matrix cell (PUT body element).
type MenuAccessEntry struct {
	MenuID    int64 `json:"menu_id" binding:"required,min=1"`
	CanView   bool  `json:"can_view"`
	CanCreate bool  `json:"can_create"`
	CanEdit   bool  `json:"can_edit"`
	CanDelete bool  `json:"can_delete"`
	Enabled   *bool `json:"enabled,omitempty"`
}

// UpsertRoleMenuRequest is the PUT /api/v1/access/menu/role/{role} body.
type UpsertRoleMenuRequest struct {
	Menus []MenuAccessEntry `json:"menus" binding:"required,min=1,dive"`
}

// ModuleLicense is one module of the tenant licence registry; industry modules are
// opt-in per tenant (B12 task 11).
type ModuleLicense struct {
	ModuleCode string  `json:"module_code"`
	Name       string  `json:"name"`
	App        string  `json:"app"`
	Industry   bool    `json:"industry"`
	Enabled    bool    `json:"enabled"`
	LicensedAt *string `json:"licensed_at,omitempty"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
}

// UpsertModuleLicenseRequest is the PUT /api/v1/modules/:code body.
type UpsertModuleLicenseRequest struct {
	Enabled   bool    `json:"enabled"`
	ExpiresAt *string `json:"expires_at" binding:"omitempty,max=40"`
}

// Integration is one outbound API key / webhook configuration. `secret` appears
// only in the CREATE response (one-time display); only its hash is stored.
type Integration struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	EndpointURL *string  `json:"endpoint_url,omitempty"`
	KeyPrefix   *string  `json:"key_prefix,omitempty"`
	Events      []string `json:"events,omitempty"`
	Status      string   `json:"status"`
	LastUsedAt  *string  `json:"last_used_at,omitempty"`
	Notes       *string  `json:"notes,omitempty"`
	CreatedAt   string   `json:"created_at"`
	Secret      string   `json:"secret,omitempty"`
}

// UpsertIntegrationRequest is the POST/PATCH body of /integrations.
type UpsertIntegrationRequest struct {
	Name        string   `json:"name" binding:"omitempty,min=2,max=120"`
	Kind        string   `json:"kind" binding:"omitempty,oneof=api_key webhook"`
	EndpointURL *string  `json:"endpoint_url" binding:"omitempty,max=512"`
	Events      []string `json:"events" binding:"omitempty,max=50,dive,max=64"`
	Status      *string  `json:"status" binding:"omitempty,oneof=active disabled"`
	Notes       *string  `json:"notes" binding:"omitempty,max=500"`
}

// ShareLink is a public, TTL-bound location share (FR-9.3). CompanyCode is the
// owner tenant of the token — it is required internally to load the vehicles of
// the PUBLIC endpoint and is therefore never serialised to a client.
type ShareLink struct {
	ID           int64   `json:"id"`
	CompanyCode  string  `json:"-"`
	Token        string  `json:"token"`
	Label        *string `json:"label,omitempty"`
	Scope        string  `json:"scope"`
	VehicleIDs   []int64 `json:"vehicle_ids,omitempty"`
	ExpiresAt    string  `json:"expires_at"`
	RevokedAt    *string `json:"revoked_at,omitempty"`
	ViewCount    int64   `json:"view_count"`
	LastViewedAt *string `json:"last_viewed_at,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

// CreateShareLinkRequest is the POST /api/v1/share-links body.
type CreateShareLinkRequest struct {
	Label      *string `json:"label" binding:"omitempty,max=120"`
	VehicleIDs []int64 `json:"vehicle_ids" binding:"required,min=1,dive,min=1"`
	TTLMinutes int     `json:"ttl_minutes" binding:"required,min=5,max=10080"`
}

// SharedVehicle is one vehicle of a public share payload (position only).
type SharedVehicle struct {
	VehicleID   int64    `json:"vehicle_id"`
	PlateNumber string   `json:"plate_number"`
	Lat         *float64 `json:"lat,omitempty"`
	Lon         *float64 `json:"lon,omitempty"`
	Speed       *float64 `json:"speed,omitempty"`
	LastSeenAt  *string  `json:"last_seen_at,omitempty"`
}

// HeatmapCell is one aggregated density cell (Pemantauan menu).
type HeatmapCell struct {
	CellLat     float64 `json:"cell_lat"`
	CellLon     float64 `json:"cell_lon"`
	SampleCount int64   `json:"sample_count"`
}

// TripReport is the trip summary of one period (Analysis → Reports).
type TripReport struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	TripCount   int64   `json:"trip_count"`
	DistanceKm  float64 `json:"distance_km"`
	AvgSpeedKmh float64 `json:"avg_speed_kmh"`
	MaxSpeedKmh float64 `json:"max_speed_kmh"`
	StopCount   int64   `json:"stop_count"`
}

// ViolationReportRow is one violation type of the period (Analysis → Analytics).
type ViolationReportRow struct {
	EventType string `json:"event_type"`
	Severity  string `json:"severity"`
	Count     int64  `json:"count"`
}

// SafetyScore is one vehicle score row (Safety menu) derived from B8 behaviour.
type SafetyScore struct {
	VehicleID         int64   `json:"vehicle_id"`
	DriverID          *int64  `json:"driver_id,omitempty"`
	PeriodStart       string  `json:"period_start"`
	PeriodEnd         string  `json:"period_end"`
	Score             float64 `json:"score"`
	Grade             string  `json:"grade"`
	HarshAcceleration int64   `json:"harsh_acceleration_count"`
	HarshBraking      int64   `json:"harsh_braking_count"`
	HarshCornering    int64   `json:"harsh_cornering_count"`
	SpeedingCount     int64   `json:"speeding_count"`
	SpeedingSeconds   int64   `json:"speeding_seconds"`
	ComputedAt        string  `json:"computed_at"`
}

// GroupMember is one membership of a group (vehicle or driver).
type GroupMember struct {
	ID         int64  `json:"id"`
	GroupID    int64  `json:"group_id"`
	MemberType string `json:"member_type"`
	MemberID   int64  `json:"member_id"`
	CreatedAt  string `json:"created_at"`
}

// AddGroupMemberRequest is the POST /api/v1/groups/:id/members body.
type AddGroupMemberRequest struct {
	MemberType string `json:"member_type" binding:"required,oneof=vehicle driver"`
	MemberID   int64  `json:"member_id" binding:"required,min=1"`
}
