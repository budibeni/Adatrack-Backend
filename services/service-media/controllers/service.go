package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
	"adatrack_gps/internal/storage"
	"adatrack_gps/service-media/models"
)

// Deps are the collaborators of service-media (production wiring in main.go).
type Deps struct {
	Settings Settings
	Store    Store
	Storage  storage.Store
	KV       KVStore
	NATS     *internal.NATSClient
	Registry *prometheus.Registry
}

// Service owns the HTTP engine, the media catalog handlers and the retention
// sweep of service-media (PRD Module 8 / Scope A, phase B5b).
type Service struct {
	settings Settings
	store    Store
	storage  storage.Store
	kv       KVStore
	nats     *internal.NATSClient
	registry *prometheus.Registry

	auth    *AuthService
	auditor *Auditor

	retention  *retentionScheduler
	metricsSrv *internal.HealthServer

	configMu    sync.RWMutex
	configCache []MediaConfig
	configAt    time.Time

	engine *gin.Engine
}

// NewService wires the service and its routes.
func NewService(deps Deps) *Service {
	s := &Service{
		settings: deps.Settings,
		store:    deps.Store,
		storage:  deps.Storage,
		kv:       deps.KV,
		nats:     deps.NATS,
		registry: deps.Registry,
	}
	s.auth = NewAuthService(deps.Settings, deps.KV)
	s.auditor = NewAuditor(deps.Store, deps.NATS, deps.Settings.AuditEnabled)
	s.engine = s.buildRouter()
	return s
}

// Handler returns the HTTP engine (tests use it with httptest).
func (s *Service) Handler() *gin.Engine { return s.engine }

// Storage exposes the object store (tests + readiness).
func (s *Service) Storage() storage.Store { return s.storage }

// Auditor exposes the audit writer (tests assert on it).
func (s *Service) Auditor() *Auditor { return s.auditor }

// Start launches the retention sweep and the dedicated metrics listener.
func (s *Service) Start(ctx context.Context) {
	s.startRetention(ctx)

	if s.settings.MetricsAddr == "" || s.settings.MetricsAddr == s.settings.HTTPAddr {
		return
	}
	health := internal.NewHealthServer(s.settings.MetricsAddr, s.registry,
		internal.HealthCheck{Name: "object_storage", Critical: true, Fn: func(ctx context.Context) error {
			return s.storage.Health(ctx)
		}})
	health.Start()
	s.metricsSrv = health
}

// Stop shuts the secondary health/metrics listener down.
func (s *Service) Stop() {
	if s.metricsSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.metricsSrv.Shutdown(ctx)
	}
}

// mediaConfig resolves the per-company media configuration (cached for
// MEDIA_CONFIG_CACHE_SEC so an upload does not query master on every request).
func (s *Service) mediaConfig(ctx context.Context, company string) (MediaConfig, error) {
	code := strings.ToUpper(strings.TrimSpace(company))
	ttl := s.settings.ConfigCacheTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	s.configMu.RLock()
	cached, at := s.configCache, s.configAt
	s.configMu.RUnlock()

	if cached != nil && time.Since(at) < ttl {
		return pickConfig(cached, code), nil
	}

	list, err := s.store.MediaCompanies(ctx)
	if err != nil {
		// A stale cache beats failing the request (the config rarely changes).
		if cached != nil {
			slog.Warn("service-media: media config refresh failed; serving stale cache", "error", err)
			return pickConfig(cached, code), nil
		}
		return MediaConfig{}, err
	}
	s.configMu.Lock()
	s.configCache, s.configAt = list, time.Now()
	s.configMu.Unlock()
	return pickConfig(list, code), nil
}

// pickConfig selects one company row (zero value when absent → env defaults).
func pickConfig(list []MediaConfig, code string) MediaConfig {
	for _, cfg := range list {
		if cfg.CompanyCode == code {
			return cfg
		}
	}
	for _, cfg := range list {
		if strings.EqualFold(cfg.CompanyCode, code) {
			return cfg
		}
	}
	return MediaConfig{CompanyCode: code}
}

// objectKey builds the documented layout `{company}/{vehicle}/{yyyyMM}/{uuid}.ext`
// (FR-8.2). The UUID comes from crypto/rand, so two captures never collide.
func (s *Service) objectKey(company string, vehicleID int64, captured time.Time, ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("media: random key: %w", err)
	}
	return fmt.Sprintf("%s/%d/%s/%s.%s", strings.ToLower(strings.TrimSpace(company)),
		vehicleID, captured.UTC().Format("200601"), hex.EncodeToString(buf), ext), nil
}

// publishMediaEvent publishes `media.event.<company_code>` (FR-8.5) so
// service-websocket can fan the MEDIA_EVENT out to entitled clients.
func (s *Service) publishMediaEvent(company string, data models.MediaEventData) error {
	if s.nats == nil {
		return nil
	}
	subject := s.nats.SubjectPlain("media", "event", strings.ToUpper(strings.TrimSpace(company)))
	body, err := json.Marshal(data)
	if err != nil {
		mediaEventsPublished.WithLabelValues("encode_error").Inc()
		return err
	}
	if err := s.nats.Publish(subject, body); err != nil {
		mediaEventsPublished.WithLabelValues("publish_error").Inc()
		return err
	}
	mediaEventsPublished.WithLabelValues("published").Inc()
	return nil
}
