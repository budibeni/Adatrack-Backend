package models

// AuditLog is one append-only `tm_audit_logs` row (PRD §9.4) exposed to the
// tenant Admin. before/after snapshots are already redacted at write time.
type AuditLog struct {
	AuditID    int64  `json:"audit_id"`
	Action     string `json:"action"`
	Outcome    string `json:"outcome"`
	ActorID    *int64 `json:"actor_user_id,omitempty"`
	ActorEmail string `json:"actor_email,omitempty"`
	ActorRole  string `json:"actor_role,omitempty"`
	ActorIP    string `json:"actor_ip,omitempty"`
	// ActorUserAgent is intentionally omitted from the JSON projection: it can be
	// long and is only needed when investigating a specific row.
	CompanyCode string `json:"company_code,omitempty"`
	EntityType  string `json:"entity_type,omitempty"`
	EntityID    string `json:"entity_id,omitempty"`
	BeforeState any    `json:"before_state,omitempty"`
	AfterState  any    `json:"after_state,omitempty"`
	Reason      string `json:"reason,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
	CreatedAt   string `json:"created_at"`
}
