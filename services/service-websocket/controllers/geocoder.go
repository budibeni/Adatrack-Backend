package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
	"adatrack_gps/internal/geo"
	"adatrack_gps/service-websocket/models"
)

// B7.3 metrics (PRD §10.1 style: every lookup outcome is observable).
var (
	geocodeRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "geocode_requests_total",
		Help: "Offline reverse-geocoding lookups by outcome (B7.3)",
	}, []string{"result"})
	geocodeIndexSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "geocode_index_size",
		Help: "Administrative centroids loaded into the offline geocoder index",
	})
	geocodeIndexLoadErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "geocode_index_load_errors_total",
		Help: "Failures while loading the master region reference data",
	})
)

// RegisterGeocoderMetrics registers the B7.3 collectors.
func RegisterGeocoderMetrics(reg *prometheus.Registry) {
	if reg == nil {
		return
	}
	reg.MustRegister(geocodeRequests, geocodeIndexSize, geocodeIndexLoadErrors)
}

// geocodeEntry is the cached resolution of one rounded coordinate.
type geocodeEntry struct {
	Address  geo.Address `json:"address"`
	Resolved bool        `json:"resolved"`
	expires  time.Time
}

// maxGeocodeMemory bounds the in-process cache (a full reset is the documented
// eviction policy: it only costs a Redis round trip per entry afterwards).
const maxGeocodeMemory = 5000

// geocoder resolves coordinates to an offline address (B7.3) from the master
// reference tables. Lookup order:
//
//	in-process cache → Redis (shared by replicas, survives restarts) →
//	region index (PostgreSQL, refreshed every GEOCODE_INDEX_REFRESH_SEC)
//
// A lookup NEVER fails a request: an unresolvable coordinate yields an empty
// address with resolved=false — the documented fallback of B7.3.
type geocoder struct {
	regions RegionStore
	redis   *internal.RedisClient
	prefix  string

	indexTTL   time.Duration
	cacheTTL   time.Duration
	maxKM      float64
	specificKM float64
	maxMemory  int

	mu        sync.RWMutex
	specific  *geo.Index // city/district/subdistrict centroids
	provinces *geo.Index // province fallback
	memory    map[string]geocodeEntry
	loadedAt  time.Time
	loading   bool
}

// newGeocoder builds the geocoder (the index is loaded lazily on first use).
func newGeocoder(settings Settings, regions RegionStore, redis *internal.RedisClient) *geocoder {
	ttl := settings.GeocodeIndexRefresh
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	cacheTTL := settings.GeocodeCacheTTL
	if cacheTTL <= 0 {
		cacheTTL = 6 * time.Hour
	}
	maxKM := settings.GeocodeMaxDistanceKM
	if maxKM <= 0 {
		maxKM = 75
	}
	specificKM := settings.GeocodeSpecificMaxKM
	if specificKM <= 0 {
		specificKM = 30
	}
	prefix := "adatrack_gps:geocode:"
	if redis != nil {
		prefix = redis.KeyPrefix() + "geocode:"
	}
	return &geocoder{
		regions:    regions,
		redis:      redis,
		prefix:     prefix,
		indexTTL:   ttl,
		cacheTTL:   cacheTTL,
		maxKM:      maxKM,
		specificKM: specificKM,
		maxMemory:  maxGeocodeMemory,
		memory:     map[string]geocodeEntry{},
	}
}

// Reverse resolves a coordinate (never returns an error: see the type doc).
func (g *geocoder) Reverse(ctx context.Context, lat, lon float64) (geo.Address, bool) {
	if g == nil {
		return geo.Address{}, false
	}
	key := g.cacheKey(lat, lon)

	g.mu.RLock()
	entry, ok := g.memory[key]
	g.mu.RUnlock()
	if ok && time.Now().Before(entry.expires) {
		geocodeRequests.WithLabelValues("hit_memory").Inc()
		return entry.Address, entry.Resolved
	}

	if addr, resolved, hit := g.fromRedis(ctx, key); hit {
		g.storeMemory(key, addr, resolved)
		geocodeRequests.WithLabelValues("hit_redis").Inc()
		return addr, resolved
	}

	addr, resolved := g.resolve(ctx, lat, lon)
	g.storeMemory(key, addr, resolved)
	g.storeRedis(ctx, key, addr, resolved)
	if resolved {
		geocodeRequests.WithLabelValues("resolved").Inc()
	} else {
		geocodeRequests.WithLabelValues("unresolved").Inc()
	}
	return addr, resolved
}

// Payload maps a resolution onto the response DTO (B7.3).
func Payload(addr geo.Address, resolved bool) models.AddressPayload {
	return models.AddressPayload{
		Village:    addr.Village,
		District:   addr.District,
		City:       addr.City,
		Province:   addr.Province,
		PostalCode: addr.PostalCode,
		Level:      addr.Level,
		DistanceKM: math.Round(addr.DistanceKM*1000) / 1000,
		Address:    addr.String(),
		Resolved:   resolved,
	}
}

// resolve runs the specificity policy of B7.3: the most specific available level
// wins when it is close enough (GEOCODE_SPECIFIC_MAX_KM); otherwise the resolver
// falls back to the province centroid (GEOCODE_MAX_DISTANCE_KM) instead of
// claiming a city that lies far away.
func (g *geocoder) resolve(ctx context.Context, lat, lon float64) (geo.Address, bool) {
	g.ensureIndex(ctx)

	g.mu.RLock()
	specific, provinces := g.specific, g.provinces
	g.mu.RUnlock()

	if specific != nil {
		if region, dist, ok := specific.Nearest(lat, lon, g.specificKM); ok {
			return region.Address(dist), true
		}
	}
	if provinces != nil {
		if region, dist, ok := provinces.Nearest(lat, lon, g.maxKM); ok {
			return region.Address(dist), true
		}
	}
	return geo.Address{}, false
}

// ensureIndex loads the index synchronously on a cold start and refreshes it in
// the background once it is older than GEOCODE_INDEX_REFRESH_SEC (the previous
// snapshot keeps serving while the refresh runs).
func (g *geocoder) ensureIndex(ctx context.Context) {
	if g.regions == nil {
		return
	}
	g.mu.RLock()
	fresh := g.specific != nil && time.Since(g.loadedAt) < g.indexTTL
	loading := g.loading
	g.mu.RUnlock()
	if fresh || loading {
		return
	}
	if g.specific == nil {
		g.load(ctx) // cold start: block this request, the index is required
		return
	}

	g.mu.Lock()
	if g.loading {
		g.mu.Unlock()
		return
	}
	g.loading = true
	g.mu.Unlock()

	go func() {
		defer func() {
			g.mu.Lock()
			g.loading = false
			g.mu.Unlock()
		}()
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 30*time.Second)
		defer cancel()
		g.load(loadCtx)
	}()
}

// load reads the reference rows and swaps the indexes atomically.
func (g *geocoder) load(ctx context.Context) {
	regions, err := g.regions.RegionCentroids(ctx)
	if err != nil {
		geocodeIndexLoadErrors.Inc()
		slog.Warn("service-websocket: region reference load failed; geocoding answers 'unknown'",
			"error", err)
		return
	}
	specificRows := make([]geo.Region, 0, len(regions))
	provinceRows := make([]geo.Region, 0, len(regions))
	for _, r := range regions {
		if r.Level == geo.LevelProvince {
			provinceRows = append(provinceRows, r)
			continue
		}
		specificRows = append(specificRows, r)
	}
	specific := geo.NewIndex(specificRows)
	provinces := geo.NewIndex(provinceRows)

	g.mu.Lock()
	g.specific, g.provinces, g.loadedAt = specific, provinces, time.Now()
	g.mu.Unlock()

	geocodeIndexSize.Set(float64(specific.Len() + provinces.Len()))
	slog.Info("service-websocket: offline geocoder index loaded",
		"specific", specific.Len(), "provinces", provinces.Len())
}

// round3 rounds to three decimals — the cache-key granularity (≈110 m, which is
// finer than the address level the reference data resolves).
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// cacheKey rounds the coordinate to ~110 m so neighbouring lookups share a cache
// entry (address granularity is city/district, so this is lossless in practice).
func (g *geocoder) cacheKey(lat, lon float64) string {
	return g.prefix + strconv.FormatFloat(round3(lat), 'f', 3, 64) + ":" +
		strconv.FormatFloat(round3(lon), 'f', 3, 64)
}

// fromRedis reads a cached resolution (a Redis failure is a cache miss).
func (g *geocoder) fromRedis(ctx context.Context, key string) (geo.Address, bool, bool) {
	if g.redis == nil {
		return geo.Address{}, false, false
	}
	raw, err := g.redis.Get(ctx, key)
	if err != nil || raw == "" {
		return geo.Address{}, false, false
	}
	var entry geocodeEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return geo.Address{}, false, false
	}
	return entry.Address, entry.Resolved, true
}

// storeRedis writes a resolution best-effort (a cache failure never fails the
// request).
func (g *geocoder) storeRedis(ctx context.Context, key string, addr geo.Address, resolved bool) {
	if g.redis == nil || g.cacheTTL <= 0 {
		return
	}
	body, err := json.Marshal(geocodeEntry{Address: addr, Resolved: resolved})
	if err != nil {
		return
	}
	if err := g.redis.Set(ctx, key, string(body), g.cacheTTL); err != nil {
		slog.Debug("service-websocket: geocode cache write failed", "error", err)
	}
}

// storeMemory fills the in-process cache (resetting it when the bound is hit).
func (g *geocoder) storeMemory(key string, addr geo.Address, resolved bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.memory) >= g.maxMemory {
		g.memory = map[string]geocodeEntry{}
	}
	g.memory[key] = geocodeEntry{
		Address:  addr,
		Resolved: resolved,
		expires:  time.Now().Add(g.cacheTTL),
	}
}

// indexSize reports the loaded index size (health/debug + tests).
func (g *geocoder) indexSize() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n := 0
	if g.specific != nil {
		n += g.specific.Len()
	}
	if g.provinces != nil {
		n += g.provinces.Len()
	}
	return n
}
