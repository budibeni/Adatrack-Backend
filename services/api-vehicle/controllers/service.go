package controllers

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/api-vehicle/models"
	"adatrack_gps/internal/tenant"
)

// Deps are the collaborators of the service (production wiring in main.go).
type Deps struct {
	Settings Settings
	Store    Store
	KV       *RedisKV
	// Live is the live-state overlay source (phase B6). When nil the service
	// falls back to KV; tests inject a stub instead of a real Redis.
	Live     LiveStateReader
	Tenants  *tenant.Manager
	Registry *prometheus.Registry
	// Commands publishes B8 downlink requests to the ingestion tier. When nil the
	// command endpoints report 503 instead of silently dropping the request.
	Commands CommandPublisher
}

// Service owns the HTTP engine and every handler of api-vehicle (PRD §8.2
// fleet management, phase B3).
type Service struct {
	settings Settings
	store    Store
	kv       *RedisKV
	live     LiveStateReader
	tenants  *tenant.Manager
	registry *prometheus.Registry
	commands CommandPublisher

	auth   *AuthService
	engine *gin.Engine
}

// NewService wires the service and its routes.
func NewService(deps Deps) *Service {
	s := &Service{
		settings: deps.Settings,
		store:    deps.Store,
		kv:       deps.KV,
		live:     deps.Live,
		tenants:  deps.Tenants,
		registry: deps.Registry,
		commands: deps.Commands,
	}
	if s.live == nil && deps.KV != nil {
		s.live = deps.KV
	}
	s.auth = NewAuthService(deps.Settings, deps.KV)
	s.engine = s.buildRouter()
	return s
}

// Handler returns the HTTP engine (tests use it with httptest).
func (s *Service) Handler() *gin.Engine { return s.engine }

// PostgresStore returns the store as a *PostgresStore when available.
func (s *Service) PostgresStore() (*PostgresStore, bool) {
	pg, ok := s.store.(*PostgresStore)
	return pg, ok
}

// vehicleStoreErr maps a persistence failure onto the generic 503 the PRD §8.1
// contract prescribes (internal details are never leaked to the client).
func vehicleStoreErr(err error) error {
	_ = err
	return errUnavailable("data source unavailable")
}

// deniedListGuard rejects `include_deleted=true` for non-Admin identities
// (PRD §6.0.1: only the tenant Admin may look behind the soft-delete veil).
func deniedListGuard(identity *tenantIdentity, includeDel bool) *APIError {
	if includeDel && identity.role != models.RoleAdmin {
		return errForbidden(CodeForbidden, "only Admin may list deleted records")
	}
	return nil
}
