package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"ajb_gps/internal"
)

// Deps are the collaborators of the service (production wiring in main.go).
type Deps struct {
	Settings Settings
	Store    Store
	KV       KVStore
	Live     LiveStateStore
	Redis    *internal.RedisClient
	NATS     *internal.NATSClient
	Registry *prometheus.Registry
}

// Service owns the HTTP engine, the WebSocket hub and every handler of
// service-websocket (PRD Module 5: REST + WebSocket + RBAC + auth).
type Service struct {
	settings Settings
	store    Store
	kv       KVStore
	live     LiveStateStore
	redis    *internal.RedisClient
	nats     *internal.NATSClient
	registry *prometheus.Registry

	auth    *AuthService
	auditor *Auditor
	hub     *Hub

	engine *gin.Engine
	plates *plateCache
}

// NewService wires the service and its routes.
func NewService(deps Deps) *Service {
	s := &Service{
		settings: deps.Settings,
		store:    deps.Store,
		kv:       deps.KV,
		live:     deps.Live,
		redis:    deps.Redis,
		nats:     deps.NATS,
		registry: deps.Registry,
	}
	s.auditor = NewAuditor(deps.Store, deps.NATS, deps.Settings)
	s.auth = NewAuthService(deps.Settings, deps.Store, deps.KV, s.auditor)
	s.plates = newPlateCache(deps.Store)
	s.hub = NewHub(deps.Settings, s.plates)
	s.engine = s.buildRouter()
	return s
}

// Hub exposes the WebSocket hub (the NATS bridge publishes into it).
func (s *Service) Hub() *Hub { return s.hub }

// Auth exposes the auth service (the NATS bridge / tests may need it).
func (s *Service) Auth() *AuthService { return s.auth }

// Auditor exposes the audit writer (tests assert on it).
func (s *Service) Auditor() *Auditor { return s.auditor }

// Start launches the audit flusher and the WebSocket hub.
func (s *Service) Start() {
	s.auditor.Start()
	s.hub.Start()
}

// Stop drains the audit buffer and closes every WebSocket connection.
func (s *Service) Stop() {
	s.hub.Stop()
	s.auditor.Stop()
}

// Handler returns the HTTP engine (tests use it with httptest).
func (s *Service) Handler() *gin.Engine { return s.engine }

// PostgresStore returns the store as a *PostgresStore when available (readiness).
func (s *Service) PostgresStore() (*PostgresStore, bool) {
	pg, ok := s.store.(*PostgresStore)
	return pg, ok
}

// Readiness aggregates the critical dependencies (PRD §10.2).
func (s *Service) Readiness(ctx context.Context, store *PostgresStore) error {
	var errs []error
	if s.kv != nil {
		if err := s.kv.Ping(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if store != nil {
		if err := store.Master().Ping(ctx); err != nil {
			errs = append(errs, fmt.Errorf("postgres_master: %w", err))
		}
		if err := store.TenantHealth(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tenant_pools: %w", err))
		}
	}
	if s.nats != nil && !s.nats.IsConnected() {
		errs = append(errs, errNATS)
	}
	return errors.Join(errs...)
}

// plateCache resolves vehicle plate numbers for the WebSocket payload with a
// short TTL so the fan-out never issues a query per message (FR-5.2 includes
// `plate_number` in every VEHICLE_UPDATE).
type plateCache struct {
	store Store
	ttl   time.Duration

	mu         sync.RWMutex
	data       map[string]plateEntry
	refreshing map[string]bool
}

// plateEntry is one tenant's vehicle id → plate map plus its expiry.
type plateEntry struct {
	plates  map[int64]string
	expires time.Time
}

// newPlateCache builds the cache (60 s TTL keeps plate changes visible quickly).
func newPlateCache(store Store) *plateCache {
	return &plateCache{
		store:      store,
		ttl:        60 * time.Second,
		data:       map[string]plateEntry{},
		refreshing: map[string]bool{},
	}
}

// Plate returns the plate number of a vehicle ("" when unknown). A cache miss
// triggers a SYNCHRONOUS refresh — only used by request-scoped callers.
func (p *plateCache) Plate(ctx context.Context, companyCode string, vehicleID int64) string {
	if p == nil || p.store == nil || vehicleID <= 0 {
		return ""
	}
	p.mu.RLock()
	entry, ok := p.data[companyCode]
	p.mu.RUnlock()
	if !ok || time.Now().After(entry.expires) {
		entry = p.refresh(ctx, companyCode)
	}
	return entry.plates[vehicleID]
}

// plateCachedOrScheduleRefresh is the fan-out path: it serves from the cache and,
// on a miss, triggers a refresh in the background so a NATS callback never blocks
// on a database round trip (the current message simply carries no plate).
func (p *plateCache) plateCachedOrScheduleRefresh(companyCode string, vehicleID int64) string {
	if p == nil || p.store == nil || vehicleID <= 0 {
		return ""
	}
	p.mu.RLock()
	entry, ok := p.data[companyCode]
	refresh := p.refreshing[companyCode]
	p.mu.RUnlock()

	if ok && time.Now().Before(entry.expires) {
		return entry.plates[vehicleID]
	}
	if !refresh {
		p.mu.Lock()
		if p.refreshing == nil {
			p.refreshing = map[string]bool{}
		}
		if !p.refreshing[companyCode] {
			p.refreshing[companyCode] = true
			go p.backgroundRefresh(companyCode)
		}
		p.mu.Unlock()
	}
	return entry.plates[vehicleID]
}

// backgroundRefresh reloads one tenant's plates and clears the in-flight flag.
func (p *plateCache) backgroundRefresh(companyCode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.refresh(ctx, companyCode)
	p.mu.Lock()
	delete(p.refreshing, companyCode)
	p.mu.Unlock()
}

// refresh reloads one tenant's plate map (best effort: a failure keeps the
// previous map so live updates keep flowing).
func (p *plateCache) refresh(ctx context.Context, companyCode string) plateEntry {
	entry := plateEntry{plates: map[int64]string{}, expires: time.Now().Add(p.ttl)}
	vehicles, _, err := p.store.ListVehicles(ctx, VehicleQuery{
		CompanyCode: companyCode, AllVehicles: true, Page: 1, Limit: 1000,
	})
	if err != nil {
		slog.Warn("service-websocket: plate cache refresh failed", "company", companyCode, "error", err)
		return entry
	}
	for _, v := range vehicles {
		entry.plates[v.ID] = v.PlateNumber
	}
	p.mu.Lock()
	p.data[companyCode] = entry
	p.mu.Unlock()
	return entry
}
