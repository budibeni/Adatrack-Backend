package controllers

import (
	"context"
	"sync"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
)

// natsMsg aliases the NATS message type used by subscription callbacks.
type natsMsg = nats.Msg

// WorkerAlert implements multi-tenant alert detection (geofence, speed,
// battery, offline, SOS, route deviation) by consuming telemetry.raw.>
// (queue group "alert") and routing every check to adatrack_gps_{company_code}
// via the tenant manager (PRD §6).
type WorkerAlert struct {
	cfg    *internal.Config
	tm     *tenant.Manager
	nac    *internal.NATSClient
	redis  *internal.RedisClient
	reg    *prometheus.Registry
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	routesMu    sync.RWMutex
	routes      map[string]*models.RouteAssignment // key: company|imei
	deviating   map[string]bool                    // key: assignmentKey -> currently outside threshold
	sosMu       sync.Mutex
	sosLast     map[string]time.Time // key: company|imei -> last SOS trigger time
	batteryMu   sync.Mutex
	batteryLast map[string]time.Time // dedup window for BATTERY_LOW

	metrics *alertMetrics
}

// store is the per-company data access surface used by the checks.
// Implementations live in repos.go and are instantiated per company pool.
type store interface {
	// VehicleByIMEI returns (vehicle_id, status) or sql.ErrNoRows.
	VehicleByIMEI(imei string) (uint64, string, error)
	// InsertAlert creates an alert row and returns its auto-increment id.
	InsertAlert(a models.AlertRecord) (uint64, error)
	// HasOpenAlert reports whether an open alert of the type already exists
	// for the vehicle (dedup for OFFLINE/BATTERY loops).
	HasOpenAlert(vehicleID uint64, alertType string) (bool, error)
	// SpeedConfigFor resolves the effective speed config (vehicle-specific
	// active row first, then global vehicle_id IS NULL).
	SpeedConfigFor(vehicleID uint64) (models.SpeedConfig, bool, error)
	// FuelConfigFor resolves the effective fuel config (B5a, migration 014):
	// vehicle-specific active row first, then global vehicle_id IS NULL.
	FuelConfigFor(vehicleID uint64) (models.FuelConfig, bool, error)
	// ActiveGeofences lists geofences applicable to the vehicle (linked via
	// geofence_vehicles; circle + polygon).
	ActiveGeofences(vehicleID uint64) ([]models.GeofenceDef, error)
	// LoadActiveAssignments joins route_assignments+routes+vehicles for rows
	// still being tracked (not_started/in_progress/delayed).
	LoadActiveAssignments() ([]models.RouteAssignment, error)
	// UpdateAssignmentStatus applies a lifecycle transition.
	UpdateAssignmentStatus(id uint64, status string) error
	// UpdateAssignmentDeviation stores the observed max deviation meters.
	UpdateAssignmentDeviation(id uint64, meters float64) error
	// EnabledPreferences lists enabled notification_preferences rows matching
	// an alert type exactly or the 'all' wildcard.
	EnabledPreferences(alertTypes []string) ([]models.NotifPreference, error)
	// VehicleUserIDs returns user ids having the vehicle assigned.
	VehicleUserIDs(vehicleID uint64) ([]uint64, error)
	// RecordNotification inserts a notifications audit row (migration 010).
	RecordNotification(userID uint64, alertID uint64, channel, alertType, subject, body, status, errMsg string) error
	// OpenSOSOlderThan lists SOS alerts still open after the given age cutoff.
	OpenSOSOlderThan(cutoff time.Time) ([]models.OpenSOSAlert, error)
	// GetAlert reads one alert row by id.
	GetAlert(id uint64) (models.AlertRecord, error)
}

// New builds a WorkerAlert wiring tenant manager, NATS, Redis and metrics.
func New(cfg *internal.Config, tm *tenant.Manager, nac *internal.NATSClient,
	redis *internal.RedisClient, reg *prometheus.Registry) (*WorkerAlert, error) {

	wa := &WorkerAlert{
		cfg:         cfg,
		tm:          tm,
		nac:         nac,
		redis:       redis,
		reg:         reg,
		routes:      make(map[string]*models.RouteAssignment),
		deviating:   make(map[string]bool),
		sosLast:     make(map[string]time.Time),
		batteryLast: make(map[string]time.Time),
		metrics:     newAlertMetrics(reg),
	}
	return wa, nil
}

// internalRegistry returns the gatherer backing /metrics for this service.
func (wa *WorkerAlert) internalRegistry() prometheus.Gatherer {
	if wa.reg != nil {
		return wa.reg
	}
	return prometheus.DefaultGatherer
}
